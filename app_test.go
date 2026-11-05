package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

func TestRecoverHistoryStartsEmpty(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "history.db")
	write := func() {
		os.WriteFile(path, []byte("garbage garbage garbage"), 0o600)
		os.WriteFile(path+"-journal", []byte("journal"), 0o600)
	}
	write()
	button := 0
	a := &app{messageDialog: func(mygo.MessageOptions) (mygo.MessageResult, error) {
		return mygo.MessageResult{Button: button}, nil
	}}
	s, err := a.recoverHistory(ctx, path, errors.New("x"))
	if err != nil || s == nil {
		t.Fatalf("recoverHistory = %v, %v", s, err)
	}
	defer s.Close()
	if n, _, _, err := s.Counts(ctx); err != nil || n != 0 {
		t.Errorf("count = %d, %v", n, err)
	}
	if m, _ := filepath.Glob(path + ".bad-*"); len(m) != 2 {
		t.Errorf("quarantined files = %v", m)
	}
	if m, _ := filepath.Glob(path + ".bad-*-journal"); len(m) != 1 {
		t.Errorf("quarantined journals = %v", m)
	}

	s.Close()
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o700)
	write()
	button = 1
	s, err = a.recoverHistory(ctx, path, errors.New("x"))
	if s != nil || err != nil {
		t.Fatalf("quit = %v, %v", s, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "garbage garbage garbage" {
		t.Errorf("file touched: %q", b)
	}
}
