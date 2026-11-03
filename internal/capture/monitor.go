package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"my-clipboard/internal/history"
	"my-clipboard/internal/settings"
)

type monitorConfig struct {
	settings.Settings
	patterns []*regexp.Regexp
}

// Monitor polls the clipboard and records what it finds.
type Monitor struct {
	src         Source
	store       *history.Store
	settings    atomic.Pointer[monitorConfig]
	pausedUntil atomic.Int64 // unix ms; MaxInt64 until resumed; 0 not paused
	ignoreNext  atomic.Bool
	suppress    struct {
		mu    sync.Mutex
		hash  string
		until time.Time
	}
	onChange func()
	now      func() time.Time

	// baseline, touched only by the Run goroutine (or tests calling tick).
	lastToken uint64
	haveToken bool
	lastPrint string
	imgHash   string
	imgHashAt time.Time
	started   bool
	wasPaused bool
}

// New returns a Monitor that records src into store.
func New(src Source, store *history.Store, s settings.Settings, onChange func()) *Monitor {
	m := &Monitor{src: src, store: store, onChange: onChange, now: time.Now}
	if m.onChange == nil {
		m.onChange = func() {}
	}
	_ = m.SetConfig(s)
	return m
}

// SetConfig installs new settings; it fails when an ignore pattern is bad.
func (m *Monitor) SetConfig(s settings.Settings) error {
	s.Normalize()
	pats, err := settings.CompilePatterns(s.IgnorePatterns)
	if err != nil {
		return err
	}
	m.settings.Store(&monitorConfig{Settings: s, patterns: pats})
	return nil
}

func (m *Monitor) cfg() *monitorConfig { return m.settings.Load() }

// Pause stops capture for d; a negative d pauses until Resume.
func (m *Monitor) Pause(d time.Duration) {
	if d < 0 {
		m.pausedUntil.Store(math.MaxInt64)
	} else {
		m.pausedUntil.Store(m.now().Add(d).UnixMilli())
	}
	m.onChange()
}

func (m *Monitor) Resume() {
	m.pausedUntil.Store(0)
	m.onChange()
}

// Paused reports whether capture is paused, and until when (zero time for
// until resumed).
func (m *Monitor) Paused() (bool, time.Time) {
	p := m.pausedUntil.Load()
	if p == 0 || (p != math.MaxInt64 && m.now().UnixMilli() >= p) {
		return false, time.Time{}
	}
	if p == math.MaxInt64 {
		return true, time.Time{}
	}
	return true, time.UnixMilli(p)
}

func (m *Monitor) IgnoreNext() { m.ignoreNext.Store(true) }

// Suppress drops the next capture with this hash, within 3 s.
func (m *Monitor) Suppress(hash string) {
	m.suppress.mu.Lock()
	m.suppress.hash, m.suppress.until = hash, m.now().Add(3*time.Second)
	m.suppress.mu.Unlock()
}

func (m *Monitor) takeSuppressed(hash string) bool {
	m.suppress.mu.Lock()
	defer m.suppress.mu.Unlock()
	if m.suppress.hash == "" {
		return false
	}
	live := m.now().Before(m.suppress.until)
	match := live && m.suppress.hash == hash
	if match || !live {
		m.suppress.hash = ""
	}
	return match
}

// Run polls until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	go m.pruneLoop(ctx)
	poll := m.cfg().PollMillis
	ticker := time.NewTicker(time.Duration(poll) * time.Millisecond)
	defer ticker.Stop()
	m.tick(ctx) // startup capture
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if p := m.cfg().PollMillis; p != poll {
			poll = p
			ticker.Reset(time.Duration(p) * time.Millisecond)
		}
		m.tick(ctx)
	}
}

func (m *Monitor) pruneLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.prune(ctx)
		}
	}
}

func (m *Monitor) prune(ctx context.Context) {
	c := m.cfg()
	n, err := m.store.Prune(ctx, c.MaxEntries, time.Duration(c.RetentionDays)*24*time.Hour, m.now())
	if err != nil && ctx.Err() == nil {
		slog.Error("prune", "err", err)
	} else if n > 0 {
		m.onChange()
	}
}

// fingerprint is the change marker where the OS gives no token.
func (m *Monitor) fingerprint(formats []transfer.Format) string {
	var b strings.Builder
	for _, f := range formats {
		b.WriteString(string(f))
		b.WriteByte('\n')
	}
	if hasFormat(formats, transfer.Text) {
		if d, err := m.src.Read(transfer.Text); err == nil {
			t, _ := d.Read(transfer.Text)
			sum := sha256.Sum256(t)
			b.WriteString(hex.EncodeToString(sum[:]))
		}
	} else if hasFormat(formats, transfer.PNG) {
		if m.now().Sub(m.imgHashAt) >= time.Second || m.imgHash == "" {
			if d, err := m.src.Read(transfer.PNG); err == nil {
				p, _ := d.Read(transfer.PNG)
				sum := sha256.Sum256(p)
				m.imgHash = hex.EncodeToString(sum[:])
			}
			m.imgHashAt = m.now()
		}
		b.WriteString(m.imgHash)
	}
	return b.String()
}

// baseline records the clipboard's current state as seen.
func (m *Monitor) baseline(tok uint64, haveTok bool, formats []transfer.Format) {
	if haveTok {
		m.lastToken, m.haveToken = tok, true
		return
	}
	m.haveToken = false
	m.lastPrint = m.fingerprint(formats)
}

// tick runs one poll.
func (m *Monitor) tick(ctx context.Context) {
	cfg := m.cfg()
	tok, haveTok := m.src.Token()
	paused, _ := m.Paused()
	if paused {
		m.wasPaused = true
		m.baseline(tok, haveTok, m.src.Formats())
		return
	}
	if m.wasPaused {
		m.wasPaused = false
		m.pausedUntil.Store(0)
		m.baseline(tok, haveTok, m.src.Formats())
		m.onChange()
		return
	}
	var formats []transfer.Format
	var print string
	if haveTok {
		if m.started && m.haveToken && tok == m.lastToken {
			return
		}
		formats = m.src.Formats()
	} else {
		formats = m.src.Formats()
		print = m.fingerprint(formats)
		if m.started && !m.haveToken && print == m.lastPrint {
			return
		}
	}
	m.started = true
	// update marks the change as seen.
	update := func() {
		if haveTok {
			m.lastToken, m.haveToken = tok, true
		} else {
			m.lastPrint, m.haveToken = print, false
		}
	}
	if len(formats) == 0 {
		update()
		return
	}
	if cfg.SkipSensitive && IsSensitive(m.src.NativeFormats(), formats, m.src.ReadFormat) {
		slog.Info("capture skipped", "reason", "sensitive")
		update()
		return
	}
	if m.ignoreNext.Swap(false) {
		update()
		return
	}
	var wanted []transfer.Format
	for _, f := range []transfer.Format{transfer.FileList, transfer.URIList, transfer.Text, transfer.HTML, transfer.PNG} {
		if !hasFormat(formats, f) {
			continue
		}
		if (f == transfer.FileList || f == transfer.URIList) && !cfg.CaptureFiles {
			continue
		}
		if f == transfer.PNG && !cfg.CaptureImages {
			continue
		}
		wanted = append(wanted, f)
	}
	if len(wanted) == 0 {
		update()
		return
	}
	data, err := m.src.Read(wanted...)
	if err != nil {
		if errors.Is(err, mygo.ErrClipboardChanged) {
			return // retry on the next tick
		}
		slog.Error("clipboard read", "err", err)
		update()
		return
	}
	c, reason, ok := Classify(data, wanted, cfg.Settings, cfg.patterns)
	update()
	if !ok {
		if reason != "" {
			slog.Info("capture skipped", "reason", reason)
		}
		return
	}
	if m.takeSuppressed(c.Hash()) {
		return
	}
	var thumb []byte
	if c.Kind == history.KindImage {
		thumb, err = history.MakeThumb(c.Image)
		if err != nil {
			slog.Info("capture skipped", "reason", "thumbnail", "err", err)
			return
		}
	}
	if _, _, err := m.store.Capture(ctx, c, thumb, m.now()); err != nil {
		if ctx.Err() == nil {
			slog.Error("store capture", "err", err)
		}
		return
	}
	if _, err := m.store.Prune(ctx, cfg.MaxEntries, time.Duration(cfg.RetentionDays)*24*time.Hour, m.now()); err != nil && ctx.Err() == nil {
		slog.Error("prune", "err", err)
	}
	m.onChange()
}
