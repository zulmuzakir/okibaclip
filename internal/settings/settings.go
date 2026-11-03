package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Settings are the user's preferences, stored as settings.json.
type Settings struct {
	Shortcut        string   `json:"shortcut"`
	MaxEntries      int      `json:"maxEntries"`
	RetentionDays   int      `json:"retentionDays"`
	CaptureImages   bool     `json:"captureImages"`
	CaptureFiles    bool     `json:"captureFiles"`
	MaxImageMB      int      `json:"maxImageMB"`
	SkipSensitive   bool     `json:"skipSensitive"`
	IgnorePatterns  []string `json:"ignorePatterns"`
	PollMillis      int      `json:"pollMillis"`
	MoveToTopOnCopy bool     `json:"moveToTopOnCopy"`
	FirstRunDone    bool     `json:"firstRunDone"`
}

// PollIntervals are the allowed clipboard poll intervals, in milliseconds.
var PollIntervals = []int{250, 500, 1000}

// PatternError reports an ignore pattern that does not compile.
type PatternError struct {
	Line int
	Err  error
}

func (e *PatternError) Error() string { return fmt.Sprintf("line %d: %v", e.Line, e.Err) }
func (e *PatternError) Unwrap() error { return e.Err }

// Default returns the settings of a first run.
func Default() Settings {
	return Settings{
		MaxEntries:      500,
		CaptureImages:   true,
		CaptureFiles:    true,
		MaxImageMB:      10,
		SkipSensitive:   true,
		PollMillis:      500,
		MoveToTopOnCopy: true,
	}
}

// Load reads path over the defaults. A missing file is not an
// error; a corrupt one returns the defaults and the error.
func Load(path string) (Settings, error) {
	s := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return Default(), err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		s = Default()
		s.Normalize()
		return s, fmt.Errorf("settings %s: %w", path, err)
	}
	s.Normalize()
	return s, nil
}

// Normalize clamps values to their ranges and drops blank patterns.
func (s *Settings) Normalize() {
	s.MaxEntries = min(max(s.MaxEntries, 50), 5000)
	s.RetentionDays = min(max(s.RetentionDays, 0), 3650)
	s.MaxImageMB = min(max(s.MaxImageMB, 1), 50)
	if !slices.Contains(PollIntervals, s.PollMillis) {
		s.PollMillis = 500
	}
	var keep []string
	for _, l := range s.IgnorePatterns {
		if strings.TrimSpace(l) != "" {
			keep = append(keep, l)
		}
	}
	s.IgnorePatterns = keep
}

// Save writes s to path atomically, with mode 0600.
func Save(path string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CompilePatterns compiles one regexp per line; blank lines are skipped
// and the error names the first bad line (1-based, counting blanks).
func CompilePatterns(lines []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		re, err := regexp.Compile(l)
		if err != nil {
			return nil, &PatternError{Line: i + 1, Err: err}
		}
		out = append(out, re)
	}
	return out, nil
}
