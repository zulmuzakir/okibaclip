package history

import (
	"context"
	"strings"
	"time"
)

// Snippet is a named text the user keeps.
type Snippet struct {
	ID                   int64
	Name, Body           string
	CreatedAt, UpdatedAt time.Time
}

// Snippets lists snippets by name, filtered by a case-insensitive match on name or body.
func (s *Store) Snippets(ctx context.Context, query string) ([]Snippet, error) {
	rows, err := s.db.Query(ctx, `SELECT id,name,body,created_at,updated_at FROM snippets ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var out []Snippet
	for _, r := range rows.Values {
		sn := Snippet{ID: asInt(r[0]), Name: asString(r[1]), Body: asString(r[2]), CreatedAt: fromMs(asInt(r[3])), UpdatedAt: fromMs(asInt(r[4]))}
		if q == "" || strings.Contains(strings.ToLower(sn.Name), q) || strings.Contains(strings.ToLower(sn.Body), q) {
			out = append(out, sn)
		}
	}
	return out, nil
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
