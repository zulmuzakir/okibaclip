package history

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/sqlite"
)

const (
	// ExportFormat identifies export files.
	ExportFormat  = "okibaclip-export"
	ExportVersion = 2
	// MaxImportSize is the largest export file Import accepts, in bytes.
	MaxImportSize = 512 << 20
)

type ExportEntry struct {
	Kind      Kind   `json:"kind"`
	Text      string `json:"text"`
	HTML      string `json:"html"`
	Image     []byte `json:"image"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Pinned    bool   `json:"pinned"`
	CreatedAt int64  `json:"createdAt"`
	UsedAt    int64  `json:"usedAt"`
	UseCount  int    `json:"useCount"`
	// Added in version 2.
	Title      string `json:"title,omitempty"`
	PinOrder   int64  `json:"pinOrder,omitempty"`
	SourceApp  string `json:"sourceApp,omitempty"`
	SourceName string `json:"sourceName,omitempty"`
}

type ExportSnippet struct {
	Name      string `json:"name"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// Added in version 2.
	Folder   string `json:"folder,omitempty"`
	Shortcut string `json:"shortcut,omitempty"`
	UseCount int    `json:"useCount,omitempty"`
}

type ExportFile struct {
	Format     string          `json:"format"`
	Version    int             `json:"version"`
	ExportedAt string          `json:"exportedAt"`
	Entries    []ExportEntry   `json:"entries"`
	Snippets   []ExportSnippet `json:"snippets"`
}

// DecodeExport parses and validates an export file's bytes.
func DecodeExport(b []byte) (ExportFile, error) {
	var f ExportFile
	if err := json.Unmarshal(b, &f); err != nil {
		return f, err
	}
	if f.Format != ExportFormat {
		return f, fmt.Errorf("not an Okiba Clip export (format %q)", f.Format)
	}
	if f.Version != 1 && f.Version != ExportVersion {
		return f, fmt.Errorf("unsupported export version %d", f.Version)
	}
	return f, nil
}

// Export returns every entry and snippet in export form.
func (s *Store) Export(ctx context.Context) (ExportFile, error) {
	f := ExportFile{Format: ExportFormat, Version: ExportVersion, ExportedAt: time.Now().Format(time.RFC3339), Entries: []ExportEntry{}, Snippets: []ExportSnippet{}}
	rows, err := s.db.Query(ctx, `SELECT `+fullCols+` FROM entries ORDER BY used_at DESC`)
	if err != nil {
		return f, err
	}
	for _, r := range rows.Values {
		e := scanFull(r)
		f.Entries = append(f.Entries, ExportEntry{Kind: e.Kind, Text: e.Text, HTML: e.HTML, Image: e.Image, Width: e.Width, Height: e.Height,
			Pinned: e.Pinned, CreatedAt: ms(e.CreatedAt), UsedAt: ms(e.UsedAt), UseCount: e.UseCount,
			Title: e.Title, PinOrder: e.PinOrder, SourceApp: e.SourceApp, SourceName: e.SourceName})
	}
	sn, err := s.Snippets(ctx, nil)
	if err != nil {
		return f, err
	}
	for _, x := range sn {
		f.Snippets = append(f.Snippets, ExportSnippet{Name: x.Name, Body: x.Body, CreatedAt: ms(x.CreatedAt), UpdatedAt: ms(x.UpdatedAt),
			Folder: x.Folder, Shortcut: x.Shortcut, UseCount: x.UseCount})
	}
	return f, nil
}

// Import merges f: entries upsert by recomputed hash, snippets dedupe on
// (name, body). dupes counts merged entries and skipped snippets.
func (s *Store) Import(ctx context.Context, f ExportFile) (entries, snippets, dupes int, err error) {
	seen := map[string]bool{}
	var stmts []sqlite.Statement
	flush := func() error {
		if len(stmts) == 0 {
			return nil
		}
		_, err := s.db.Transaction(ctx, stmts)
		stmts = stmts[:0]
		return err
	}
	now := ms(time.Now())
	for _, e := range f.Entries {
		switch e.Kind {
		case KindText, KindLink, KindColor, KindFiles, KindImage:
		default:
			continue
		}
		if (e.Kind == KindImage) != (len(e.Image) > 0) {
			continue
		}
		switch e.Kind {
		case KindText, KindLink, KindColor:
			e.Kind = TextKind(e.Text)
		case KindFiles:
			if strings.TrimSpace(e.Text) == "" {
				continue
			}
		}
		var thumb []byte
		if e.Kind == KindImage {
			var terr error
			thumb, terr = MakeThumb(e.Image)
			if terr != nil {
				continue
			}
		} else if len(e.Text) > MaxTextBytes {
			continue
		}
		c := Capture{Kind: e.Kind, Text: e.Text, HTML: e.HTML, Image: e.Image, Width: e.Width, Height: e.Height}
		hash := c.Hash()
		existing := seen[hash]
		if !existing {
			rows, err := s.db.Query(ctx, `SELECT 1 FROM entries WHERE hash=?`, hash)
			if err != nil {
				return entries, snippets, dupes, err
			}
			existing = len(rows.Values) > 0
		}
		if existing {
			dupes++
		} else {
			entries++
		}
		seen[hash] = true
		var html any
		if e.HTML != "" {
			html = e.HTML
		}
		usedAt := e.UsedAt
		if usedAt <= 0 || usedAt > now {
			usedAt = now
		}
		created := e.CreatedAt
		if created <= 0 || created > usedAt {
			created = usedAt
		}
		count := max(e.UseCount, 1)
		title := ""
		if e.Pinned {
			title = cutRunes(strings.TrimSpace(e.Title), 100)
		}
		stmts = append(stmts, sqlite.Statement{
			SQL: `INSERT INTO entries (kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count,title,pin_order,source_app,source_name) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(hash) DO UPDATE SET pinned=max(pinned,excluded.pinned), used_at=max(used_at,excluded.used_at), use_count=max(use_count,excluded.use_count),
  title=CASE WHEN title='' THEN excluded.title ELSE title END,
  search=CASE WHEN title='' AND excluded.title!='' THEN excluded.search ELSE search END,
  pin_order=CASE WHEN pinned=0 AND excluded.pinned=1 THEN excluded.pin_order ELSE pin_order END,
  source_app=CASE WHEN source_app='' THEN excluded.source_app ELSE source_app END,
  source_name=CASE WHEN source_name='' THEN excluded.source_name ELSE source_name END`,
			Args: []any{string(e.Kind), e.Text, searchFor(title, e.Text), html, nullable(e.Image), nullable(thumb), e.Width, e.Height, c.Size(), hash, e.Pinned, created, usedAt, count, title, e.PinOrder, e.SourceApp, e.SourceName},
		})
		if len(stmts) >= 200 {
			if err := flush(); err != nil {
				return entries, snippets, dupes, err
			}
		}
	}
	if err := flush(); err != nil {
		return entries, snippets, dupes, err
	}
	for _, sn := range f.Snippets {
		rows, err := s.db.Query(ctx, `SELECT 1 FROM snippets WHERE name=? AND body=?`, sn.Name, sn.Body)
		if err != nil {
			return entries, snippets, dupes, err
		}
		if len(rows.Values) > 0 {
			dupes++
			continue
		}
		created, updated := sn.CreatedAt, sn.UpdatedAt
		if created == 0 {
			created = ms(time.Now())
		}
		if updated == 0 {
			updated = created
		}
		stmts = append(stmts, sqlite.Statement{SQL: `INSERT INTO snippets (name,body,folder,shortcut,use_count,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, Args: []any{sn.Name, sn.Body, sn.Folder, sn.Shortcut, max(sn.UseCount, 0), created, updated}})
		snippets++
		if err := flush(); err != nil { // one at a time so the dedupe query above sees earlier inserts
			return entries, snippets, dupes, err
		}
	}
	return entries, snippets, dupes, nil
}

// cutRunes shortens s to at most n runes.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
