package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"my-clipboard/internal/history"
)

// preview is the single-line description of an entry.
func preview(e history.Entry) string {
	switch e.Kind {
	case history.KindImage:
		return fmt.Sprintf("Image · %d×%d · %s", e.Width, e.Height, humanSize(e.Size))
	case history.KindFiles:
		paths := strings.Split(e.Text, "\n")
		s := filepath.Base(paths[0])
		if n := len(paths) - 1; n > 0 {
			s += fmt.Sprintf(" +%d more", n)
		}
		return s
	case history.KindLink, history.KindColor:
		return strings.TrimSpace(e.Text)
	}
	return textPreview(e.Text, 120)
}

// textPreview is the first non-blank line, whitespace collapsed, cut at
// limit runes, with " +N lines" when more lines follow.
func textPreview(text string, limit int) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	first := -1
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			first = i
			break
		}
	}
	if first < 0 {
		return ""
	}
	s := strings.Join(strings.Fields(lines[first]), " ")
	if utf8.RuneCountInString(s) > limit {
		s = string([]rune(s)[:limit]) + "…"
	}
	more := 0
	for _, l := range lines[first+1:] {
		if strings.TrimSpace(l) != "" {
			more++
		}
	}
	if more > 0 {
		s += fmt.Sprintf(" +%d lines", more)
	}
	return s
}

func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func relTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2, 2006")
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

func firstRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// fileSize is the size of the file at path, or 0.
func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}
