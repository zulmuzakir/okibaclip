package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"okibaclip/internal/fsutil"
	"okibaclip/internal/history"
)

// Settings are the user's preferences, stored as settings.json.
type Settings struct {
	Shortcut        string   `json:"shortcut"`
	MaxEntries      int      `json:"maxEntries"`
	RetentionDays   int      `json:"retentionDays"`
	CaptureImages   bool     `json:"captureImages"`
	CaptureFiles    bool     `json:"captureFiles"`
	MaxImageMB      int      `json:"maxImageMB"`
	MaxStorageMB    int      `json:"maxStorageMB"`
	SkipSensitive   bool     `json:"skipSensitive"`
	IgnorePatterns  []string `json:"ignorePatterns"`
	IgnoreApps      []string `json:"ignoreApps"`
	PollMillis      int      `json:"pollMillis"`
	MoveToTopOnCopy bool     `json:"moveToTopOnCopy"`
	PasteAfterCopy  bool     `json:"pasteAfterCopy"`
	FirstRunDone    bool     `json:"firstRunDone"`
}

// PollIntervals are the allowed clipboard poll intervals, in milliseconds.
var PollIntervals = []int{250, 500, 1000}

// ErrCorrupt marks a settings file that exists but is not valid JSON.
var ErrCorrupt = errors.New("settings file is corrupt")

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
		MaxStorageMB:    1024,
		SkipSensitive:   true,
		PollMillis:      500,
		MoveToTopOnCopy: true,
	}
}

// Load reads path over the defaults. A missing file is not an
// error; a corrupt one (wrapping ErrCorrupt) returns the defaults and the error.
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
		return s, fmt.Errorf("%w: %s: %w", ErrCorrupt, path, err)
	}
	s.Normalize()
	return s, nil
}

// Quarantine renames path to path+".bad-"+now.Format("20060102-150405") and returns the new path.
func Quarantine(path string, now time.Time) (string, error) {
	bad := path + ".bad-" + now.Format("20060102-150405")
	if err := os.Rename(path, bad); err != nil {
		return "", err
	}
	return bad, nil
}

// Normalize clamps values to their ranges, drops ignore patterns that do not
// compile (logging each one) and normalizes the ignored app names.
func (s *Settings) Normalize() {
	s.MaxEntries = min(max(s.MaxEntries, 50), 5000)
	s.RetentionDays = min(max(s.RetentionDays, 0), 3650)
	s.MaxImageMB = min(max(s.MaxImageMB, 1), 50)
	s.MaxStorageMB = min(max(s.MaxStorageMB, 100), 20480)
	if !slices.Contains(PollIntervals, s.PollMillis) {
		s.PollMillis = 500
	}
	kept, _, bad := CompilePatterns(s.IgnorePatterns)
	for _, e := range bad {
		slog.Warn("ignore pattern dropped", "line", e.Line, "pattern", s.IgnorePatterns[e.Line-1], "err", e.Err)
	}
	s.IgnorePatterns = kept

	var apps []string
	for _, a := range s.IgnoreApps {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" && !slices.Contains(apps, a) {
			apps = append(apps, a)
		}
	}
	s.IgnoreApps = apps
}

// Limits are the history retention rules s sets.
func (s Settings) Limits() history.Limits {
	return history.Limits{MaxEntries: s.MaxEntries, Retention: time.Duration(s.RetentionDays) * 24 * time.Hour, MaxBytes: int64(s.MaxStorageMB) << 20}
}

// Save writes s to path atomically, with mode 0600.
func Save(path string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, b)
}

// CompilePatterns compiles each non-blank line. kept holds the lines that
// compile, in order; res their regexps; errs one *PatternError per bad line
// (Line is 1-based and counts blank lines).
func CompilePatterns(lines []string) (kept []string, res []*regexp.Regexp, errs []*PatternError) {
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		re, err := regexp.Compile(l)
		if err != nil {
			errs = append(errs, &PatternError{Line: i + 1, Err: err})
			continue
		}
		kept = append(kept, l)
		res = append(res, re)
	}
	return kept, res, errs
}
