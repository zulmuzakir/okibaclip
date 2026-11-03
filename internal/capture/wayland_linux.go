//go:build linux

package capture

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/transfer"
)

// waylandSource reads the Wayland clipboard through wl-paste, which speaks
// the data-control protocol (KDE, Sway, Hyprland; not GNOME). A
// `wl-paste --watch` process prints a line on every selection change; those
// lines are the change token.
type waylandSource struct {
	bin string
	tok atomic.Uint64
}

// NewWaylandSource returns a Source backed by wl-paste, or an error when
// wl-paste is missing or the compositor has no data-control.
func NewWaylandSource(ctx context.Context) (Source, error) {
	bin, err := exec.LookPath("wl-paste")
	if err != nil {
		return nil, err
	}
	return newWaylandSource(ctx, bin)
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimSpace(l.b.String())
}

type watcher struct {
	line   <-chan struct{} // a line arrived
	done   <-chan error    // the process exited
	stderr *lockedBuf
}

func (w *waylandSource) spawn(ctx context.Context) (watcher, error) {
	cmd := exec.CommandContext(ctx, w.bin, "--watch", "echo")
	stderr := &lockedBuf{}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return watcher{}, err
	}
	if err := cmd.Start(); err != nil {
		return watcher{}, err
	}
	line, done := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			w.tok.Add(1)
			select {
			case line <- struct{}{}:
			default:
			}
		}
		done <- cmd.Wait()
	}()
	return watcher{line: line, done: done, stderr: stderr}, nil
}

func newWaylandSource(ctx context.Context, bin string) (*waylandSource, error) {
	w := &waylandSource{bin: bin}
	cur, err := w.spawn(ctx)
	if err != nil {
		return nil, err
	}
	select {
	case err := <-cur.done:
		msg := cur.stderr.String()
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return nil, fmt.Errorf("wl-paste --watch: %s", msg)
	case <-cur.line:
	case <-time.After(time.Second):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	go w.supervise(ctx, cur)
	return w, nil
}

// supervise restarts the watcher when it exits while ctx is live.
func (w *waylandSource) supervise(ctx context.Context, cur watcher) {
	for {
		select {
		case err := <-cur.done:
			if ctx.Err() != nil {
				return
			}
			slog.Warn("wl-paste exited", "err", err, "stderr", cur.stderr.String())
		case <-ctx.Done():
			return
		}
		for {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
			next, err := w.spawn(ctx)
			if err != nil {
				slog.Warn("wl-paste restart", "err", err)
				continue
			}
			cur = next
			w.tok.Add(1)
			break
		}
	}
}

func (w *waylandSource) Token() (uint64, bool) { return w.tok.Load(), true }

func (w *waylandSource) Owner() Owner { return Owner{} }

// NativeFormats lists the MIME types the selection offers.
func (w *waylandSource) NativeFormats() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, w.bin, "--list-types").Output()
	if err != nil {
		return nil
	}
	var types []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			types = append(types, l)
		}
	}
	return types
}

// textMimes are the offered types read as text, best first.
var textMimes = []string{"text/plain;charset=utf-8", "text/plain", "UTF8_STRING", "STRING", "TEXT"}

func (w *waylandSource) Formats() []transfer.Format {
	return formatsOf(w.NativeFormats())
}

func formatsOf(types []string) []transfer.Format {
	has := func(m ...string) bool {
		for _, t := range types {
			for _, x := range m {
				if t == x {
					return true
				}
			}
		}
		return false
	}
	var out []transfer.Format
	if has("text/uri-list") {
		out = append(out, transfer.URIList)
	}
	if has(textMimes...) {
		out = append(out, transfer.Text)
	}
	if has("text/html") {
		out = append(out, transfer.HTML)
	}
	if has("image/png") {
		out = append(out, transfer.PNG)
	}
	return out
}

func mimeFor(f transfer.Format, types []string) (string, bool) {
	candidates := []string{string(f)}
	if f == transfer.Text {
		candidates = textMimes
	}
	for _, c := range candidates {
		for _, t := range types {
			if t == c {
				return c, true
			}
		}
	}
	return "", false
}

func (w *waylandSource) read(f transfer.Format, types []string) ([]byte, error) {
	mime, ok := mimeFor(f, types)
	if !ok {
		return nil, transfer.ErrFormat
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.bin, "--no-newline", "--type", mime)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	b, rerr := io.ReadAll(io.LimitReader(out, 64<<20))
	io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil && rerr == nil {
		rerr = err
	}
	if rerr != nil {
		return nil, rerr
	}
	return b, nil
}

func (w *waylandSource) ReadFormat(f transfer.Format) ([]byte, error) {
	return w.read(f, w.NativeFormats())
}

func (w *waylandSource) Read(formats ...transfer.Format) (transfer.Data, error) {
	types := w.NativeFormats()
	var reps []transfer.Representation
	for _, f := range formats {
		if b, err := w.read(f, types); err == nil {
			reps = append(reps, transfer.Bytes(f, b))
		}
	}
	if len(reps) == 0 {
		return transfer.Data{}, transfer.ErrFormat
	}
	return transfer.New(transfer.NewItem(reps...)), nil
}
