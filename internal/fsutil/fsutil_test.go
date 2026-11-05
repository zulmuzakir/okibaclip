package fsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWriteFileAtomicFuncFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.json")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteFileAtomicFunc(p, func(w io.Writer) error {
		w.Write([]byte("partial"))
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("want error")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "old" {
		t.Fatalf("file = %q, want old", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %d entries", len(entries))
	}
}

func TestWriteFileAtomicFuncStreams(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.json")
	if err := WriteFileAtomicFunc(p, func(w io.Writer) error {
		_, err := io.WriteString(w, "hello")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "hello" {
		t.Fatalf("file = %q", got)
	}
}

func TestWriteFileAtomicReplacesWithMode0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "new" {
		t.Fatalf("contents = %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", st.Mode().Perm())
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}

func TestQuarantineMovesCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{nope`), 0o600)
	now := time.Date(2026, 11, 5, 14, 30, 12, 0, time.UTC)
	got, err := Quarantine(p, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := p + ".bad-20261105-143012"; got != want {
		t.Errorf("Quarantine = %q; want %q", got, want)
	}
	b, err := os.ReadFile(got)
	if err != nil || string(b) != `{nope` {
		t.Errorf("quarantined file = %q, %v", b, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("original still present: %v", err)
	}
}
