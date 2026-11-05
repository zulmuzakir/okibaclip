package capture

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"

	"okibaclip/internal/history"
	"okibaclip/internal/history/historytest"
	"okibaclip/internal/settings"
)

func richData(reps ...transfer.Representation) transfer.Data {
	return transfer.New(transfer.NewItem(reps...))
}

func TestClassify(t *testing.T) {
	img := historytest.PNG(t, 4, 3)
	files, _ := transfer.FileData("/tmp/a.txt", "/tmp/b.txt")
	filesAndText := transfer.New(append(files.Items(), transfer.NewItem(transfer.Bytes(transfer.Text, []byte("x"))))...)
	textRep := func(s string) transfer.Representation { return transfer.Bytes(transfer.Text, []byte(s)) }
	def := settings.Default()
	noImg := def
	noImg.CaptureImages = false
	noFiles := def
	noFiles.CaptureFiles = false
	tiny := def
	tiny.MaxImageMB = 1

	type tc struct {
		name     string
		data     transfer.Data
		set      settings.Settings
		pats     []*regexp.Regexp
		wantKind history.Kind
		ok       bool
		reason   string
	}
	textData := richData(textRep("hello"))
	blank := richData(textRep("  \n\t"))
	link := richData(textRep(" https://example.com/a?b=1 \n"))
	notLink := richData(textRep("https://example.com and more"))
	color := richData(textRep("#FFaa00"))
	notColor := richData(textRep("#12345"))
	htmlD := richData(textRep("bold"), transfer.Bytes(transfer.HTML, []byte("<b>bold</b>")))
	imgD := richData(transfer.Bytes(transfer.PNG, img))
	imgText := richData(textRep("alt text"), transfer.Bytes(transfer.PNG, img))
	big := richData(textRep(strings.Repeat("a", history.MaxTextBytes+1)))
	bigImg := richData(transfer.Bytes(transfer.PNG, append(append([]byte{}, img...), make([]byte, 1<<20+1)...)))
	secretPat := []*regexp.Regexp{regexp.MustCompile(`^\d{16}$`)}
	card := richData(textRep("4111111111111111"))
	partial := richData(textRep("4111 1111"))
	sixDigits := richData(textRep("123456\n"))
	sixPat := []*regexp.Regexp{regexp.MustCompile(`^\d{6}$`)}

	cases := []tc{
		{"text", textData, def, nil, history.KindText, true, ""},
		{"blank skipped", blank, def, nil, "", false, ""},
		{"link", link, def, nil, history.KindLink, true, ""},
		{"link with trailing words is text", notLink, def, nil, history.KindText, true, ""},
		{"color", color, def, nil, history.KindColor, true, ""},
		{"bad color is text", notColor, def, nil, history.KindText, true, ""},
		{"html kept", htmlD, def, nil, history.KindText, true, ""},
		{"image only", imgD, def, nil, history.KindImage, true, ""},
		{"text wins over image", imgText, def, nil, history.KindText, true, ""},
		{"images off", imgD, noImg, nil, "", false, ""},
		{"text over 1 MiB", big, def, nil, "", false, "text over 1 MiB"},
		{"image over limit", bigImg, tiny, nil, "", false, "image over 1 MB"},
		{"files win over text", filesAndText, def, nil, history.KindFiles, true, ""},
		{"files off falls to text", filesAndText, noFiles, nil, history.KindText, true, ""},
		{"ignore pattern full match", card, def, secretPat, "", false, "matches ignore pattern"},
		{"ignore pattern is anchored by the regexp", partial, def, secretPat, history.KindText, true, ""},
		{"ignore pattern matches trimmed text", sixDigits, def, sixPat, "", false, "matches ignore pattern"},
	}
	for _, c := range cases {
		got, reason, ok := Classify(c.data, c.data.Formats(), c.set, c.pats)
		if ok != c.ok || (ok && got.Kind != c.wantKind) || reason != c.reason {
			t.Errorf("%s: got kind=%q reason=%q ok=%v; want kind=%q reason=%q ok=%v", c.name, got.Kind, reason, ok, c.wantKind, c.reason, c.ok)
		}
	}
	got, _, _ := Classify(htmlD, htmlD.Formats(), def, nil)
	if got.HTML != "<b>bold</b>" {
		t.Errorf("html = %q", got.HTML)
	}
	got, _, _ = Classify(imgD, imgD.Formats(), def, nil)
	if got.Width != 4 || got.Height != 3 {
		t.Errorf("dims = %dx%d", got.Width, got.Height)
	}
	got, _, _ = Classify(files, files.Formats(), def, nil)
	if got.Text != "/tmp/a.txt\n/tmp/b.txt" {
		t.Errorf("files text = %q", got.Text)
	}
}

func TestSensitive(t *testing.T) {
	for _, m := range sensitiveMarkers {
		if !IsSensitive([]string{"public.utf8-plain-text", m}, nil, nil) {
			t.Errorf("native %q not sensitive", m)
		}
		if !IsSensitive(nil, []transfer.Format{transfer.Text, transfer.Format(m)}, nil) {
			t.Errorf("portable %q not sensitive", m)
		}
	}
	if IsSensitive([]string{"text/plain"}, []transfer.Format{transfer.Text}, nil) {
		t.Error("plain text flagged")
	}
	read := func(b []byte) func(transfer.Format) ([]byte, error) {
		return func(transfer.Format) ([]byte, error) { return b, nil }
	}
	native := []string{"CanIncludeInClipboardHistory"}
	if !IsSensitive(native, nil, read([]byte{0, 0, 0, 0})) {
		t.Error("zero CanIncludeInClipboardHistory must be sensitive")
	}
	if IsSensitive(native, nil, read([]byte{1, 0, 0, 0})) {
		t.Error("non-zero CanIncludeInClipboardHistory must not be sensitive")
	}
}

// pngHeaderOnly returns a PNG that has a valid signature and IHDR chunk and
// nothing else: enough for png.DecodeConfig, without allocating the pixels.
func pngHeaderOnly(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestClassifyRejectsTooManyPixels(t *testing.T) {
	d := richData(transfer.Bytes(transfer.PNG, pngHeaderOnly(8001, 5001)))
	_, reason, ok := Classify(d, d.Formats(), settings.Default(), nil)
	if ok || reason != "image over 40 megapixels" {
		t.Errorf("ok = %v, reason = %q", ok, reason)
	}
}

func TestClassifyAcceptsPixelBoundary(t *testing.T) {
	d := richData(transfer.Bytes(transfer.PNG, pngHeaderOnly(8000, 5000)))
	got, _, ok := Classify(d, d.Formats(), settings.Default(), nil)
	if !ok || got.Width != 8000 || got.Height != 5000 {
		t.Errorf("ok = %v, dims = %dx%d", ok, got.Width, got.Height)
	}
}
