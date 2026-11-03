package history_test

import (
	"bytes"
	"image/png"
	"testing"

	"okibaclip/internal/history"
	"okibaclip/internal/history/historytest"
)

func TestHashDistinguishesKinds(t *testing.T) {
	a := history.Capture{Kind: history.KindText, Text: "x"}.Hash()
	b := history.Capture{Kind: history.KindLink, Text: "x"}.Hash()
	if a == b || a != (history.Capture{Kind: history.KindText, Text: "x", HTML: "<i>x</i>"}).Hash() {
		t.Error("hash must depend on kind and primary bytes only")
	}
}

func TestMakeThumb(t *testing.T) {
	th, err := history.MakeThumb(historytest.PNG(t, 400, 200))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := png.DecodeConfig(bytes.NewReader(th))
	if cfg.Width != 128 || cfg.Height != 64 {
		t.Errorf("thumb = %dx%d", cfg.Width, cfg.Height)
	}
	th, _ = history.MakeThumb(historytest.PNG(t, 20, 10))
	cfg, _ = png.DecodeConfig(bytes.NewReader(th))
	if cfg.Width != 20 || cfg.Height != 10 {
		t.Errorf("small image upscaled to %dx%d", cfg.Width, cfg.Height)
	}
}

func TestTextKind(t *testing.T) {
	cases := map[string]history.Kind{
		"hello":                       history.KindText,
		" https://example.com/a?b=1 ": history.KindLink,
		"https://example.com and":     history.KindText,
		"#FFaa00":                     history.KindColor,
		"#12345":                      history.KindText,
	}
	for in, want := range cases {
		if got := history.TextKind(in); got != want {
			t.Errorf("TextKind(%q) = %q, want %q", in, got, want)
		}
	}
}
