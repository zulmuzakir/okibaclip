package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// maxImportImage is the largest image Import accepts; it matches the MaxImageMB ceiling.
	maxImportImage = 50 << 20
	// exportPageSize bounds how many entries WriteExport reads per page.
	exportPageSize = 200
	// exportPageBytes bounds the content bytes WriteExport holds per page.
	exportPageBytes = 32 << 20
	// importBatchBytes bounds the content bytes Import holds per transaction.
	importBatchBytes = 32 << 20
	// maxImportValue is the most bytes one JSON value may span. A 50 MiB image
	// is about 67 MiB in base64; text and HTML escaping add less than 12 MiB.
	maxImportValue = 128 << 20
)

var errImportValueTooLarge = fmt.Errorf("export file has a value over %d MiB", maxImportValue>>20)

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

// exportWriter writes JSON pieces and keeps the first error, so callers check once.
type exportWriter struct {
	w   io.Writer
	err error
}

func (x *exportWriter) raw(s string) {
	x.write([]byte(s))
}

func (x *exportWriter) write(b []byte) {
	if x.err != nil {
		return
	}
	_, x.err = x.w.Write(b)
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
// are read in pages keyed on (used_at, id), so memory stays bounded by one
// page of at most exportPageBytes of content.
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
		light, err := s.db.Query(ctx, `SELECT id, used_at, `+rowBytes+` FROM entries WHERE (used_at,id) < (?,?) ORDER BY used_at DESC, id DESC LIMIT ?`, usedBefore, idBefore, exportPageSize)
		if err != nil {
			return err
		}
		if len(light.Values) == 0 {
			break
		}
		var ids []int64
		var total int64
		for _, r := range light.Values {
			total += asInt(r[2])
			if len(ids) > 0 && total > exportPageBytes {
				break
			}
			ids = append(ids, asInt(r[0]))
		}
		last := light.Values[len(ids)-1]
		in, args := idList(ids)
		rows, err := s.db.Query(ctx, `SELECT `+fullCols+` FROM entries WHERE id IN (`+in+`) ORDER BY used_at DESC, id DESC`, args...)
		if err != nil {
			return err
		}
		for _, e := range scanRows(rows, scanFull) {
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
		usedBefore, idBefore = asInt(last[1]), asInt(last[0])
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

// budgetReader fails a read once more than maxImportValue bytes have been
// consumed since the last reset, so one oversized JSON value cannot exhaust memory.
type budgetReader struct {
	r    io.Reader
	left int
}

func (b *budgetReader) reset() { b.left = maxImportValue }

func (b *budgetReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errImportValueTooLarge
	}
	if len(p) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= n
	return n, err
}

// Import merges an export file read from r: entries upsert by recomputed hash,
// snippets dedupe on (name, body). dupes counts merged entries and skipped
// snippets. The file is decoded one entry at a time and committed in batches
// bounded by count and bytes, so memory does not grow with the file.
func (s *Store) Import(ctx context.Context, r io.Reader) (entries, snippets, dupes int, err error) {
	br := &budgetReader{r: r}
	dec := json.NewDecoder(br)
	im := &importer{s: s, now: ms(time.Now()), seen: map[string]bool{}, seenSn: map[string]bool{}}
	err = im.run(ctx, dec, br)
	return im.entries, im.snippets, im.dupes, err
}

type importer struct {
	s                        *Store
	now                      int64
	seen                     map[string]bool
	stmts                    []sqlite.Statement
	batchBytes               int
	entries, snippets, dupes int

	taken     map[string]bool // shortcut keys in use; loaded on the first snippet
	seenSn    map[string]bool
	snipStmts []sqlite.Statement
}

func (im *importer) run(ctx context.Context, dec *json.Decoder, br *budgetReader) error {
	delim := func(want json.Delim) error {
		br.reset()
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); !ok || d != want {
			return fmt.Errorf("invalid export file: expected %q", string(want))
		}
		return nil
	}
	var format string
	var version int
	checked := false
	check := func() error {
		checked = true
		if format != ExportFormat {
			return fmt.Errorf("not an Okiba Clip export (format %q)", format)
		}
		if version != 1 && version != ExportVersion {
			return fmt.Errorf("unsupported export version %d", version)
		}
		return nil
	}
	// array decodes the elements of the array that follows a key, handing each to fn.
	array := func(each func() error) error {
		if err := check(); err != nil {
			return err
		}
		br.reset()
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		if d, ok := t.(json.Delim); !ok || d != '[' {
			return errors.New("invalid export file: expected a list")
		}
		for {
			br.reset()
			if !dec.More() {
				break
			}
			if err := each(); err != nil {
				return err
			}
		}
		return delim(']')
	}

	if err := delim('{'); err != nil {
		return err
	}
	for {
		br.reset()
		if !dec.More() {
			break
		}
		br.reset()
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := t.(string)
		br.reset()
		switch key {
		case "format":
			err = dec.Decode(&format)
		case "version":
			err = dec.Decode(&version)
		case "entries":
			err = array(func() error {
				var e ExportEntry
				br.reset()
				if err := dec.Decode(&e); err != nil {
					return err
				}
				return im.entry(ctx, e)
			})
		case "snippets":
			err = array(func() error {
				var sn ExportSnippet
				br.reset()
				if err := dec.Decode(&sn); err != nil {
					return err
				}
				return im.snippet(ctx, sn)
			})
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return err
		}
	}
	if err := delim('}'); err != nil {
		return err
	}
	if !checked {
		if err := check(); err != nil {
			return err
		}
	}
	return im.finish(ctx)
}

func (im *importer) flushEntries(ctx context.Context) error {
	if len(im.stmts) == 0 {
		return nil
	}
	_, err := im.s.db.Transaction(ctx, im.stmts)
	clear(im.stmts)
	im.stmts, im.batchBytes = im.stmts[:0], 0
	return err
}

func (im *importer) flushSnippets(ctx context.Context) error {
	if len(im.snipStmts) == 0 {
		return nil
	}
	_, err := im.s.db.Transaction(ctx, im.snipStmts)
	clear(im.snipStmts)
	im.snipStmts = im.snipStmts[:0]
	return err
}

func (im *importer) finish(ctx context.Context) error {
	if err := im.flushEntries(ctx); err != nil {
		return err
	}
	return im.flushSnippets(ctx)
}

// entry validates and queues one exported entry; invalid ones are skipped.
func (im *importer) entry(ctx context.Context, e ExportEntry) error {
	switch e.Kind {
	case KindText, KindLink, KindColor, KindFiles, KindImage:
	default:
		return nil
	}
	if (e.Kind == KindImage) != (len(e.Image) > 0) {
		return nil
	}
	e.SourceApp = cutRunes(e.SourceApp, MaxSourceRunes)
	e.SourceName = cutRunes(e.SourceName, MaxSourceRunes)
	switch e.Kind {
	case KindText, KindLink, KindColor:
		e.Kind = TextKind(e.Text)
	case KindFiles:
		if strings.TrimSpace(e.Text) == "" {
			return nil
		}
	}
	if e.Kind == KindImage || e.Kind == KindFiles || len(e.HTML) > MaxHTMLBytes {
		e.HTML = ""
	}
	var thumb []byte
	if e.Kind == KindImage {
		if len(e.Image) > maxImportImage {
			return nil
		}
		cfg, derr := png.DecodeConfig(bytes.NewReader(e.Image))
		if derr != nil || !ImageDimsOK(cfg.Width, cfg.Height) {
			return nil
		}
		e.Width, e.Height = cfg.Width, cfg.Height
		var terr error
		thumb, terr = MakeThumb(e.Image)
		if terr != nil {
			return nil
		}
	} else if len(e.Text) > MaxTextBytes {
		return nil
	}
	c := Capture{Kind: e.Kind, Text: e.Text, HTML: e.HTML, Image: e.Image, Width: e.Width, Height: e.Height}
	hash := c.Hash()
	existing := im.seen[hash]
	if !existing {
		ok, herr := im.s.HasHash(ctx, hash)
		if herr != nil {
			return herr
		}
		existing = ok
	}
	if existing {
		im.dupes++
	} else {
		im.entries++
	}
	im.seen[hash] = true
	var html any
	if e.HTML != "" {
		html = e.HTML
	}
	usedAt := e.UsedAt
	if usedAt <= 0 || usedAt > im.now {
		usedAt = im.now
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
	im.stmts = append(im.stmts, sqlite.Statement{
		SQL: `INSERT INTO entries (kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count,title,pin_order,source_app,source_name) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(hash) DO UPDATE SET pinned=max(pinned,excluded.pinned), used_at=max(used_at,excluded.used_at), use_count=max(use_count,excluded.use_count),
  title=CASE WHEN title='' THEN excluded.title ELSE title END,
  search=CASE WHEN title='' AND excluded.title!='' THEN excluded.search ELSE search END,
  pin_order=CASE WHEN pinned=0 AND excluded.pinned=1 THEN excluded.pin_order ELSE pin_order END,
  source_app=CASE WHEN source_app='' THEN excluded.source_app ELSE source_app END,
  source_name=CASE WHEN source_name='' THEN excluded.source_name ELSE source_name END`,
		Args: []any{string(e.Kind), e.Text, searchFor(title, e.Text), html, nullable(e.Image), nullable(thumb), e.Width, e.Height, c.Size(), hash, e.Pinned, created, usedAt, count, title, e.PinOrder, e.SourceApp, e.SourceName},
	})
	im.batchBytes += len(e.Text) + len(e.HTML) + len(e.Image) + len(thumb)
	if len(im.stmts) >= 200 || im.batchBytes >= importBatchBytes {
		return im.flushEntries(ctx)
	}
	return nil
}

// snippet validates and queues one exported snippet; invalid ones are skipped.
func (im *importer) snippet(ctx context.Context, sn ExportSnippet) error {
	// Shortcuts must stay unique: an imported one that collides with an existing
	// or earlier imported shortcut is dropped, keeping the snippet itself.
	if im.taken == nil {
		im.taken = map[string]bool{}
		rows, err := im.s.db.Query(ctx, `SELECT shortcut FROM snippets WHERE shortcut!=''`)
		if err != nil {
			return err
		}
		for _, r := range rows.Values {
			im.taken[accel.Key(asString(r[0]))] = true
		}
	}
	clamp := func(t int64) int64 {
		if t <= 0 || t > im.now {
			return im.now
		}
		return t
	}
	sn.Name = cutRunes(strings.TrimSpace(sn.Name), MaxSnippetNameRunes)
	sn.Folder = cutRunes(strings.TrimSpace(sn.Folder), MaxSnippetFolderRunes)
	if sn.Name == "" || strings.TrimSpace(sn.Body) == "" || len(sn.Body) > MaxTextBytes {
		return nil
	}
	key := sn.Name + "\x00" + sn.Body
	if im.seenSn[key] {
		im.dupes++
		return nil
	}
	hit, err := im.s.db.Query(ctx, `SELECT 1 FROM snippets WHERE name=? AND body=?`, sn.Name, sn.Body)
	if err != nil {
		return err
	}
	if len(hit.Values) > 0 {
		im.dupes++
		return nil
	}
	im.seenSn[key] = true
	created, updated := clamp(sn.CreatedAt), clamp(sn.UpdatedAt)
	if created > updated {
		created = updated
	}
	shortcut := strings.TrimSpace(sn.Shortcut)
	if k := accel.Key(shortcut); k == "" || im.taken[k] || accel.Valid(shortcut) != nil {
		shortcut = ""
	} else {
		im.taken[k] = true
	}
	im.snipStmts = append(im.snipStmts, sqlite.Statement{SQL: `INSERT INTO snippets (name,body,folder,shortcut,use_count,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, Args: []any{sn.Name, sn.Body, sn.Folder, shortcut, max(sn.UseCount, 0), created, updated}})
	im.snippets++
	if len(im.snipStmts) >= 200 {
		return im.flushSnippets(ctx)
	}
	return nil
}

// cutRunes shortens s to at most n runes.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
