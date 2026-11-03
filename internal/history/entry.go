package history

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
)

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
}

// Capture is a classified clipboard read, before it is stored.
type Capture struct {
	Kind          Kind
	Text, HTML    string
	Image         []byte
	Width, Height int
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
