// Package historytest has helpers for tests that need a history store.
package historytest

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"okibaclip/internal/history"
)

// T0 is a fixed point in time for test data.
var T0 = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

// NewStore opens a store in a temporary directory, closed when the test ends.
func NewStore(t testing.TB) *history.Store {
	t.Helper()
	s, err := history.Open(context.Background(), filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// PNG encodes a blank w×h image.
func PNG(t testing.TB, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Add records c at time at, with a thumbnail for images, and returns its id.
func Add(t testing.TB, s *history.Store, c history.Capture, at time.Time) int64 {
	t.Helper()
	var thumb []byte
	if c.Kind == history.KindImage {
		thumb, _ = history.MakeThumb(c.Image)
	}
	id, _, err := s.Capture(context.Background(), c, thumb, at)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Texts lists the text of each entry.
func Texts(es []history.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Text)
	}
	return out
}
