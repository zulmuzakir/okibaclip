package history

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"okibaclip/internal/search"
)

// Snippet is a named text the user keeps.
type Snippet struct {
	ID                   int64
	Name, Body           string
	Folder, Shortcut     string
	UseCount             int
	CreatedAt, UpdatedAt time.Time
	UsedAt               time.Time
}

const snippetCols = `id,name,body,created_at,updated_at,folder,shortcut,use_count,used_at`

func scanSnippet(r []any) Snippet {
	return Snippet{
		ID: asInt(r[0]), Name: asString(r[1]), Body: asString(r[2]), CreatedAt: fromMs(asInt(r[3])), UpdatedAt: fromMs(asInt(r[4])),
		Folder: asString(r[5]), Shortcut: asString(r[6]), UseCount: int(asInt(r[7])), UsedAt: fromMs(asInt(r[8])),
	}
}

// Snippets lists the snippets whose name or body matches every term, most
// used first, or best match first when there are terms. A match in the name
// ranks above the same match in the body.
func (s *Store) Snippets(ctx context.Context, terms []search.Term) ([]Snippet, error) {
	rows, err := s.db.Query(ctx, `SELECT `+snippetCols+` FROM snippets ORDER BY use_count DESC, lower(name), id`)
	if err != nil {
		return nil, err
	}
	type ranked struct {
		sn    Snippet
		score int
	}
	var out []ranked
	for _, r := range rows.Values {
		sn := scanSnippet(r)
		total := 0
		for _, t := range terms {
			sc := search.Score(sn.Body, t)
			if n := search.Score(sn.Name, t); n > 0 {
				sc = max(sc, n+1)
			}
			if sc == 0 {
				total = -1
				break
			}
			total += sc
		}
		if total >= 0 {
			out = append(out, ranked{sn, total})
		}
	}
	slices.SortStableFunc(out, func(a, b ranked) int { return b.score - a.score })
	snips := make([]Snippet, len(out))
	for i, r := range out {
		snips[i] = r.sn
	}
	return snips, nil
}

// Snippet returns one snippet, or ErrNotFound.
func (s *Store) Snippet(ctx context.Context, id int64) (Snippet, error) {
	rows, err := s.db.Query(ctx, `SELECT `+snippetCols+` FROM snippets WHERE id=?`, id)
	if err != nil {
		return Snippet{}, err
	}
	if len(rows.Values) == 0 {
		return Snippet{}, ErrNotFound
	}
	return scanSnippet(rows.Values[0]), nil
}

// UseSnippet counts a use of a snippet.
func (s *Store) UseSnippet(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.Execute(ctx, `UPDATE snippets SET use_count=use_count+1, used_at=? WHERE id=?`, ms(now), id)
	return err
}

// SnippetFolders lists the distinct non-empty folders.
func (s *Store) SnippetFolders(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT folder FROM snippets WHERE folder!='' ORDER BY lower(folder), folder`)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, asString(r[0]))
	}
	return out, nil
}

// ErrInvalidSnippet means a snippet has no name or no body after trimming.
var ErrInvalidSnippet = errors.New("snippet needs a name and a body")

// SaveSnippet inserts a snippet (ID 0) or updates it, and returns its id.
func (s *Store) SaveSnippet(ctx context.Context, sn Snippet) (int64, error) {
	if strings.TrimSpace(sn.Name) == "" || strings.TrimSpace(sn.Body) == "" {
		return 0, ErrInvalidSnippet
	}
	if len(sn.Body) > MaxTextBytes {
		return 0, ErrTooLarge
	}
	now := ms(time.Now())
	if sn.ID == 0 {
		r, err := s.db.Execute(ctx, `INSERT INTO snippets (name,body,folder,shortcut,created_at,updated_at) VALUES (?,?,?,?,?,?)`, sn.Name, sn.Body, sn.Folder, sn.Shortcut, now, now)
		return r.LastInsertID, err
	}
	_, err := s.db.Execute(ctx, `UPDATE snippets SET name=?, body=?, folder=?, shortcut=?, updated_at=? WHERE id=?`, sn.Name, sn.Body, sn.Folder, sn.Shortcut, now, sn.ID)
	return sn.ID, err
}

// DeleteSnippet removes a snippet.
func (s *Store) DeleteSnippet(ctx context.Context, id int64) error {
	_, err := s.db.Execute(ctx, `DELETE FROM snippets WHERE id=?`, id)
	return err
}
