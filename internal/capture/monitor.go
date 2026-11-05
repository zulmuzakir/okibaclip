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

	"okibaclip/internal/history"
	"okibaclip/internal/settings"
)

type monitorConfig struct {
	settings.Settings
	patterns []*regexp.Regexp
}

const (
	ignoreWindow    = 2 * time.Minute
	maxCaptureTries = 3
)

// Monitor polls the clipboard and records what it finds.
type Monitor struct {
	src         Source
	store       *history.Store
	settings    atomic.Pointer[monitorConfig]
	pausedUntil atomic.Int64 // unix ms; MaxInt64 until resumed; 0 not paused
	ignoreUntil atomic.Int64 // unix ms; 0 none
	suppress    struct {
		mu sync.Mutex
		m  map[string]time.Time // hash -> expiry
	}
	wake     chan struct{} // Resume nudges Run to re-baseline at once
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

	captureFails int    // consecutive failed attempts at the pending change
	lastHash     string // hash of the last capture stored or suppressed (Run goroutine only)
}

// New returns a Monitor that records src into store.
func New(src Source, store *history.Store, s settings.Settings, onChange func()) *Monitor {
	m := &Monitor{src: src, store: store, onChange: onChange, now: time.Now, wake: make(chan struct{}, 1)}
	m.suppress.m = map[string]time.Time{}
	if m.onChange == nil {
		m.onChange = func() {}
	}
	s.Normalize()
	_, pats, _ := settings.CompilePatterns(s.IgnorePatterns)
	m.settings.Store(&monitorConfig{Settings: s, patterns: pats})
	return m
}

// SetConfig installs new settings; it fails when an ignore pattern is bad.
func (m *Monitor) SetConfig(s settings.Settings) error {
	_, pats, errs := settings.CompilePatterns(s.IgnorePatterns)
	if len(errs) > 0 {
		return errs[0]
	}
	s.Normalize()
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
	select {
	case m.wake <- struct{}{}:
	default:
	}
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

func (m *Monitor) IgnoreNext() { m.ignoreUntil.Store(m.now().Add(ignoreWindow).UnixMilli()) }

// takeIgnore consumes the Ignore Next Copy request, reporting whether it is live.
func (m *Monitor) takeIgnore() bool {
	u := m.ignoreUntil.Swap(0)
	return u != 0 && m.now().UnixMilli() < u
}

// Suppress drops the next capture with this hash within 3 s; several may be pending.
func (m *Monitor) Suppress(hash string) {
	m.suppress.mu.Lock()
	m.suppress.m[hash] = m.now().Add(3 * time.Second)
	m.suppress.mu.Unlock()
}

// Unsuppress lifts a pending Suppress, e.g. after the write it guarded failed.
func (m *Monitor) Unsuppress(hash string) {
	m.suppress.mu.Lock()
	delete(m.suppress.m, hash)
	m.suppress.mu.Unlock()
}

func (m *Monitor) takeSuppressed(hash string) bool {
	m.suppress.mu.Lock()
	defer m.suppress.mu.Unlock()
	now := m.now()
	for h, until := range m.suppress.m {
		if !now.Before(until) {
			delete(m.suppress.m, h)
		}
	}
	if _, ok := m.suppress.m[hash]; ok {
		delete(m.suppress.m, hash)
		return true
	}
	return false
}

// Run polls until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.pruneLoop(ctx)
	}()
	defer wg.Wait()
	poll := m.cfg().PollMillis
	ticker := time.NewTicker(time.Duration(poll) * time.Millisecond)
	defer ticker.Stop()
	m.tick(ctx) // startup capture
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.wake:
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
	n, err := m.store.Prune(ctx, c.Settings.Limits(), m.now())
	if err != nil && ctx.Err() == nil {
		slog.Error("prune", "err", err)
	} else if n > 0 {
		if err := m.store.Compact(ctx); err != nil && ctx.Err() == nil {
			slog.Error("compact", "err", err)
		}
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
	for _, f := range []transfer.Format{transfer.FileList, transfer.URIList, transfer.Text, transfer.HTML} {
		if !hasFormat(formats, f) {
			continue
		}
		if p, err := m.src.ReadFormat(f); err == nil {
			sum := sha256.Sum256(p)
			b.WriteString(hex.EncodeToString(sum[:]))
		}
		return b.String()
	}
	if hasFormat(formats, transfer.PNG) {
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

// retryPending records one failed attempt at the pending change. After
// maxCaptureTries it gives up on the change by calling update.
func (m *Monitor) retryPending(update func()) {
	m.captureFails++
	if m.captureFails >= maxCaptureTries {
		slog.Warn("clipboard change abandoned", "fails", m.captureFails)
		update()
	}
}

// tick runs one poll.
func (m *Monitor) tick(ctx context.Context) {
	cfg := m.cfg()
	tok, haveTok := m.src.Token()
	paused, _ := m.Paused()
	if paused {
		m.wasPaused = true
		return
	}
	if m.wasPaused {
		m.wasPaused = false
		if p := m.pausedUntil.Load(); p != 0 && p != math.MaxInt64 {
			m.pausedUntil.CompareAndSwap(p, 0)
		}
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
		m.captureFails = 0
		if haveTok {
			m.lastToken, m.haveToken = tok, true
		} else {
			m.lastPrint, m.haveToken = print, false
		}
	}
	if len(formats) == 0 {
		m.retryPending(update)
		return
	}
	owner := m.src.Owner()
	if ignoredApp(owner, cfg.IgnoreApps) {
		slog.Info("capture skipped", "reason", "ignored app", "app", owner.App)
		m.takeIgnore()
		update()
		return
	}
	if cfg.SkipSensitive && IsSensitive(m.src.NativeFormats(), formats, m.src.ReadFormat) {
		slog.Info("capture skipped", "reason", "sensitive")
		m.takeIgnore()
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
		m.takeIgnore()
		update()
		return
	}
	data, err := m.src.Read(wanted...)
	if err != nil {
		if errors.Is(err, mygo.ErrClipboardChanged) {
			return // retry on the next tick
		}
		slog.Error("clipboard read", "err", err)
		m.retryPending(update)
		return
	}
	if haveTok {
		if t2, _ := m.src.Token(); t2 != tok {
			return // changed during the read: re-check sensitivity next tick
		}
	}
	c, reason, ok := Classify(data, wanted, cfg.Settings, cfg.patterns)
	if !ok {
		m.takeIgnore() // a user copy that was skipped anyway
		update()
		if reason != "" {
			slog.Info("capture skipped", "reason", reason)
		}
		return
	}
	c.SourceApp, c.SourceName, c.SourcePath = owner.App, owner.Name, owner.Path
	h := c.Hash()
	if m.takeSuppressed(h) {
		m.lastHash = h
		update()
		return
	}
	if h == m.lastHash {
		if ok, err := m.store.HasHash(ctx, h); err == nil && ok {
			update() // same content re-announced (watcher restart, delayed rendering): not a new copy
			return
		}
	}
	if m.takeIgnore() {
		update()
		return
	}
	var thumb []byte
	if c.Kind == history.KindImage {
		thumb, err = history.MakeThumb(c.Image)
		if err != nil {
			slog.Info("capture skipped", "reason", "thumbnail", "err", err)
			update()
			return
		}
	}
	if _, _, err := m.store.Capture(ctx, c, thumb, m.now()); err != nil {
		if ctx.Err() == nil {
			slog.Error("store capture", "err", err)
		}
		m.retryPending(update)
		return // not marked seen: the next tick retries
	}
	m.lastHash = h
	update()
	if _, err := m.store.Prune(ctx, cfg.Settings.Limits(), m.now()); err != nil && ctx.Err() == nil {
		slog.Error("prune", "err", err)
	}
	m.onChange()
}
