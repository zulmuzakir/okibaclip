package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsMissingFileGivesDefaults(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.MaxEntries != 500 || !s.CaptureImages || !s.SkipSensitive || s.PollMillis != 500 || !s.MoveToTopOnCopy {
		t.Errorf("unexpected defaults: %+v", s)
	}
}

func TestLoadSettingsPartialKeepsDefaultBools(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"maxEntries": 100}`), 0o600)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.MaxEntries != 100 || !s.CaptureFiles || !s.SkipSensitive || !s.MoveToTopOnCopy {
		t.Errorf("absent bools must keep defaults: %+v", s)
	}
}

func TestLoadSettingsCorruptReturnsDefaultsAndError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{nope`), 0o600)
	s, err := Load(p)
	if err == nil {
		t.Fatal("want error")
	}
	if s.MaxEntries != 500 {
		t.Errorf("want defaults, got %+v", s)
	}
}

func TestNormalizeClamps(t *testing.T) {
	s := Settings{MaxEntries: 1, RetentionDays: 99999, MaxImageMB: 0, PollMillis: 333, IgnorePatterns: []string{"a", " ", "", "b"}}
	s.Normalize()
	if s.MaxEntries != 50 || s.RetentionDays != 3650 || s.MaxImageMB != 1 || s.PollMillis != 500 {
		t.Errorf("clamp: %+v", s)
	}
	if len(s.IgnorePatterns) != 2 {
		t.Errorf("blank patterns kept: %q", s.IgnorePatterns)
	}
	s.MaxEntries = 99999
	s.Normalize()
	if s.MaxEntries != 5000 {
		t.Errorf("max clamp: %d", s.MaxEntries)
	}
}

func TestSaveSettingsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	s := Default()
	s.Shortcut = "Ctrl+Alt+K"
	s.IgnorePatterns = []string{`^\d+$`}
	if err := Save(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.Shortcut != "Ctrl+Alt+K" || len(got.IgnorePatterns) != 1 {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("temp file left behind")
	}
}

func TestCompilePatternsNamesBadLine(t *testing.T) {
	_, err := CompilePatterns([]string{`ok`, ``, `(unclosed`})
	var pe *PatternError
	if !errors.As(err, &pe) || pe.Line != 3 {
		t.Errorf("err = %v", err)
	}
}
