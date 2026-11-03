package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateDataDir(t *testing.T) {
	write := func(t *testing.T, path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return string(b)
	}

	t.Run("moves the former folder", func(t *testing.T) {
		base := t.TempDir()
		write(t, filepath.Join(base, "old", "history.db"), "old")
		if err := migrateDataDir(base, "old", "new"); err != nil {
			t.Fatal(err)
		}
		if got := read(filepath.Join(base, "new", "history.db")); got != "old" {
			t.Errorf("new history = %q", got)
		}
		if _, err := os.Stat(filepath.Join(base, "old")); !os.IsNotExist(err) {
			t.Errorf("former folder remains: %v", err)
		}
	})

	t.Run("keeps an existing new folder", func(t *testing.T) {
		base := t.TempDir()
		write(t, filepath.Join(base, "old", "history.db"), "old")
		write(t, filepath.Join(base, "new", "history.db"), "new")
		if err := migrateDataDir(base, "old", "new"); err != nil {
			t.Fatal(err)
		}
		if got := read(filepath.Join(base, "new", "history.db")); got != "new" {
			t.Errorf("new history overwritten: %q", got)
		}
		if got := read(filepath.Join(base, "old", "history.db")); got != "old" {
			t.Errorf("former history lost: %q", got)
		}
	})

	t.Run("nothing to migrate", func(t *testing.T) {
		base := t.TempDir()
		if err := migrateDataDir(base, "old", "new"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(base, "new")); !os.IsNotExist(err) {
			t.Errorf("created new folder: %v", err)
		}
	})
}
