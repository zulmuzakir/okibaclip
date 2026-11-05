package placeholder

import (
	"testing"
	"time"
)

var now = time.Date(2025, 3, 4, 15, 6, 0, 0, time.UTC)

func TestExpand(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		left     int
	}{
		{"on {date} at {time}", "on 2025-03-04 at 15:06", 0},
		{"{datetime}", "2025-03-04 15:06", 0},
		{"a{cursor}bc", "abc", 2},
		{"a{cursor}b{cursor}c", "abc", 2},
		{"{{date}", "{date}", 0},
		{"{unknown} {", "{unknown} {", 0},
		{"héé{cursor}é!", "hééé!", 2},
		{"no placeholders", "no placeholders", 0},
		{"{nope {date}", "{nope 2025-03-04", 0},
		{"{a {cursor}bc", "{a bc", 2},
		{"{x{time}y", "{x15:06y", 0},
		{"{cursor}e\u0301x", "e\u0301x", 2},
		{"{cursor}\U0001F1FA\U0001F1F8!", "\U0001F1FA\U0001F1F8!", 2},
		{"{cursor}\U0001F44D\U0001F3FD", "\U0001F44D\U0001F3FD", 1},
		{"{cursor}a\r\nb", "a\r\nb", 3},
		{"a{unknown}b", "a{unknown}b", 0},
	} {
		got, left := Expand(tc.in, now, nil)
		if got != tc.want || left != tc.left {
			t.Errorf("Expand(%q) = %q, %d; want %q, %d", tc.in, got, left, tc.want, tc.left)
		}
	}
}

func TestExpandReadsClipboardOnce(t *testing.T) {
	calls := 0
	got, _ := Expand("{clipboard}-{clipboard}", now, func() string { calls++; return "x" })
	if got != "x-x" || calls != 1 {
		t.Errorf("got %q after %d calls", got, calls)
	}
	if got, _ := Expand("none", now, func() string { calls++; return "y" }); got != "none" || calls != 1 {
		t.Errorf("clipboard read without {clipboard}")
	}
}
