// Package placeholder expands the {date}-style placeholders of a snippet.
package placeholder

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-text/typesetting/segmenter"
)

// Expand replaces placeholders in body: {date}, {time}, {datetime},
// {clipboard} and {cursor}. "{{" is a literal "{", and unknown {…} stays as
// written. A "{" inside a brace pair that is not a placeholder is literal,
// and placeholders after it still expand ("{x{time}" → "{x15:06"). clipboard
// is called at most once. left is the number of user-perceived characters
// (grapheme clusters) after the first {cursor} in the result (0 when there
// is none), i.e. how far to move the caret back after pasting.
func Expand(body string, now time.Time, clipboard func() string) (text string, left int) {
	var out strings.Builder
	var clip string
	var haveClip, haveCursor bool
	cursorAt := 0 // byte offset in out
	for i := 0; i < len(body); {
		if body[i] != '{' {
			out.WriteByte(body[i])
			i++
			continue
		}
		if strings.HasPrefix(body[i:], "{{") {
			out.WriteByte('{')
			i += 2
			continue
		}
		end := strings.IndexByte(body[i:], '}')
		if end < 0 {
			out.WriteString(body[i:])
			break
		}
		if strings.IndexByte(body[i+1:i+end], '{') >= 0 {
			out.WriteByte('{') // unmatched brace: literal; placeholders inside still expand
			i++
			continue
		}
		name := body[i+1 : i+end]
		switch name {
		case "date":
			out.WriteString(now.Format("2006-01-02"))
		case "time":
			out.WriteString(now.Format("15:04"))
		case "datetime":
			out.WriteString(now.Format("2006-01-02 15:04"))
		case "clipboard":
			if !haveClip {
				haveClip = true
				if clipboard != nil {
					clip = clipboard()
				}
			}
			out.WriteString(clip)
		case "cursor":
			if !haveCursor {
				haveCursor, cursorAt = true, out.Len()
			}
		default:
			out.WriteString(body[i : i+end+1])
		}
		i += end + 1
	}
	text = out.String()
	if haveCursor {
		left = countGraphemes(text[cursorAt:])
	}
	return text, left
}

// countGraphemes returns the number of UAX #29 grapheme clusters in s ("\r\n" is one); invalid input falls back to runes.
func countGraphemes(s string) int {
	var seg segmenter.Segmenter
	if err := seg.InitWithString(s); err != nil {
		return utf8.RuneCountInString(s)
	}
	n := 0
	for it := seg.GraphemeIterator(); it.Next(); {
		n++
	}
	return n
}
