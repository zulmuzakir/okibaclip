package main

import (
	"strings"
	"testing"
	"time"

	"pastello/internal/history"
)

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
