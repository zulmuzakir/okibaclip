package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"okibaclip/internal/history"
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

// rowLines are the two lines of an entry's list row: what it holds, then
// its second line of text, or else its kind and a detail.
func rowLines(e history.Entry) (title, detail string) {
	switch e.Kind {
	case history.KindImage:
		return "Image", fmt.Sprintf("%d×%d · %s", e.Width, e.Height, humanSize(e.Size))
	case history.KindFiles:
		first, _, _ := strings.Cut(e.Text, "\n")
		return preview(e), filepath.Dir(first)
	case history.KindLink:
		text := strings.TrimSpace(e.Text)
		if u, err := url.Parse(text); err == nil && u.Host != "" {
			return text, "Link · " + u.Host
		}
		return text, "Link"
	case history.KindColor:
		return strings.TrimSpace(e.Text), "Color"
	}
	var lines []string
	for _, l := range strings.Split(e.Text, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			lines = append(lines, l)
		}
	}
	var base []string
	if e.UseCount > 1 {
		base = append(base, fmt.Sprintf("used %d×", e.UseCount))
	}
	if e.Size >= 1024 {
		base = append(base, humanSize(e.Size))
	}
	meta := strings.Join(append([]string{"Text"}, base...), " · ")
	switch len(lines) {
	case 0:
		return "", meta
	case 1:
		return truncRunes(lines[0], 120), meta
	}
	detail = truncRunes(lines[1], 120)
	if n := len(lines) - 2; n > 0 {
		detail += fmt.Sprintf(" · +%d lines", n)
	}
	return truncRunes(lines[0], 120), detail
}

// kindTitle names an entry's kind for the preview header.
func kindTitle(e history.Entry) string {
	switch e.Kind {
	case history.KindImage:
		return "Image"
	case history.KindFiles:
		if n := strings.Count(e.Text, "\n") + 1; n > 1 {
			return fmt.Sprintf("%d Files", n)
		}
		return "File"
	}
	k := string(e.Kind)
	return strings.ToUpper(k[:1]) + k[1:]
}

// details describes an entry's size and use for the preview footer. stats
// is the text's textStats, empty when not known.
func details(e history.Entry, stats string, now time.Time) string {
	var parts []string
	switch e.Kind {
	case history.KindImage:
		parts = append(parts, fmt.Sprintf("%d×%d", e.Width, e.Height), humanSize(e.Size))
	case history.KindText:
		if stats != "" {
			parts = append(parts, stats)
		} else {
			parts = append(parts, humanSize(e.Size))
		}
	}
	if e.UseCount > 1 {
		parts = append(parts, fmt.Sprintf("used %d×", e.UseCount))
	}
	parts = append(parts, "last used "+ago(now, e.UsedAt), "first copied "+e.CreatedAt.Format("Jan 2, 2006 15:04"))
	return strings.Join(parts, " · ")
}

// plural is "1 word" or "N words".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// textStats counts text's characters and words, and its lines past one:
// "13 characters · 1 word", "5 characters · 3 words · 2 lines".
func textStats(text string) string {
	parts := []string{plural(utf8.RuneCountInString(text), "character"), plural(len(strings.Fields(text)), "word")}
	s := strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if n := strings.Count(s, "\n") + 1; n > 1 {
		parts = append(parts, plural(n, "line"))
	}
	return strings.Join(parts, " · ")
}

// looksLikeCode reports whether text reads as code or data: one {…} or
// […] block, or two or more lines of which a third are indented or half
// end in one of {};()[].
func looksLikeCode(text string) bool {
	t := strings.TrimSpace(text)
	if (strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")) || (strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")) {
		return true
	}
	n, indented, ends := 0, 0, 0
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n++
		if strings.HasPrefix(l, "\t") || strings.HasPrefix(l, "  ") {
			indented++
		}
		if r := strings.TrimRight(l, " \t"); r != "" && strings.ContainsRune("{};()[]", rune(r[len(r)-1])) {
			ends++
		}
	}
	if n < 2 {
		return false
	}
	return indented*3 >= n || ends*2 >= n
}

// colorValues spells a "#rgb", "#rgba", "#rrggbb" or "#rrggbbaa" color as
// upper-case #RRGGBB (#RRGGBBAA when not opaque) and as rgb(r, g, b), or
// rgba(r, g, b, a) with a to two decimals when not opaque.
func colorValues(c ui.Color) (hex, rgb string) {
	hex = fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
	if c.A == 255 {
		return hex, fmt.Sprintf("rgb(%d, %d, %d)", c.R, c.G, c.B)
	}
	a := strconv.FormatFloat(float64(c.A)/255, 'f', 2, 64)
	return hex + fmt.Sprintf("%02X", c.A), fmt.Sprintf("rgba(%d, %d, %d, %s)", c.R, c.G, c.B, a)
}

// ago is relTime as a phrase: "just now", "5m ago", "on Jan 2".
func ago(now, t time.Time) string {
	r := relTime(now, t)
	switch {
	case r == "now":
		return "just now"
	case now.Sub(t) < 7*24*time.Hour:
		return r + " ago"
	}
	return "on " + r
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
