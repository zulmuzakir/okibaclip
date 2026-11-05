package history

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/sqlite"

	"okibaclip/internal/accel"
)

const (
	// ExportFormat identifies export files.
	ExportFormat  = "okibaclip-export"
	ExportVersion = 2
	// MaxImportSize is the largest export file Import accepts, in bytes.
	MaxImportSize = 512 << 20
	// maxImportImage is the largest image Import accepts; it matches the MaxImageMB ceiling.
	maxImportImage = 50 << 20
	// exportPageSize bounds how many entries WriteExport holds in memory at once.
	exportPageSize = 200
)

// ErrExportTooLarge means the export would exceed MaxImportSize, so Import
// could not read it back. Delete large images or lower the storage limit.
var ErrExportTooLarge = fmt.Errorf("export would be over the %d MiB import limit; delete large images or lower the storage limit first", MaxImportSize>>20)

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

// exportWriter writes JSON pieces and keeps the first error, so callers check once.
type exportWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (x *exportWriter) raw(s string) {
	x.write([]byte(s))
}

func (x *exportWriter) write(b []byte) {
	if x.err != nil {
		return
	}
	if x.n+int64(len(b)) > MaxImportSize {
		x.err = ErrExportTooLarge
		return
	}
	n, err := x.w.Write(b)
	x.n += int64(n)
	x.err = err
}

func (x *exportWriter) value(v any) {
	if x.err != nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		x.err = err
		return
	}
	x.write(b)
}

// WriteExport streams every entry and snippet to w as an export file. Entries
// are read in pages keyed on (used_at, id), so memory stays bounded by one page.
func (s *Store) WriteExport(ctx context.Context, w io.Writer) error {
	x := &exportWriter{w: w}
	x.raw(`{"format":`)
	x.value(ExportFormat)
	x.raw(`,"version":`)
	x.value(ExportVersion)
	x.raw(`,"exportedAt":`)
	x.value(time.Now().Format(time.RFC3339))
	x.raw(`,"entries":[`)

	usedBefore, idBefore := int64(math.MaxInt64), int64(math.MaxInt64)
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := s.db.Query(ctx, `SELECT `+fullCols+` FROM entries WHERE (used_at,id) < (?,?) ORDER BY used_at DESC, id DESC LIMIT ?`, usedBefore, idBefore, exportPageSize)
		if err != nil {
			return err
		}
		page := scanRows(rows, scanFull)
		for _, e := range page {
			if !first {
				x.raw(",")
			}
			first = false
			x.value(ExportEntry{Kind: e.Kind, Text: e.Text, HTML: e.HTML, Image: e.Image, Width: e.Width, Height: e.Height,
				Pinned: e.Pinned, CreatedAt: ms(e.CreatedAt), UsedAt: ms(e.UsedAt), UseCount: e.UseCount,
				Title: e.Title, PinOrder: e.PinOrder, SourceApp: e.SourceApp, SourceName: e.SourceName})
		}
		if x.err != nil {
			return x.err
		}
		if len(page) < exportPageSize {
			break
		}
		last := page[len(page)-1]
		usedBefore, idBefore = ms(last.UsedAt), last.ID
	}

	x.raw(`],"snippets":[`)
	snips, err := s.Snippets(ctx, nil)
	if err != nil {
		return err
	}
	for i, sn := range snips {
		if i > 0 {
			x.raw(",")
		}
		x.value(ExportSnippet{Name: sn.Name, Body: sn.Body, CreatedAt: ms(sn.CreatedAt), UpdatedAt: ms(sn.UpdatedAt),
			Folder: sn.Folder, Shortcut: sn.Shortcut, UseCount: sn.UseCount})
	}
	x.raw("]}")
	return x.err
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
		if e.Kind == KindImage || e.Kind == KindFiles || len(e.HTML) > MaxHTMLBytes {
			e.HTML = ""
		}
		var thumb []byte
		if e.Kind == KindImage {
			if len(e.Image) > maxImportImage {
				continue
			}
			cfg, derr := png.DecodeConfig(bytes.NewReader(e.Image))
			if derr != nil || !ImageDimsOK(cfg.Width, cfg.Height) {
				continue
			}
			e.Width, e.Height = cfg.Width, cfg.Height
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
			ok, herr := s.HasHash(ctx, hash)
			if herr != nil {
				return entries, snippets, dupes, herr
			}
			existing = ok
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
			title = cutRunes(strings.TrimSpace(e.Title), MaxTitleRunes)
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

	// Shortcuts must stay unique: an imported one that collides with an existing
	// or earlier imported shortcut is dropped, keeping the snippet itself.
	taken := map[string]bool{}
	rows, err := s.db.Query(ctx, `SELECT shortcut FROM snippets WHERE shortcut!=''`)
	if err != nil {
		return entries, snippets, dupes, err
	}
	for _, r := range rows.Values {
		taken[accel.Key(asString(r[0]))] = true
	}
	now = ms(time.Now())
	clamp := func(t int64) int64 {
		if t <= 0 || t > now {
			return now
		}
		return t
	}
	seenSn := map[string]bool{}
	var snipStmts []sqlite.Statement
	for _, sn := range f.Snippets {
		sn.Name = strings.TrimSpace(sn.Name)
		if sn.Name == "" || strings.TrimSpace(sn.Body) == "" || len(sn.Body) > MaxTextBytes {
			continue
		}
		key := sn.Name + "\x00" + sn.Body
		if seenSn[key] {
			dupes++
			continue
		}
		hit, err := s.db.Query(ctx, `SELECT 1 FROM snippets WHERE name=? AND body=?`, sn.Name, sn.Body)
		if err != nil {
			return entries, snippets, dupes, err
		}
		if len(hit.Values) > 0 {
			dupes++
			continue
		}
		seenSn[key] = true
		created, updated := clamp(sn.CreatedAt), clamp(sn.UpdatedAt)
		if created > updated {
			created = updated
		}
		shortcut := strings.TrimSpace(sn.Shortcut)
		if k := accel.Key(shortcut); k == "" || taken[k] {
			shortcut = ""
		} else {
			taken[k] = true
		}
		snipStmts = append(snipStmts, sqlite.Statement{SQL: `INSERT INTO snippets (name,body,folder,shortcut,use_count,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, Args: []any{sn.Name, sn.Body, sn.Folder, shortcut, max(sn.UseCount, 0), created, updated}})
		snippets++
	}
	if len(snipStmts) > 0 {
		if _, err := s.db.Transaction(ctx, snipStmts); err != nil {
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
