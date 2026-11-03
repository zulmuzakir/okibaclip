package search

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)

func TestParse(t *testing.T) {
	pinned, unpinned := true, false
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	for _, tc := range []struct {
		in   string
		want Query
	}{
		{"", Query{}},
		{"  Git   PUSH ", Query{Terms: []Term{{Text: "git"}, {Text: "push"}}}},
		{`"Hello World" foo`, Query{Terms: []Term{{Text: "hello world", Phrase: true}, {Text: "foo"}}}},
		{`"unterminated phrase`, Query{Terms: []Term{{Text: "unterminated phrase", Phrase: true}}}},
		{`"" "  "`, Query{}},
		{"is:pinned", Query{Pinned: &pinned}},
		{"is:pinned is:unpinned", Query{Pinned: &unpinned}},
		{"type:LINK kind:image type:links", Query{Kinds: []string{"link", "image"}}},
		{"type:file", Query{Kinds: []string{"files"}}},
		{"after:today", Query{After: day(2026, 3, 15)}},
		{"since:yesterday", Query{After: day(2026, 3, 14)}},
		{"before:2026-01-31", Query{Before: day(2026, 1, 31)}},
		{"after:7d", Query{After: now.Add(-7 * 24 * time.Hour)}},
		{"after:2w after:3h", Query{After: now.Add(-3 * time.Hour)}},
		{"before:2026-01-31 before:2025-12-01", Query{Before: day(2025, 12, 1)}},
		// Not understood: searched for literally.
		{"type:pdf is:great after:soon before:0d http://x.io a:", Query{Terms: []Term{
			{Text: "type:pdf"}, {Text: "is:great"}, {Text: "after:soon"}, {Text: "before:0d"}, {Text: "http://x.io"}, {Text: "a:"}}}},
	} {
		if got := Parse(tc.in, now); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestScore(t *testing.T) {
	for _, tc := range []struct {
		text string
		term Term
		want int
	}{
		{"git push origin", Term{Text: "push"}, ScoreWord},
		{"Git Push", Term{Text: "push"}, ScoreWord},
		{"git pushed", Term{Text: "push"}, ScoreWordPrefix},
		{"autopush", Term{Text: "push"}, ScoreSubstring},
		{"autopush then push", Term{Text: "push"}, ScoreWord}, // the best occurrence counts
		{"git push", Term{Text: "gtpsh"}, ScoreFuzzy},
		{"git push", Term{Text: "hsup"}, 0},                               // out of order
		{"git push", Term{Text: "gp", Phrase: false}, 0},                  // too short to be fuzzy
		{"git push", Term{Text: "gtpsh", Phrase: true}, 0},                // phrases are not fuzzy
		{"g" + spaces(40) + "tpsh", Term{Text: "gtpsh"}, 0},               // too spread out
		{spaces(FuzzyWindow) + "git push", Term{Text: "gtpsh"}, 0},        // beyond the fuzzy window
		{spaces(FuzzyWindow) + "git push", Term{Text: "push"}, ScoreWord}, // substrings are found anywhere
		{"Grüße aus Köln", Term{Text: "köln"}, ScoreWord},
		{"hello world", Term{Text: "o w", Phrase: true}, ScoreSubstring},
	} {
		if got := Score(tc.text, tc.term); got != tc.want {
			t.Errorf("Score(%.30q, %+v) = %d, want %d", tc.text, tc.term, got, tc.want)
		}
	}
}

func TestFuzzyPicksShortestWindow(t *testing.T) {
	// The first "a" would stretch the match over the whole text; the
	// shortest window is at the end.
	text := []rune("a......... abc")
	got := fuzzy(text, []rune("abc"))
	if want := []int{11, 12, 13}; !reflect.DeepEqual(got, want) {
		t.Errorf("fuzzy = %v, want %v", got, want)
	}
}

func TestHighlight(t *testing.T) {
	for _, tc := range []struct {
		text  string
		terms []Term
		want  []string
	}{
		{"Push to git, then push", []Term{{Text: "push"}}, []string{"Push", "push"}},
		{"git push", []Term{{Text: "gtpsh"}}, []string{"g", "t", "p", "sh"}}, // adjacent letters merge
		{"abcdef", []Term{{Text: "bc"}, {Text: "cde"}}, []string{"bcde"}},    // overlapping merge
		{"\xffpush", []Term{{Text: "push"}}, []string{"push"}},
		{"Grüße aus Köln", []Term{{Text: "köln"}, {Text: "grüße"}}, []string{"Grüße", "Köln"}},
		{"nothing here", []Term{{Text: "zzz"}}, nil},
		{"anything", nil, nil},
	} {
		var got []string
		for _, r := range Highlight(tc.text, tc.terms) {
			got = append(got, tc.text[r.Start:r.End])
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Highlight(%q, %v) = %q, want %q", tc.text, tc.terms, got, tc.want)
		}
	}
}

func spaces(n int) string { return strings.Repeat(" ", n) }
