// Package historytest has helpers for tests that need a history store.
package historytest

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
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

// PNGHeader returns a PNG that declares a w×h image but carries no pixel data.
// png.DecodeConfig reads only the IHDR chunk, so it can exercise size limits
// without allocating a real image.
func PNGHeader(w, h int) []byte {
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8], ihdr[9] = 8, 2 // 8-bit RGB
	chunk := append([]byte("IHDR"), ihdr[:]...)
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, uint32(len(ihdr)))
	out = append(out, chunk...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
	return out
}

// Export encodes entries and snippets as a current-version export file.
func Export(t testing.TB, entries []history.ExportEntry, snippets []history.ExportSnippet) *bytes.Reader {
	t.Helper()
	if entries == nil {
		entries = []history.ExportEntry{}
	}
	if snippets == nil {
		snippets = []history.ExportSnippet{}
	}
	b, err := json.Marshal(struct {
		Format   string                  `json:"format"`
		Version  int                     `json:"version"`
		Entries  []history.ExportEntry   `json:"entries"`
		Snippets []history.ExportSnippet `json:"snippets"`
	}{history.ExportFormat, history.ExportVersion, entries, snippets})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}
