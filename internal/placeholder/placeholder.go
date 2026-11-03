// Package placeholder expands the {date}-style placeholders of a snippet.
package placeholder

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Expand replaces placeholders in body: {date}, {time}, {datetime},
// {clipboard} and {cursor}. "{{" is a literal "{", and unknown {…} stays as
// written. clipboard is called at most once. left is the number of runes
// after the first {cursor} in the result (0 when there is none), i.e. how
// far to move the caret back after pasting.
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
		left = utf8.RuneCountInString(text[cursorAt:])
	}
	return text, left
}
