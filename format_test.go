package main

import (
	"strings"
	"testing"
	"time"

	"pastello/internal/history"
)

func TestRowLines(t *testing.T) {
	for _, tc := range []struct {
		e             history.Entry
		title, detail string
	}{
		{history.Entry{Kind: history.KindText, Text: "one line", Size: 8}, "one line", "Text · 8 B"},
		{history.Entry{Kind: history.KindText, Text: "\n  first   line \n\n second\nthird\nfourth"}, "first line", "second · +2 lines"},
		{history.Entry{Kind: history.KindText, Text: "a\nb"}, "a", "b"},
		{history.Entry{Kind: history.KindText, Text: " \n\t", Size: 3}, "", "Text · 3 B"},
		{history.Entry{Kind: history.KindText, Text: strings.Repeat("é", 130)}, strings.Repeat("é", 120) + "…", "Text · 0 B"},
		{history.Entry{Kind: history.KindLink, Text: " https://github.com/x?y=1 "}, "https://github.com/x?y=1", "Link · github.com"},
		{history.Entry{Kind: history.KindColor, Text: "#ff8800"}, "#ff8800", "Color"},
		{history.Entry{Kind: history.KindImage, Width: 1920, Height: 1080, Size: 2048}, "Image", "1920×1080 · 2.0 KB"},
		{history.Entry{Kind: history.KindFiles, Text: "/home/u/a.txt\n/home/u/b.txt"}, "a.txt +1 more", "/home/u"},
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
		e    history.Entry
		want string
	}{
		{history.Entry{Kind: history.KindText, Size: 10, UseCount: 1, UsedAt: now.Add(-30 * time.Second), CreatedAt: first},
			"10 B · last used just now · first copied Jan 2, 2026 09:05"},
		{history.Entry{Kind: history.KindText, Size: 10, UseCount: 3, UsedAt: now.Add(-2 * time.Hour), CreatedAt: first},
			"10 B · used 3× · last used 2h ago · first copied Jan 2, 2026 09:05"},
		{history.Entry{Kind: history.KindImage, Width: 4, Height: 3, Size: 10, UseCount: 1, UsedAt: first, CreatedAt: first},
			"4×3 · 10 B · last used on Jan 2 · first copied Jan 2, 2026 09:05"},
	} {
		if got := details(tc.e, now); got != tc.want {
			t.Errorf("details = %q, want %q", got, tc.want)
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
