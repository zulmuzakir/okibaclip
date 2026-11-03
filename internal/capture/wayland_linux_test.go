//go:build linux

package capture

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
)

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wl-paste")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWaylandSourceReadsThroughWlPaste(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "text"), []byte("hello wayland"), 0o644)
	os.WriteFile(filepath.Join(dir, "html"), []byte("<b>hi</b>"), 0o644)
	script := writeScript(t, `case "$1" in
--watch) echo; exec sleep 30;;
--list-types) printf 'text/plain;charset=utf-8\ntext/html\n';;
--no-newline) case "$3" in text/html) cat `+dir+`/html;; *) cat `+dir+`/text;; esac;;
esac
`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := newWaylandSource(ctx, script)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if tok, ok := w.Token(); ok && tok >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no change token")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := w.Formats(); !slices.Equal(got, []transfer.Format{transfer.Text, transfer.HTML}) {
		t.Errorf("Formats = %v", got)
	}
	b, err := w.ReadFormat(transfer.Text)
	if err != nil || string(b) != "hello wayland" {
		t.Errorf("text = %q, %v", b, err)
	}
	d, err := w.Read(transfer.Text, transfer.HTML, transfer.PNG)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := d.Read(transfer.HTML); string(h) != "<b>hi</b>" {
		t.Errorf("html = %q", h)
	}
	if _, err := w.ReadFormat(transfer.PNG); err == nil {
		t.Error("read a type the selection doesn't offer")
	}
}

func TestWaylandSourceFailsWithoutDataControl(t *testing.T) {
	script := writeScript(t, "echo 'Compositor requires data-control' >&2\nexit 1\n")
	if _, err := newWaylandSource(context.Background(), script); err == nil || !strings.Contains(err.Error(), "requires data-control") {
		t.Fatalf("err = %v", err)
	}
}
