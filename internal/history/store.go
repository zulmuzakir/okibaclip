package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/sqlite"
)

// Store is the SQLite history and snippet database.
type Store struct{ db *sqlite.DB }

// Filter selects history rows.
type Filter struct {
	Tab   string // "all", "pinned", "text", "link", "image", "files"
	Query string
	Limit int
}

var migrationV1 = []string{
	`CREATE TABLE entries (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, text TEXT NOT NULL DEFAULT '', search TEXT NOT NULL DEFAULT '', html TEXT, image BLOB, thumb BLOB, width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, size INTEGER NOT NULL, hash TEXT NOT NULL UNIQUE, pinned INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, used_at INTEGER NOT NULL, use_count INTEGER NOT NULL DEFAULT 1)`,
	`CREATE INDEX entries_order ON entries(pinned DESC, used_at DESC)`,
	`CREATE TABLE snippets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, body TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
	`PRAGMA user_version = 1`,
}

// Open opens (and migrates) the history database at path.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{BusyTimeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Store{db: db}
	rows, err := db.Query(ctx, "PRAGMA user_version")
	if err != nil {
		db.Close()
		return nil, err
	}
	if len(rows.Values) == 0 || asInt(rows.Values[0][0]) < 1 {
		for _, stmt := range migrationV1 {
			if _, err := db.Execute(ctx, stmt); err != nil {
				db.Close()
				return nil, fmt.Errorf("migrate: %w", err)
			}
		}
	}
	return s, nil
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

const listCols = `id,kind,substr(text,1,4000),thumb,width,height,size,hash,pinned,created_at,used_at,use_count`
const fullCols = `id,kind,text,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count`

func scanList(row []any) Entry {
	return Entry{
		ID: asInt(row[0]), Kind: Kind(asString(row[1])), Text: asString(row[2]), Thumb: asBytes(row[3]),
		Width: int(asInt(row[4])), Height: int(asInt(row[5])), Size: asInt(row[6]), Hash: asString(row[7]),
		Pinned: asInt(row[8]) != 0, CreatedAt: fromMs(asInt(row[9])), UsedAt: fromMs(asInt(row[10])), UseCount: int(asInt(row[11])),
	}
}

func scanFull(row []any) Entry {
	return Entry{
		ID: asInt(row[0]), Kind: Kind(asString(row[1])), Text: asString(row[2]), HTML: asString(row[3]),
		Image: asBytes(row[4]), Thumb: asBytes(row[5]),
		Width: int(asInt(row[6])), Height: int(asInt(row[7])), Size: asInt(row[8]), Hash: asString(row[9]),
		Pinned: asInt(row[10]) != 0, CreatedAt: fromMs(asInt(row[11])), UsedAt: fromMs(asInt(row[12])), UseCount: int(asInt(row[13])),
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
	rows, err := s.db.Query(ctx, `INSERT INTO entries (kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count)
VALUES (?,?,?,?,?,?,?,?,?,?,0,?,?,1)
ON CONFLICT(hash) DO UPDATE SET used_at=excluded.used_at, use_count=use_count+1
RETURNING id, (use_count = 1)`,
		string(c.Kind), c.Text, searchText(c.Text), html, nullable(c.Image), nullable(thumb), c.Width, c.Height, c.Size(), c.Hash(), ms(now), ms(now))
	if err != nil {
		return 0, false, err
	}
	if len(rows.Values) != 1 {
		return 0, false, errors.New("capture: no row returned")
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

// List returns the entries matching f, pinned first, then by last use.
func (s *Store) List(ctx context.Context, f Filter) ([]Entry, error) {
	if f.Limit <= 0 {
		f.Limit = 1000
	}
	q := strings.ToLower(f.Query)
	rows, err := s.db.Query(ctx, `SELECT `+listCols+` FROM entries WHERE (?='' OR instr(search, ?) > 0) AND `+tabClause(f.Tab)+` ORDER BY pinned DESC, used_at DESC LIMIT ?`, q, q, f.Limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanList), nil
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

// SetPinned pins or unpins an entry.
func (s *Store) SetPinned(ctx context.Context, id int64, pinned bool) error {
	_, err := s.db.Execute(ctx, `UPDATE entries SET pinned=? WHERE id=?`, pinned, id)
	return err
}

// Delete removes entries and returns their full rows, for undo.
func (s *Store) Delete(ctx context.Context, ids ...int64) ([]Entry, error) {
	var removed []Entry
	var stmts []sqlite.Statement
	for _, id := range ids {
		e, err := s.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		removed = append(removed, e)
		stmts = append(stmts, sqlite.Statement{SQL: `DELETE FROM entries WHERE id=?`, Args: []any{id}})
	}
	if _, err := s.db.Transaction(ctx, stmts); err != nil {
		return nil, err
	}
	return removed, nil
}

// Restore reinserts deleted entries with their original ids.
func (s *Store) Restore(ctx context.Context, entries []Entry) error {
	var stmts []sqlite.Statement
	for _, e := range entries {
		var html any
		if e.HTML != "" {
			html = e.HTML
		}
		stmts = append(stmts, sqlite.Statement{
			SQL:  `INSERT OR IGNORE INTO entries (id,kind,text,search,html,image,thumb,width,height,size,hash,pinned,created_at,used_at,use_count) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Args: []any{e.ID, string(e.Kind), e.Text, searchText(e.Text), html, nullable(e.Image), nullable(e.Thumb), e.Width, e.Height, e.Size, e.Hash, e.Pinned, ms(e.CreatedAt), ms(e.UsedAt), e.UseCount},
		})
	}
	_, err := s.db.Transaction(ctx, stmts)
	return err
}

// Clear deletes all entries, keeping pinned ones when keepPinned is set.
func (s *Store) Clear(ctx context.Context, keepPinned bool) (int64, error) {
	q := `DELETE FROM entries`
	if keepPinned {
		q += ` WHERE pinned=0`
	}
	r, err := s.db.Execute(ctx, q)
	return r.Changes, err
}

// Prune applies the retention rules to unpinned entries.
func (s *Store) Prune(ctx context.Context, max int, retention time.Duration, now time.Time) (int64, error) {
	var total int64
	if retention > 0 {
		r, err := s.db.Execute(ctx, `DELETE FROM entries WHERE pinned=0 AND used_at < ?`, ms(now.Add(-retention)))
		if err != nil {
			return total, err
		}
		total += r.Changes
	}
	r, err := s.db.Execute(ctx, `DELETE FROM entries WHERE pinned=0 AND id NOT IN (SELECT id FROM entries WHERE pinned=0 ORDER BY used_at DESC LIMIT ?)`, max)
	total += r.Changes
	return total, err
}

// Recent returns the n most recently used entries.
func (s *Store) Recent(ctx context.Context, n int) ([]Entry, error) {
	rows, err := s.db.Query(ctx, `SELECT `+listCols+` FROM entries ORDER BY used_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanList), nil
}

// Pinned returns up to n pinned entries, most recently used first.
func (s *Store) Pinned(ctx context.Context, n int) ([]Entry, error) {
	rows, err := s.db.Query(ctx, `SELECT `+listCols+` FROM entries WHERE pinned=1 ORDER BY used_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanList), nil
}

// Counts returns the number of entries, pinned entries and snippets.
func (s *Store) Counts(ctx context.Context) (entries, pinned, snippets int, err error) {
	rows, err := s.db.Query(ctx, `SELECT (SELECT count(*) FROM entries), (SELECT count(*) FROM entries WHERE pinned=1), (SELECT count(*) FROM snippets)`)
	if err != nil {
		return
	}
	r := rows.Values[0]
	return int(asInt(r[0])), int(asInt(r[1])), int(asInt(r[2])), nil
}
