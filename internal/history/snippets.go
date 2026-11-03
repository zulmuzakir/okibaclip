package history

import (
	"context"
	"slices"
	"time"

	"okibaclip/internal/search"
)

// Snippet is a named text the user keeps.
type Snippet struct {
	ID                   int64
	Name, Body           string
	CreatedAt, UpdatedAt time.Time
}

// Snippets lists the snippets whose name or body matches every term, by
// name, or best match first when there are terms. A match in the name ranks
// above the same match in the body.
func (s *Store) Snippets(ctx context.Context, terms []search.Term) ([]Snippet, error) {
	rows, err := s.db.Query(ctx, `SELECT id,name,body,created_at,updated_at FROM snippets ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	type ranked struct {
		sn    Snippet
		score int
	}
	var out []ranked
	for _, r := range rows.Values {
		sn := Snippet{ID: asInt(r[0]), Name: asString(r[1]), Body: asString(r[2]), CreatedAt: fromMs(asInt(r[3])), UpdatedAt: fromMs(asInt(r[4]))}
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

// SaveSnippet inserts a snippet (ID 0) or updates it, and returns its id.
func (s *Store) SaveSnippet(ctx context.Context, sn Snippet) (int64, error) {
	now := ms(time.Now())
	if sn.ID == 0 {
		r, err := s.db.Execute(ctx, `INSERT INTO snippets (name,body,created_at,updated_at) VALUES (?,?,?,?)`, sn.Name, sn.Body, now, now)
		return r.LastInsertID, err
	}
	_, err := s.db.Execute(ctx, `UPDATE snippets SET name=?, body=?, updated_at=? WHERE id=?`, sn.Name, sn.Body, now, sn.ID)
	return sn.ID, err
}

// DeleteSnippet removes a snippet.
func (s *Store) DeleteSnippet(ctx context.Context, id int64) error {
	_, err := s.db.Execute(ctx, `DELETE FROM snippets WHERE id=?`, id)
	return err
}
