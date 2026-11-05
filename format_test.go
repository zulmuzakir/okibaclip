package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"okibaclip/internal/history"
)

func TestRowLines(t *testing.T) {
	// Native paths: filepath.Dir gives \home\u for /home/u/a.txt on Windows.
	dir := filepath.Join(string(filepath.Separator)+"home", "u")
	files := filepath.Join(dir, "a.txt") + "\n" + filepath.Join(dir, "b.txt")
	for _, tc := range []struct {
		e             history.Entry
		title, detail string
	}{
		{history.Entry{Kind: history.KindText, Text: "one line", Size: 8}, "one line", "Text"},
		{history.Entry{Kind: history.KindText, Text: "\n  first   line \n\n second\nthird\nfourth"}, "first line", "second · +2 lines"},
		{history.Entry{Kind: history.KindText, Text: "a\nb"}, "a", "b"},
		{history.Entry{Kind: history.KindText, Text: " \n\t", Size: 3}, "", "Text"},
		{history.Entry{Kind: history.KindText, Text: strings.Repeat("é", 130)}, strings.Repeat("é", 120) + "…", "Text"},
		{history.Entry{Kind: history.KindLink, Text: " https://github.com/x?y=1 "}, "https://github.com/x?y=1", "Link · github.com"},
		{history.Entry{Kind: history.KindColor, Text: "#ff8800"}, "#ff8800", "Color"},
		{history.Entry{Kind: history.KindImage, Width: 1920, Height: 1080, Size: 2048}, "Image", "1920×1080 · 2.0 KB"},
		{history.Entry{Kind: history.KindFiles, Text: files}, "a.txt +1 more", dir},
		{history.Entry{Kind: history.KindText, Text: "x", Size: 2048, UseCount: 3}, "x", "Text · used 3× · 2.0 KB"},
	} {
		if title, detail := rowLines(tc.e); title != tc.title || detail != tc.detail {
			t.Errorf("rowLines(%.20q) = %q, %q; want %q, %q", tc.e.Text, title, detail, tc.title, tc.detail)
		}
	}
}

func TestDetails(t *testing.T) {
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	first := time.Date(2026, 1, 2, 9, 5, 0, 0, time.UTC)
	for _, tc := range []struct {
		e     history.Entry
		stats string
		want  string
	}{
		{history.Entry{Kind: history.KindText, Size: 10, UseCount: 1, UsedAt: now.Add(-30 * time.Second), CreatedAt: first}, "",
			"10 B · last used just now · first copied Jan 2, 2026 09:05"},
		{history.Entry{Kind: history.KindText, Size: 10, UseCount: 3, UsedAt: now.Add(-2 * time.Hour), CreatedAt: first}, "13 characters · 1 word",
			"13 characters · 1 word · used 3× · last used 2h ago · first copied Jan 2, 2026 09:05"},
		{history.Entry{Kind: history.KindImage, Width: 4, Height: 3, Size: 10, UseCount: 1, UsedAt: first, CreatedAt: first}, "",
			"4×3 · 10 B · last used on Jan 2 · first copied Jan 2, 2026 09:05"},
		{history.Entry{Kind: history.KindLink, Size: 10, UseCount: 1, UsedAt: first, CreatedAt: first}, "",
			"last used on Jan 2 · first copied Jan 2, 2026 09:05"},
	} {
		if got := details(tc.e, tc.stats, now); got != tc.want {
			t.Errorf("details = %q, want %q", got, tc.want)
		}
	}
}

func TestTextStats(t *testing.T) {
	for text, want := range map[string]string{
		"automatically":    "13 characters · 1 word",
		"a b\nc":           "5 characters · 3 words · 2 lines",
		"héllo\r\nworld\n": "13 characters · 2 words · 2 lines",
		"":                 "0 characters · 0 words",
	} {
		if got := textStats(text); got != want {
			t.Errorf("textStats(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestLooksLikeCode(t *testing.T) {
	for text, want := range map[string]bool{
		"func main() {\n\tfmt.Println(1)\n}": true,
		`{"a":1}`:                            true,
		"[1, 2]":                             true,
		"Dear Bob,\nThanks for lunch.\nBest": false,
		"one line;":                          false,
		"- item one\n- item two":             false,
	} {
		if got := looksLikeCode(text); got != want {
			t.Errorf("looksLikeCode(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestColorValues(t *testing.T) {
	for in, want := range map[string][2]string{
		"#ff8800":   {"#FF8800", "rgb(255, 136, 0)"},
		"#ff880080": {"#FF880080", "rgba(255, 136, 0, 0.50)"},
		"#fff":      {"#FFFFFF", "rgb(255, 255, 255)"},
	} {
		if hex, rgb := colorValues(ui.Hex(in)); hex != want[0] || rgb != want[1] {
			t.Errorf("colorValues(%s) = %q, %q; want %q, %q", in, hex, rgb, want[0], want[1])
		}
	}
}

func TestPreviewFormats(t *testing.T) {
	cases := []struct {
		e    history.Entry
		want string
	}{
		{history.Entry{Kind: history.KindText, Text: "\n  hello \t world \n"}, "hello world"},
		{history.Entry{Kind: history.KindText, Text: "one\ntwo\n\nthree"}, "one +2 lines"},
		{history.Entry{Kind: history.KindText, Text: strings.Repeat("é", 130)}, strings.Repeat("é", 120) + "…"},
		{history.Entry{Kind: history.KindImage, Width: 10, Height: 20, Size: 1536}, "Image · 10×20 · 1.5 KB"},
		{history.Entry{Kind: history.KindFiles, Text: "/a/b/c.txt"}, "c.txt"},
		{history.Entry{Kind: history.KindFiles, Text: "/a/b/c.txt\n/a/d.txt\n/e.txt"}, "c.txt +2 more"},
		{history.Entry{Kind: history.KindLink, Text: " https://x.io \n"}, "https://x.io"},
		{history.Entry{Kind: history.KindColor, Text: "#fff"}, "#fff"},
	}
	for _, c := range cases {
		if got := preview(c.e); got != c.want {
			t.Errorf("preview(%+v) = %q, want %q", c.e, got, c.want)
		}
	}
}

func TestRelTime(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "now"},
		{now.Add(-5 * time.Minute), "5m"},
		{now.Add(-3 * time.Hour), "3h"},
		{now.Add(-2 * 24 * time.Hour), "2d"},
		{time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), "Jan 2"},
		{time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC), "Jan 2, 2023"},
	}
	for _, c := range cases {
		if got := relTime(now, c.t); got != c.want {
			t.Errorf("relTime(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 5 << 20: "5.0 MB"}
	for n, want := range cases {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}
