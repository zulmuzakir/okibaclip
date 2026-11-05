package settings

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	kept, res, errs := CompilePatterns([]string{`ok`, ``, `(unclosed`})
	if len(errs) != 1 || errs[0].Line != 3 {
		t.Fatalf("errs = %v", errs)
	}
	if !reflect.DeepEqual(kept, []string{"ok"}) || len(res) != 1 {
		t.Errorf("kept = %q, %d regexps", kept, len(res))
	}
}

func TestNormalizeDropsInvalidIgnorePattern(t *testing.T) {
	s := Default()
	s.IgnorePatterns = []string{"(", "ok"}
	s.Normalize()
	if !reflect.DeepEqual(s.IgnorePatterns, []string{"ok"}) {
		t.Errorf("IgnorePatterns = %q", s.IgnorePatterns)
	}
}

func TestLoadCorruptIsErrCorrupt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{nope`), 0o600)
	if _, err := Load(p); !errors.Is(err, ErrCorrupt) {
		t.Errorf("err = %v; want ErrCorrupt", err)
	}
}

func TestNormalizeIgnoreAppsAndStorage(t *testing.T) {
	s := Default()
	s.IgnoreApps = []string{" KeePass.exe ", "", "keepass.exe", "Com.Agilebits.OnePassword7"}
	s.MaxStorageMB = 5
	s.Normalize()
	if !reflect.DeepEqual(s.IgnoreApps, []string{"keepass.exe", "com.agilebits.onepassword7"}) {
		t.Errorf("IgnoreApps = %q", s.IgnoreApps)
	}
	if s.MaxStorageMB != 100 {
		t.Errorf("MaxStorageMB = %d", s.MaxStorageMB)
	}
	s.MaxStorageMB = 1 << 20
	s.Normalize()
	if s.MaxStorageMB != 20480 {
		t.Errorf("MaxStorageMB = %d", s.MaxStorageMB)
	}
}

func TestLoadWithoutStorageKeepsDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(`{"maxEntries": 100}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil || s.MaxStorageMB != 1024 || s.MaxEntries != 100 {
		t.Errorf("got %+v, %v", s, err)
	}
	if l := s.Limits(); l.MaxBytes != 1024<<20 || l.MaxEntries != 100 {
		t.Errorf("limits = %+v", l)
	}
}
