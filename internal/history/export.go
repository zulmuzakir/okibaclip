package history

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

const (
	// ExportFormat identifies export files.
	ExportFormat = "pastello-export"
	// legacyExportFormat identifies export files written before the app
	// was renamed from my-clipboard; their content is identical.
	legacyExportFormat = "my-clipboard-export"
	ExportVersion      = 1
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
}

type ExportSnippet struct {
	Name      string `json:"name"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
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
	if f.Format != ExportFormat && f.Format != legacyExportFormat {
		return f, fmt.Errorf("not a pastello export (format %q)", f.Format)
	}
	if f.Version != ExportVersion {
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
			Pinned: e.Pinned, CreatedAt: ms(e.CreatedAt), UsedAt: ms(e.UsedAt), UseCount: e.UseCount})
	}
	sn, err := s.Snippets(ctx, nil)
	if err != nil {
		return f, err
	}
	for _, x := range sn {
		f.Snippets = append(f.Snippets, ExportSnippet{Name: x.Name, Body: x.Body, CreatedAt: ms(x.CreatedAt), UpdatedAt: ms(x.UpdatedAt)})
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
	for _, e := range f.Entries {
		switch e.Kind {
		case KindText, KindLink, KindColor, KindFiles, KindImage:
		default:
			continue
		}
		c := Capture{Kind: e.Kind, Text: e.Text, HTML: e.HTML, Image: e.Image, Width: e.Width, Height: e.Height}
		if (e.Kind == KindImage) != (len(e.Image) > 0) {
			continue
		}
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
		var thumb []byte
		if e.Kind == KindImage {
			thumb, _ = MakeThumb(e.Image)
		}
		var html any
		if e.HTML != "" {
			html = e.HTML
		}
		usedAt := e.UsedAt
		if usedAt == 0 {
			usedAt = ms(time.Now())
		}
		created := e.CreatedAt
		if created == 0 {
			created = usedAt
		}
		count := max(e.UseCount, 1)
		stmts = append(stmts, sqlite.Statement{
			SQL: `INSERT INTO entries (kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(hash) DO UPDATE SET pinned=max(pinned,excluded.pinned), used_at=max(used_at,excluded.used_at), use_count=use_count+excluded.use_count`,
			Args: []any{string(e.Kind), e.Text, searchText(e.Text), html, nullable(e.Image), nullable(thumb), e.Width, e.Height, c.Size(), hash, e.Pinned, created, usedAt, count},
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
		stmts = append(stmts, sqlite.Statement{SQL: `INSERT INTO snippets (name,body,created_at,updated_at) VALUES (?,?,?,?)`, Args: []any{sn.Name, sn.Body, created, updated}})
		snippets++
		if err := flush(); err != nil { // one at a time so the dedupe query above sees earlier inserts
			return entries, snippets, dupes, err
		}
	}
	return entries, snippets, dupes, nil
}
