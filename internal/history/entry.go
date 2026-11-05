package history

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

// Kind is the sort of content an entry holds.
type Kind string

const (
	KindText  Kind = "text"
	KindLink  Kind = "link"
	KindColor Kind = "color"
	KindImage Kind = "image"
	KindFiles Kind = "files"
)

const (
	// MaxTextBytes is the largest text that is recorded.
	MaxTextBytes = 1 << 20
	// MaxImagePixels caps decoded image area; larger images are never decoded.
	MaxImagePixels = 40_000_000
	// MaxHTMLBytes is the largest rich-text payload kept with an entry.
	MaxHTMLBytes = 1 << 20
	// MaxTitleRunes is the longest title an entry can carry.
	MaxTitleRunes = 100
	// MaxUndoBytes bounds the data a delete keeps for undo.
	MaxUndoBytes = 64 << 20
)

// ErrTooManyPixels reports an image whose decoded area is over MaxImagePixels.
var ErrTooManyPixels = errors.New("image has too many pixels")

// ImageDimsOK reports whether a w×h image is non-empty and within MaxImagePixels.
func ImageDimsOK(w, h int) bool {
	return w > 0 && h > 0 && int64(w)*int64(h) <= MaxImagePixels
}

// Entry is a history row. List queries leave Image and HTML empty.
type Entry struct {
	ID                int64
	Kind              Kind
	Text, HTML        string
	Image, Thumb      []byte
	Width, Height     int
	Size              int64
	Hash              string
	Pinned            bool
	CreatedAt, UsedAt time.Time
	UseCount          int
	// Title names a pinned entry; PinOrder sorts pinned entries ascending.
	Title, SourceApp, SourceName string
	PinOrder                     int64
}

// Capture is a classified clipboard read, before it is stored.
type Capture struct {
	Kind          Kind
	Text, HTML    string
	Image         []byte
	Width, Height int
	// SourceApp, SourceName and SourcePath identify the app the copy came
	// from; they don't feed Hash.
	SourceApp, SourceName, SourcePath string
}

var (
	linkRe  = regexp.MustCompile(`^https?://\S+$`)
	colorRe = regexp.MustCompile(`^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
)

// TextKind classifies copied text as a link, a color or plain text.
func TextKind(text string) Kind {
	trimmed := strings.TrimSpace(text)
	switch {
	case linkRe.MatchString(trimmed) && validURL(trimmed):
		return KindLink
	case colorRe.MatchString(trimmed):
		return KindColor
	}
	return KindText
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Host != ""
}

func (c Capture) primary() []byte {
	if c.Kind == KindImage {
		return c.Image
	}
	return []byte(c.Text)
}

// Hash identifies the content of c, for deduplication.
func (c Capture) Hash() string { return hashOf(c.Kind, c.primary()) }

func hashOf(k Kind, primary []byte) string {
	h := sha256.New()
	h.Write([]byte(string(k)))
	h.Write([]byte{0})
	h.Write(primary)
	return hex.EncodeToString(h.Sum(nil))
}

// Size is the number of content bytes.
func (c Capture) Size() int64 {
	if c.Kind == KindImage {
		return int64(len(c.Image))
	}
	return int64(len(c.Text))
}

// MakeThumb scales a PNG to fit 128×128 without upscaling.
func MakeThumb(data []byte) ([]byte, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if !ImageDimsOK(cfg.Width, cfg.Height) {
		return nil, ErrTooManyPixels
	}
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > 128 || h > 128 {
		if w >= h {
			h, w = max(1, h*128/w), 128
		} else {
			w, h = max(1, w*128/h), 128
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
