// Package search parses the panel's search queries and matches their words
// against clipboard text.
//
// A query is words, "quoted phrases" and filters (is:pinned, type:link,
// after:7d, before:2026-01-31). Every word must match the text, case
// insensitively: as a substring anywhere, or, for unquoted words of three or
// more characters, as a fuzzy subsequence (its letters in order, gaps
// allowed) within the first FuzzyWindow characters. A word or filter that
// cannot be understood is searched for literally.
package search

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// FuzzyWindow is how many characters at the start of a text fuzzy matching
// looks at; substrings are found anywhere.
const FuzzyWindow = 512

// minFuzzy is the shortest word matched fuzzily: shorter ones would match
// nearly everything.
const minFuzzy = 3

// Scores of a word's match, best first. A text's score is the sum over the
// query's words; equal scores keep the order of last use.
const (
	ScoreWord       = 4 // a whole word: "push" in "git push"
	ScoreWordPrefix = 3 // the start of a word: "pus" in "git push"
	ScoreSubstring  = 2 // inside a word: "ush" in "git push"
	ScoreFuzzy      = 1 // letters in order: "gtpsh" in "git push"
)

// Term is a word or quoted phrase of a query, lower-cased.
type Term struct {
	Text   string
	Phrase bool // quoted: matched only as a substring
}

// Fuzzy reports whether the term may match as a subsequence.
func (t Term) Fuzzy() bool {
	return !t.Phrase && utf8.RuneCountInString(t.Text) >= minFuzzy
}

// Query is a parsed search.
type Query struct {
	Terms []Term
	// Kinds are the kinds of content type: asks for, any of them: "text",
	// "link", "color", "image" or "files".
	Kinds []string
	// Pinned, when set, keeps only pinned (true) or unpinned (false) entries.
	Pinned *bool
	// After and Before bound the time of last use: at or after After, and
	// before Before. Zero leaves that side open.
	After, Before time.Time
}

// HasFilters reports whether the query has any filter besides its words.
func (q Query) HasFilters() bool {
	return len(q.Kinds) > 0 || q.Pinned != nil || !q.After.IsZero() || !q.Before.IsZero()
}

// IsZero reports whether the query selects everything.
func (q Query) IsZero() bool { return len(q.Terms) == 0 && !q.HasFilters() }

var kindNames = map[string]string{
	"text": "text", "link": "link", "links": "link", "url": "link",
	"color": "color", "colour": "color", "image": "image", "images": "image", "img": "image",
	"file": "files", "files": "files",
}

// Parse reads a query typed in the search field; now anchors relative dates.
func Parse(s string, now time.Time) Query {
	var q Query
	for len(s) > 0 {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		if s == "" {
			break
		}
		if s[0] == '"' {
			phrase, rest, _ := strings.Cut(s[1:], `"`)
			s = rest
			if p := strings.ToLower(strings.TrimSpace(phrase)); p != "" {
				q.Terms = append(q.Terms, Term{Text: p, Phrase: true})
			}
			continue
		}
		end := strings.IndexFunc(s, unicode.IsSpace)
		if end < 0 {
			end = len(s)
		}
		word := strings.ToLower(s[:end])
		s = s[end:]
		if !q.filter(word, now) {
			q.Terms = append(q.Terms, Term{Text: word})
		}
	}
	return q
}

// filter applies word as a filter and reports whether it was one.
func (q *Query) filter(word string, now time.Time) bool {
	key, val, ok := strings.Cut(word, ":")
	if !ok || val == "" {
		return false
	}
	switch key {
	case "is":
		switch val {
		case "pinned", "unpinned":
			pinned := val == "pinned"
			q.Pinned = &pinned
			return true
		}
	case "type", "kind":
		if k, ok := kindNames[val]; ok {
			if !slices.Contains(q.Kinds, k) {
				q.Kinds = append(q.Kinds, k)
			}
			return true
		}
	case "after", "since":
		if t, ok := parseWhen(val, now); ok {
			if t.After(q.After) {
				q.After = t
			}
			return true
		}
	case "before":
		if t, ok := parseWhen(val, now); ok {
			if q.Before.IsZero() || t.Before(q.Before) {
				q.Before = t
			}
			return true
		}
	}
	return false
}

// parseWhen reads today, yesterday, a date (2006-01-02) as the start of that
// day, or a span back from now: 12h, 3d, 2w.
func parseWhen(s string, now time.Time) (time.Time, bool) {
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()) }
	switch s {
	case "today":
		return day(now), true
	case "yesterday":
		return day(now.AddDate(0, 0, -1)), true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t, true
	}
	units := map[byte]time.Duration{'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
	if unit, ok := units[s[len(s)-1]]; ok {
		if n, err := strconv.Atoi(s[:len(s)-1]); err == nil && n > 0 && n <= 100000 {
			return now.Add(-time.Duration(n) * unit), true
		}
	}
	return time.Time{}, false
}

// Score rates how well term matches text, 0 for no match.
func Score(text string, t Term) int {
	if t.Text == "" {
		return ScoreWord
	}
	low := []rune(strings.ToLower(text))
	pat := []rune(t.Text)
	best := 0
	for _, i := range occurrences(low, pat, 1000) {
		before := i == 0 || !isWordRune(low[i-1])
		after := i+len(pat) == len(low) || !isWordRune(low[i+len(pat)])
		switch {
		case before && after:
			return ScoreWord
		case before:
			best = ScoreWordPrefix
		case best == 0:
			best = ScoreSubstring
		}
	}
	if best == 0 && t.Fuzzy() && fuzzy(low, pat) != nil {
		best = ScoreFuzzy
	}
	return best
}

// Range is a byte range of a text: [Start, End).
type Range struct{ Start, End int }

// Highlight returns the ranges of text the terms match, sorted and merged:
// every substring occurrence of a term, or, when there is none, its fuzzy
// match.
func Highlight(text string, terms []Term) []Range {
	if len(terms) == 0 || text == "" {
		return nil
	}
	runes := make([]rune, 0, len(text))
	offs := make([]int, 0, len(text)+1)
	for i, r := range text {
		runes = append(runes, r)
		offs = append(offs, i)
	}
	offs = append(offs, len(text))
	low := make([]rune, len(runes))
	for i, r := range runes {
		low[i] = unicode.ToLower(r)
	}
	var rs []Range
	for _, t := range terms {
		pat := []rune(t.Text)
		if len(pat) == 0 {
			continue
		}
		occ := occurrences(low, pat, 200)
		for _, i := range occ {
			rs = append(rs, Range{offs[i], offs[i+len(pat)]})
		}
		if len(occ) == 0 && t.Fuzzy() {
			for _, i := range fuzzy(low, pat) {
				rs = append(rs, Range{offs[i], offs[i+1]})
			}
		}
	}
	slices.SortFunc(rs, func(a, b Range) int { return a.Start - b.Start })
	var out []Range
	for _, r := range rs {
		if n := len(out); n > 0 && r.Start <= out[n-1].End {
			out[n-1].End = max(out[n-1].End, r.End)
			continue
		}
		out = append(out, r)
	}
	return out
}

// occurrences returns the rune indices at which pat starts in text, at most
// limit of them, not overlapping.
func occurrences(text, pat []rune, limit int) []int {
	var out []int
	for i := 0; i+len(pat) <= len(text) && len(out) < limit; i++ {
		if slices.Equal(text[i:i+len(pat)], pat) {
			out = append(out, i)
			i += len(pat) - 1
		}
	}
	return out
}

// fuzzy finds pat's letters in order within the first FuzzyWindow runes of
// text, in the shortest stretch, which may span at most twice pat's length
// plus two ("gtpsh" in "git push", "cfg" in "config"). It returns their
// indices, or nil.
func fuzzy(text, pat []rune) []int {
	text = text[:min(len(text), FuzzyWindow)]
	bestStart, bestEnd := -1, -1
	for s := range text {
		if text[s] != pat[0] {
			continue
		}
		// Forward to the end of the first match from s, then back to the
		// latest start that still matches: the shortest window ending there.
		j, e := 0, s
		for ; e < len(text) && j < len(pat); e++ {
			if text[e] == pat[j] {
				j++
			}
		}
		if j < len(pat) {
			break // no later start can match either
		}
		e--
		k, b := len(pat)-1, e
		for ; k >= 0; b-- {
			if text[b] == pat[k] {
				k--
			}
		}
		b++
		if bestStart < 0 || e-b < bestEnd-bestStart {
			bestStart, bestEnd = b, e
		}
	}
	if bestStart < 0 || bestEnd-bestStart+1 > 2*len(pat)+2 {
		return nil
	}
	pos := make([]int, 0, len(pat))
	for i, j := bestStart, 0; j < len(pat); i++ {
		if text[i] == pat[j] {
			pos = append(pos, i)
			j++
		}
	}
	return pos
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
