package history

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/sqlite"

	"okibaclip/internal/fsutil"
	"okibaclip/internal/search"
)

// Store is the SQLite history and snippet database.
type Store struct{ db *sqlite.DB }

// Filter selects history rows.
type Filter struct {
	Tab   string // "all", "pinned", "text", "link", "image", "files"
	Query search.Query
	Limit int
}

// migrations[i] upgrades the schema from version i to i+1. Each runs in one
// transaction together with the version bump.
var migrations = [][]string{
	{ // v1
		`CREATE TABLE IF NOT EXISTS entries (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, text TEXT NOT NULL DEFAULT '', search TEXT NOT NULL DEFAULT '', html TEXT, image BLOB, thumb BLOB, width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, size INTEGER NOT NULL, hash TEXT NOT NULL UNIQUE, pinned INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, used_at INTEGER NOT NULL, use_count INTEGER NOT NULL DEFAULT 1)`,
		`CREATE INDEX IF NOT EXISTS entries_order ON entries(pinned DESC, used_at DESC)`,
		`CREATE TABLE IF NOT EXISTS snippets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, body TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
	},
	{ // v2
		`ALTER TABLE entries ADD COLUMN source_app TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE entries ADD COLUMN source_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE entries ADD COLUMN title TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE entries ADD COLUMN pin_order INTEGER NOT NULL DEFAULT 0`,
		`UPDATE entries SET pin_order = -used_at WHERE pinned = 1`,
		`CREATE TABLE IF NOT EXISTS app_icons (app TEXT PRIMARY KEY, path TEXT NOT NULL, icon BLOB, fetched INTEGER NOT NULL DEFAULT 0)`,
		`ALTER TABLE snippets ADD COLUMN folder TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE snippets ADD COLUMN shortcut TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE snippets ADD COLUMN use_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE snippets ADD COLUMN used_at INTEGER NOT NULL DEFAULT 0`,
	},
	{ // v3: AUTOINCREMENT so a deleted id is never reused (undo restores by id)
		`CREATE TABLE entries_new (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, text TEXT NOT NULL DEFAULT '', search TEXT NOT NULL DEFAULT '', html TEXT, image BLOB, thumb BLOB, width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, size INTEGER NOT NULL, hash TEXT NOT NULL UNIQUE, pinned INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, used_at INTEGER NOT NULL, use_count INTEGER NOT NULL DEFAULT 1, source_app TEXT NOT NULL DEFAULT '', source_name TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', pin_order INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO entries_new (id,kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count,source_app,source_name,title,pin_order) SELECT id,kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count,source_app,source_name,title,pin_order FROM entries`,
		`DROP TABLE entries`,
		`ALTER TABLE entries_new RENAME TO entries`,
		`CREATE INDEX IF NOT EXISTS entries_order ON entries(pinned DESC, used_at DESC)`,
	},
}

func migrate(ctx context.Context, db *sqlite.DB) error {
	rows, err := db.Query(ctx, "PRAGMA user_version")
	if err != nil {
		return err
	}
	cur := 0
	if len(rows.Values) > 0 {
		cur = int(asInt(rows.Values[0][0]))
	}
	if cur > len(migrations) {
		return fmt.Errorf("history.db has schema v%d, newer than this Okiba Clip supports (v%d)", cur, len(migrations))
	}
	for v := cur; v < len(migrations); v++ {
		var stmts []sqlite.Statement
		for _, q := range migrations[v] {
			stmts = append(stmts, sqlite.Statement{SQL: q})
		}
		stmts = append(stmts, sqlite.Statement{SQL: fmt.Sprintf("PRAGMA user_version = %d", v+1)})
		if _, err := db.Transaction(ctx, stmts); err != nil {
			return fmt.Errorf("migrate to v%d: %w", v+1, err)
		}
	}
	return nil
}

// Open opens (and migrates) the history database at path.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{BusyTimeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Store, error) {
		db.Close()
		return nil, err
	}
	if err := fsutil.Restrict(path); err != nil {
		return fail(err)
	}
	if _, err := db.Execute(ctx, "PRAGMA secure_delete = ON"); err != nil {
		return fail(err)
	}
	// On a fresh file this takes effect; older files are converted by ConvertAutoVacuum.
	if _, err := db.Execute(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return fail(err)
	}
	if err := migrate(ctx, db); err != nil {
		return fail(err)
	}
	return &Store{db: db}, nil
}

// ConvertAutoVacuum switches a database created before incremental
// auto-vacuum to it, which rewrites the whole file with VACUUM. Open leaves
// that to the caller so a large history does not delay startup; it is a no-op
// once converted. Other store calls wait on the connection while it runs.
func (s *Store) ConvertAutoVacuum(ctx context.Context) error {
	rows, err := s.db.Query(ctx, "PRAGMA auto_vacuum")
	if err != nil {
		return err
	}
	if len(rows.Values) > 0 && asInt(rows.Values[0][0]) == 2 {
		return nil
	}
	_, err = s.db.Execute(ctx, "VACUUM")
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func asInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return 0
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}

func asBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	}
	return nil
}

func ms(t time.Time) int64     { return t.UnixMilli() }
func fromMs(v int64) time.Time { return time.UnixMilli(v) }

// searchText is the lower-cased text the search column holds, cut to 64 KiB
// on a rune boundary.
func searchText(text string) string {
	l := strings.ToLower(text)
	if len(l) > 64<<10 {
		l = l[:64<<10]
		for !utf8.ValidString(l) && len(l) > 0 {
			l = l[:len(l)-1]
		}
	}
	return l
}

// searchFor is the search column of an entry: its title (when named), then
// its text.
func searchFor(title, text string) string {
	if title == "" {
		return searchText(text)
	}
	return strings.ToLower(title) + "\n" + searchText(text)
}

const listCols = `id,kind,substr(text,1,4000),thumb,width,height,size,hash,pinned,created_at,used_at,use_count,title,pin_order,source_app,source_name`

// listColsNoThumb is listCols with the thumbnail left out (NULL), for ranked queries that load thumbnails afterwards.
const listColsNoThumb = `id,kind,substr(text,1,4000),NULL,width,height,size,hash,pinned,created_at,used_at,use_count,title,pin_order,source_app,source_name`
const fullCols = `id,kind,text,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count,title,pin_order,source_app,source_name`

func scanList(row []any) Entry {
	return Entry{
		ID: asInt(row[0]), Kind: Kind(asString(row[1])), Text: asString(row[2]), Thumb: asBytes(row[3]),
		Width: int(asInt(row[4])), Height: int(asInt(row[5])), Size: asInt(row[6]), Hash: asString(row[7]),
		Pinned: asInt(row[8]) != 0, CreatedAt: fromMs(asInt(row[9])), UsedAt: fromMs(asInt(row[10])), UseCount: int(asInt(row[11])),
		Title: asString(row[12]), PinOrder: asInt(row[13]), SourceApp: asString(row[14]), SourceName: asString(row[15]),
	}
}

func scanFull(row []any) Entry {
	return Entry{
		ID: asInt(row[0]), Kind: Kind(asString(row[1])), Text: asString(row[2]), HTML: asString(row[3]),
		Image: asBytes(row[4]), Thumb: asBytes(row[5]),
		Width: int(asInt(row[6])), Height: int(asInt(row[7])), Size: asInt(row[8]), Hash: asString(row[9]),
		Pinned: asInt(row[10]) != 0, CreatedAt: fromMs(asInt(row[11])), UsedAt: fromMs(asInt(row[12])), UseCount: int(asInt(row[13])),
		Title: asString(row[14]), PinOrder: asInt(row[15]), SourceApp: asString(row[16]), SourceName: asString(row[17]),
	}
}

func nullable(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// Capture records c, or bumps the existing row with the same content.
func (s *Store) Capture(ctx context.Context, c Capture, thumb []byte, now time.Time) (id int64, created bool, err error) {
	var html any
	if c.HTML != "" {
		html = c.HTML
	}
	rows, err := s.db.Query(ctx, `INSERT INTO entries (kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count,source_app,source_name)
VALUES (?,?,?,?,?,?,?,?,?,?,0,?,?,1,?,?)
ON CONFLICT(hash) DO UPDATE SET used_at=excluded.used_at, use_count=use_count+1, html=coalesce(excluded.html, html),
  source_app=CASE WHEN excluded.source_app!='' THEN excluded.source_app ELSE source_app END,
  source_name=CASE WHEN excluded.source_name!='' THEN excluded.source_name ELSE source_name END
RETURNING id, (use_count = 1)`,
		string(c.Kind), c.Text, searchText(c.Text), html, nullable(c.Image), nullable(thumb), c.Width, c.Height, c.Size(), c.Hash(), ms(now), ms(now), c.SourceApp, c.SourceName)
	if err != nil {
		return 0, false, err
	}
	if len(rows.Values) != 1 {
		return 0, false, errors.New("capture: no row returned")
	}
	if c.SourceApp != "" && c.SourcePath != "" {
		// The entry is already committed: failing here would make the monitor
		// retry and bump use_count again. The icon path is retried on the next
		// capture from the same app.
		if _, err := s.db.Execute(ctx, `INSERT INTO app_icons(app,path) VALUES(?,?) ON CONFLICT(app) DO UPDATE SET path=excluded.path, fetched=0, icon=NULL WHERE path!=excluded.path`, c.SourceApp, c.SourcePath); err != nil {
			slog.Warn("app icon path", "app", c.SourceApp, "err", err)
		}
	}
	return asInt(rows.Values[0][0]), asInt(rows.Values[0][1]) != 0, nil
}

func tabClause(tab string) string {
	switch tab {
	case "pinned":
		return "pinned=1"
	case "text":
		return "kind IN ('text','color')"
	case "link":
		return "kind='link'"
	case "image":
		return "kind='image'"
	case "files":
		return "kind='files'"
	}
	return "1"
}

// List returns the entries matching f. Without search words or app filters
// they come pinned first (in pin order), then by last use. With either, rows
// are ranked in Go: best match first, in that order among equal matches. Ranking
// reads the full text and title, so the limit applies after it.
func (s *Store) List(ctx context.Context, f Filter) ([]Entry, error) {
	if f.Limit <= 0 {
		f.Limit = 1000
	}
	q := f.Query
	where := []string{tabClause(f.Tab)}
	var args []any
	if len(q.Kinds) > 0 {
		where = append(where, "kind IN (?"+strings.Repeat(",?", len(q.Kinds)-1)+")")
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	if q.Pinned != nil {
		where = append(where, "pinned=?")
		args = append(args, *q.Pinned)
	}
	if !q.After.IsZero() {
		where = append(where, "used_at>=?")
		args = append(args, ms(q.After))
	}
	if !q.Before.IsZero() {
		where = append(where, "used_at<?")
		args = append(args, ms(q.Before))
	}
	needGo := len(q.Terms) > 0 || len(q.Apps) > 0
	// Rows hold only the start of their text, so each word also reports
	// whether the whole text has it; fuzzy candidates are narrowed with
	// LIKE '%w%o%r%d%' and confirmed by search.Score. The fuzzy window starts
	// after the title, which searchFor puts first. Ranked queries skip the
	// thumbnails: attachThumbs loads them for the results only.
	cols := listCols
	if needGo {
		cols = listColsNoThumb
	}
	var colArgs []any
	for _, t := range q.Terms {
		cols += `, instr(search, ?) > 0`
		colArgs = append(colArgs, t.Text)
		if t.Fuzzy() {
			where = append(where, `(instr(search, ?) > 0 OR substr(search, 1, ? + CASE WHEN title='' THEN 0 ELSE length(title)+1 END) LIKE ? ESCAPE '\')`)
			args = append(args, t.Text, search.FuzzyWindow, likeSubsequence(t.Text))
		} else {
			where = append(where, `instr(search, ?) > 0`)
			args = append(args, t.Text)
		}
	}
	limit := ""
	if !needGo {
		limit = " LIMIT ?"
		args = append(args, f.Limit)
	}
	args = append(colArgs, args...)
	rows, err := s.db.Query(ctx, `SELECT `+cols+` FROM entries WHERE `+strings.Join(where, " AND ")+` ORDER BY `+orderBy+limit, args...)
	if err != nil {
		return nil, err
	}
	if !needGo {
		return scanRows(rows, scanList), nil
	}
	type ranked struct {
		e     Entry
		score int
	}
	var out []ranked
	for _, r := range rows.Values {
		e, inFull := scanList(r), r[len(r)-len(q.Terms):]
		if len(q.Apps) > 0 && !matchesApps(e, q.Apps) {
			continue
		}
		total, ok := 0, true
		for i, t := range q.Terms {
			sc := search.Score(e.Text, t)
			if e.Title != "" {
				sc = max(sc, search.Score(e.Title, t))
			}
			if sc == 0 && asInt(inFull[i]) != 0 {
				sc = search.ScoreSubstring // past the start of the text the row holds
			}
			if sc == 0 {
				ok = false
				break
			}
			total += sc
		}
		if ok {
			out = append(out, ranked{e, total})
		}
	}
	slices.SortStableFunc(out, func(a, b ranked) int { return b.score - a.score })
	entries := make([]Entry, min(len(out), f.Limit))
	for i, r := range out[:len(entries)] {
		entries[i] = r.e
	}
	if err := s.attachThumbs(ctx, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// matchesApps reports whether the entry's source app or name contains one of
// apps, ignoring case. It runs in Go because SQL lower() is ASCII-only.
func matchesApps(e Entry, apps []string) bool {
	app, name := strings.ToLower(e.SourceApp), strings.ToLower(e.SourceName)
	for _, a := range apps {
		a = strings.ToLower(a)
		if strings.Contains(app, a) || strings.Contains(name, a) {
			return true
		}
	}
	return false
}

// attachThumbs fills Thumb of es from the thumb column; ranked queries leave
// thumbnails out so that rows which don't match never load their images.
func (s *Store) attachThumbs(ctx context.Context, es []Entry) error {
	if len(es) == 0 {
		return nil
	}
	ids := make([]int64, len(es))
	for i, e := range es {
		ids[i] = e.ID
	}
	in, args := idList(ids)
	rows, err := s.db.Query(ctx, `SELECT id, thumb FROM entries WHERE id IN (`+in+`) AND thumb IS NOT NULL`, args...)
	if err != nil {
		return err
	}
	thumbs := make(map[int64][]byte, len(rows.Values))
	for _, r := range rows.Values {
		thumbs[asInt(r[0])] = asBytes(r[1])
	}
	for i := range es {
		es[i].Thumb = thumbs[es[i].ID]
	}
	return nil
}

// likeSubsequence is the LIKE pattern of s's characters in order, with
// anything between them.
func likeSubsequence(s string) string {
	var b strings.Builder
	b.WriteByte('%')
	for _, r := range s {
		if r == '%' || r == '_' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		b.WriteByte('%')
	}
	return b.String()
}

func scanRows(rows sqlite.Rows, scan func([]any) Entry) []Entry {
	out := make([]Entry, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, scan(r))
	}
	return out
}

var ErrNotFound = errors.New("entry not found")

// Get returns the full row of an entry, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (Entry, error) {
	rows, err := s.db.Query(ctx, `SELECT `+fullCols+` FROM entries WHERE id=?`, id)
	if err != nil {
		return Entry{}, err
	}
	if len(rows.Values) == 0 {
		return Entry{}, ErrNotFound
	}
	return scanFull(rows.Values[0]), nil
}

// Touch marks an entry as used now.
func (s *Store) Touch(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.Execute(ctx, `UPDATE entries SET used_at=?, use_count=use_count+1 WHERE id=?`, ms(now), id)
	return err
}

// SetPinned pins or unpins an entry. New pins go to the top of the pinned
// order; unpinning forgets the pin's name.
func (s *Store) SetPinned(ctx context.Context, id int64, pinned bool) error {
	if pinned {
		_, err := s.db.Execute(ctx, `UPDATE entries SET pinned=1, pin_order=(SELECT coalesce(min(pin_order),0)-1 FROM entries WHERE pinned=1) WHERE id=? AND pinned=0`, id)
		return err
	}
	e, err := s.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.db.Execute(ctx, `UPDATE entries SET pinned=0, pin_order=0, title='', search=? WHERE id=?`, searchFor("", e.Text), id)
	return err
}

// SetTitle names an entry; an empty title clears the name. The title is
// searchable.
func (s *Store) SetTitle(ctx context.Context, id int64, title string) error {
	e, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	title = cutRunes(strings.TrimSpace(title), MaxTitleRunes)
	res, err := s.db.Execute(ctx, `UPDATE entries SET title=?, search=? WHERE id=? AND hash=?`, title, searchFor(title, e.Text), id, e.Hash)
	if err != nil {
		return err
	}
	if res.Changes == 0 {
		return ErrConflict
	}
	return nil
}

// ReorderPinned sets the pinned order to ids, first to last.
func (s *Store) ReorderPinned(ctx context.Context, ids []int64) error {
	stmts := make([]sqlite.Statement, len(ids))
	for i, id := range ids {
		stmts[i] = sqlite.Statement{SQL: `UPDATE entries SET pin_order=? WHERE id=? AND pinned=1`, Args: []any{i, id}}
	}
	_, err := s.db.Transaction(ctx, stmts)
	return err
}

// UpdateText replaces the text of a text entry and returns the id that now
// holds it. When the edited text equals another entry's, the two merge into
// that one (pinned if either was) and its id is returned.
func (s *Store) UpdateText(ctx context.Context, id int64, text string, now time.Time) (int64, error) {
	if len(text) > MaxTextBytes {
		return 0, ErrTooLarge
	}
	e, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if e.Kind == KindImage || e.Kind == KindFiles {
		return 0, errors.New("only text entries can be edited")
	}
	k := TextKind(text)
	c := Capture{Kind: k, Text: text}
	h := c.Hash()
	if h == e.Hash {
		return id, nil
	}
	rows, err := s.db.Query(ctx, `SELECT id FROM entries WHERE hash=? AND id!=?`, h, id)
	if err != nil {
		return 0, err
	}
	if len(rows.Values) > 0 {
		oid := asInt(rows.Values[0][0])
		o, err := s.Get(ctx, oid)
		if err != nil {
			return 0, err
		}
		title := o.Title
		if title == "" {
			title = e.Title
		}
		res, err := s.db.Transaction(ctx, []sqlite.Statement{
			{SQL: `UPDATE entries SET pin_order=CASE WHEN pinned=0 AND ?=1 THEN (SELECT coalesce(min(pin_order),0)-1 FROM entries WHERE pinned=1) ELSE pin_order END, pinned=max(pinned,?), used_at=?, use_count=use_count+1, title=?, search=? WHERE id=? AND hash=? AND EXISTS (SELECT 1 FROM entries WHERE id=? AND hash=?)`,
				Args: []any{e.Pinned, e.Pinned, ms(now), title, searchFor(title, o.Text), oid, h, id, e.Hash}},
			{SQL: `DELETE FROM entries WHERE id=? AND hash=? AND EXISTS (SELECT 1 FROM entries WHERE id=? AND hash=?)`, Args: []any{id, e.Hash, oid, h}},
		})
		if err != nil {
			return 0, err
		}
		// Both statements share their guards, so neither applied when either matched no row.
		if res[0].Changes == 0 || res[1].Changes == 0 {
			return 0, ErrConflict
		}
		return oid, nil
	}
	res, err := s.db.Execute(ctx, `UPDATE entries SET kind=?, text=?, html=NULL, size=?, hash=?, search=?, used_at=? WHERE id=? AND hash=?`,
		string(k), text, c.Size(), h, searchFor(e.Title, text), ms(now), id, e.Hash)
	if isUniqueViolation(err) || (err == nil && res.Changes == 0) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Delete removes entries and returns their full rows for undo. Undo is nil
// when the removed rows hold more than MaxUndoBytes, so the caller can say the
// deletion cannot be undone; deleted still counts the rows.
func (s *Store) Delete(ctx context.Context, ids ...int64) (deleted int64, undo []Entry, err error) {
	if len(ids) == 0 {
		return 0, nil, nil
	}
	in, args := idList(ids)
	rows, err := s.db.Query(ctx, `SELECT coalesce(SUM(`+rowBytes+`),0) FROM entries WHERE id IN (`+in+`)`, args...)
	if err != nil {
		return 0, nil, err
	}
	if len(rows.Values) > 0 && asInt(rows.Values[0][0]) > MaxUndoBytes {
		r, err := s.db.Execute(ctx, `DELETE FROM entries WHERE id IN (`+in+`)`, args...)
		if r.Changes > 0 {
			s.compact(ctx)
		}
		return r.Changes, nil, err
	}
	rows, err = s.db.Query(ctx, `DELETE FROM entries WHERE id IN (`+in+`) RETURNING `+fullCols, args...)
	if err != nil {
		return 0, nil, err
	}
	undo = scanRows(rows, scanFull)
	if len(undo) > 0 {
		s.compact(ctx)
	}
	return int64(len(undo)), undo, nil
}

// Restore reinserts deleted entries. An entry whose content was captured again
// since the delete merges into that row: the pin and title survive, the id doesn't change.
func (s *Store) Restore(ctx context.Context, entries []Entry) error {
	var stmts []sqlite.Statement
	for _, e := range entries {
		var html any
		if e.HTML != "" {
			html = e.HTML
		}
		stmts = append(stmts, sqlite.Statement{
			SQL: `INSERT INTO entries (id,kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count,title,pin_order,source_app,source_name) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(hash) DO UPDATE SET
  pinned=max(pinned,excluded.pinned),
  title=CASE WHEN title='' THEN excluded.title ELSE title END,
  search=CASE WHEN title='' AND excluded.title!='' THEN excluded.search ELSE search END,
  pin_order=CASE WHEN pinned=0 AND excluded.pinned=1 THEN excluded.pin_order ELSE pin_order END`,
			Args: []any{e.ID, string(e.Kind), e.Text, searchFor(e.Title, e.Text), html, nullable(e.Image), nullable(e.Thumb), e.Width, e.Height, e.Size, e.Hash, e.Pinned, ms(e.CreatedAt), ms(e.UsedAt), e.UseCount, e.Title, e.PinOrder, e.SourceApp, e.SourceName},
		})
	}
	_, err := s.db.Transaction(ctx, stmts)
	return err
}

// ErrConflict is returned when an entry changed under an edit or delete.
var ErrConflict = errors.New("entry changed while editing; try again")

// ErrTooLarge is returned when text exceeds MaxTextBytes.
var ErrTooLarge = errors.New("text is over 1 MiB")

// HasHash reports whether an entry with this content hash exists.
func (s *Store) HasHash(ctx context.Context, hash string) (bool, error) {
	rows, err := s.db.Query(ctx, `SELECT 1 FROM entries WHERE hash=? LIMIT 1`, hash)
	if err != nil {
		return false, err
	}
	return len(rows.Values) > 0, nil
}

// HasHashPrefix reports whether an entry's hash starts with prefix (lower-case hex).
func (s *Store) HasHashPrefix(ctx context.Context, prefix string) (bool, error) {
	rows, err := s.db.Query(ctx, `SELECT 1 FROM entries WHERE hash >= ? AND hash < ? LIMIT 1`, prefix, prefix+"g")
	if err != nil {
		return false, err
	}
	return len(rows.Values) > 0, nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.ExtendedCode == 2067
}

// IsCorrupt reports whether err means the database file is damaged or not a database.
func IsCorrupt(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && (se.Code == 11 || se.Code == 26) // SQLITE_CORRUPT, SQLITE_NOTADB
}

// idList returns "?,?,…" for ids and the matching args.
func idList(ids []int64) (string, []any) {
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args[i] = id
	}
	return strings.Join(marks, ","), args
}

// Clear deletes all entries, keeping pinned ones when keepPinned is set.
func (s *Store) Clear(ctx context.Context, keepPinned bool) (int64, error) {
	q := `DELETE FROM entries`
	if keepPinned {
		q += ` WHERE pinned=0`
	}
	r, err := s.db.Execute(ctx, q)
	if r.Changes > 0 {
		s.compact(ctx)
	}
	return r.Changes, err
}

// Limits are the retention rules Prune applies to unpinned entries.
type Limits struct {
	MaxEntries int
	Retention  time.Duration
	MaxBytes   int64 // 0 = unlimited
}

// rowBytes is the stored size of an entry row's content.
const rowBytes = `size + coalesce(length(CAST(html AS BLOB)),0) + coalesce(length(thumb),0) + coalesce(length(CAST(search AS BLOB)),0)`

// Prune applies the retention rules to unpinned entries. Pinned entries
// count toward MaxBytes but are never removed, and the newest unpinned entry
// always stays.
func (s *Store) Prune(ctx context.Context, l Limits, now time.Time) (int64, error) {
	total, err := s.prune(ctx, l, now)
	if total > 0 {
		s.compact(ctx)
	}
	return total, err
}

func (s *Store) prune(ctx context.Context, l Limits, now time.Time) (int64, error) {
	var total int64
	newest := `id != (SELECT id FROM entries WHERE pinned=0 ORDER BY used_at DESC, id DESC LIMIT 1)`
	if l.Retention > 0 {
		r, err := s.db.Execute(ctx, `DELETE FROM entries WHERE pinned=0 AND used_at < ? AND `+newest, ms(now.Add(-l.Retention)))
		if err != nil {
			return total, err
		}
		total += r.Changes
	}
	r, err := s.db.Execute(ctx, `DELETE FROM entries WHERE pinned=0 AND id NOT IN (SELECT id FROM entries WHERE pinned=0 ORDER BY used_at DESC, id DESC LIMIT max(?,1))`, l.MaxEntries)
	if err != nil {
		return total, err
	}
	total += r.Changes
	if l.MaxBytes <= 0 {
		return total, nil
	}
	r, err = s.db.Execute(ctx, `DELETE FROM entries WHERE pinned=0
  AND `+newest+`
  AND id IN (SELECT id FROM (SELECT id, SUM(`+rowBytes+`)
        OVER (ORDER BY pinned DESC, used_at DESC, id DESC ROWS UNBOUNDED PRECEDING) AS run FROM entries) WHERE run > ?)`, l.MaxBytes)
	if err != nil {
		return total, err
	}
	total += r.Changes
	return total, nil
}

// compact returns space freed by deletions to the file system. Failures are
// logged: the deletion that triggered it already succeeded.
func (s *Store) compact(ctx context.Context) {
	if _, err := s.db.Query(ctx, "PRAGMA incremental_vacuum"); err != nil {
		slog.Warn("compact", "err", err)
	}
}

// Usage is the stored size of the history's content.
type Usage struct{ Bytes, ImageBytes int64 }

// Usage sums the content, HTML and thumbnail bytes of every entry.
func (s *Store) Usage(ctx context.Context) (Usage, error) {
	rows, err := s.db.Query(ctx, `SELECT coalesce(SUM(`+rowBytes+`),0), coalesce(SUM(CASE WHEN kind='image' THEN `+rowBytes+` ELSE 0 END),0) FROM entries`)
	if err != nil || len(rows.Values) == 0 {
		return Usage{}, err
	}
	return Usage{Bytes: asInt(rows.Values[0][0]), ImageBytes: asInt(rows.Values[0][1])}, nil
}

// Recent returns the n most recently used entries.
func (s *Store) Recent(ctx context.Context, n int) ([]Entry, error) {
	rows, err := s.db.Query(ctx, `SELECT `+listCols+` FROM entries ORDER BY used_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanList), nil
}

// Pinned returns up to n pinned entries in pin order.
func (s *Store) Pinned(ctx context.Context, n int) ([]Entry, error) {
	rows, err := s.db.Query(ctx, `SELECT `+listCols+` FROM entries WHERE pinned=1 ORDER BY `+orderBy+` LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanList), nil
}

// orderBy lists pinned entries first in their own order, then by last use.
const orderBy = `pinned DESC, CASE WHEN pinned=1 THEN pin_order ELSE 0 END, used_at DESC`

// Counts returns the number of entries, pinned entries and snippets.
func (s *Store) Counts(ctx context.Context) (entries, pinned, snippets int, err error) {
	rows, err := s.db.Query(ctx, `SELECT (SELECT count(*) FROM entries), (SELECT count(*) FROM entries WHERE pinned=1), (SELECT count(*) FROM snippets)`)
	if err != nil {
		return
	}
	r := rows.Values[0]
	return int(asInt(r[0])), int(asInt(r[1])), int(asInt(r[2])), nil
}
