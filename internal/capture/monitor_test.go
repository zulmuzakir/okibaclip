package capture

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"context"
	"errors"
	"fmt"
	"okibaclip/internal/history"
	"okibaclip/internal/history/historytest"
	"okibaclip/internal/settings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

type fakeClip struct {
	mu          sync.Mutex
	useToken    bool
	token       uint64
	formats     []transfer.Format
	native      []string
	data        transfer.Data
	readErrs    []error
	reads       int
	formatReads int // Formats and NativeFormats calls
	owners      []Owner
	onRead      func() // runs in Read with mu released, before data is returned
}

func (f *fakeClip) Token() (uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token, f.useToken
}
func (f *fakeClip) Formats() []transfer.Format {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.formatReads++
	return f.formats
}
func (f *fakeClip) NativeFormats() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.formatReads++
	return f.native
}
func (f *fakeClip) Owners() []Owner { f.mu.Lock(); defer f.mu.Unlock(); return f.owners }
func (f *fakeClip) ReadFormat(format transfer.Format) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data.Read(format)
}
func (f *fakeClip) Read(...transfer.Format) (transfer.Data, error) {
	f.mu.Lock()
	f.reads++
	hook := f.onRead
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.readErrs) > 0 {
		err := f.readErrs[0]
		f.readErrs = f.readErrs[1:]
		if err != nil {
			return transfer.Data{}, err
		}
	}
	return f.data, nil
}
func (f *fakeClip) setText(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token++
	f.data = transfer.TextData(s)
	f.formats = f.data.Formats()
}
func (f *fakeClip) setFiles(paths ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token++
	f.data, _ = transfer.FileData(paths...)
	f.formats = f.data.Formats()
}

func (f *fakeClip) bump() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token++
}

type monFixture struct {
	m     *Monitor
	clip  *fakeClip
	store *history.Store
	now   time.Time
	ctx   context.Context
	n     int // onChange calls
}

func newMonFixture(t *testing.T, useToken bool) *monFixture {
	f := &monFixture{clip: &fakeClip{useToken: useToken}, store: historytest.NewStore(t), now: historytest.T0, ctx: context.Background()}
	f.m = New(f.clip, f.store, settings.Default(), func() { f.n++ })
	f.m.now = func() time.Time { return f.now }
	return f
}

func (f *monFixture) tick(t *testing.T) {
	t.Helper()
	f.now = f.now.Add(time.Second)
	f.m.tick(f.ctx)
}

func (f *monFixture) list(t *testing.T) []string {
	t.Helper()
	l, err := f.store.List(f.ctx, history.Filter{Tab: "all"})
	if err != nil {
		t.Fatal(err)
	}
	return historytest.Texts(l)
}

func TestMonitorTokenUnchangedDoesNotRead(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("a")
	f.tick(t)
	reads := f.clip.reads
	f.tick(t)
	f.tick(t)
	if f.clip.reads != reads {
		t.Errorf("read again with the same token: %d -> %d", reads, f.clip.reads)
	}
	if !slices.Equal(f.list(t), []string{"a"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorChangeCapturesOnce(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("a")
	f.tick(t)
	f.clip.setText("b")
	f.tick(t)
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"b", "a"}) {
		t.Errorf("list = %v", f.list(t))
	}
	if f.n != 2 {
		t.Errorf("onChange called %d times", f.n)
	}
}

func TestMonitorRetriesAfterClipboardChanged(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("a")
	f.clip.readErrs = []error{mygo.ErrClipboardChanged}
	f.tick(t)
	if len(f.list(t)) != 0 {
		t.Fatal("captured despite the changed error")
	}
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"a"}) {
		t.Errorf("not retried: %v", f.list(t))
	}
}

func TestMonitorSkipsSensitive(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("hunter2")
	f.clip.native = []string{"org.nspasteboard.ConcealedType"}
	f.tick(t)
	f.clip.native = nil
	f.tick(t) // same token: still not read
	if len(f.list(t)) != 0 {
		t.Errorf("recorded a concealed copy: %v", f.list(t))
	}
	s := settings.Default()
	s.SkipSensitive = false
	f.m.SetConfig(s)
	f.clip.native = []string{"org.nspasteboard.ConcealedType"}
	f.clip.setText("hunter3")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"hunter3"}) {
		t.Errorf("with the filter off: %v", f.list(t))
	}
}

func TestMonitorIgnoreNextSkipsExactlyOne(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.IgnoreNext()
	f.clip.setText("a")
	f.tick(t)
	f.clip.setText("b")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"b"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorSuppressDropsMatchOnce(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.Suppress(history.Capture{Kind: history.KindText, Text: "x"}.Hash())
	f.clip.setText("x")
	f.tick(t)
	if len(f.list(t)) != 0 {
		t.Fatal("suppressed capture recorded")
	}
	f.clip.setText("x")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"x"}) {
		t.Errorf("suppression should be one-shot: %v", f.list(t))
	}
}

func TestMonitorSuppressExpires(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.Suppress(history.Capture{Kind: history.KindText, Text: "x"}.Hash())
	f.now = f.now.Add(10 * time.Second)
	f.clip.setText("x")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"x"}) {
		t.Errorf("expired suppression still applied: %v", f.list(t))
	}
}

func TestMonitorPauseAndResume(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.Pause(15 * time.Minute)
	f.clip.setText("while paused")
	f.tick(t)
	if len(f.list(t)) != 0 {
		t.Fatal("captured while paused")
	}
	f.now = f.now.Add(16 * time.Minute)
	f.tick(t) // pause expires: baseline only
	if p, _ := f.m.Paused(); p {
		t.Error("still paused after expiry")
	}
	if len(f.list(t)) != 0 {
		t.Errorf("resume captured the paused content: %v", f.list(t))
	}
	f.clip.setText("after")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"after"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorExplicitResumeDoesNotCapturePausedContent(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.Pause(-1)
	f.clip.setText("secret-ish")
	f.tick(t)
	f.m.Resume()
	f.tick(t)
	if len(f.list(t)) != 0 {
		t.Errorf("captured %v", f.list(t))
	}
	f.clip.setText("next")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"next"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorFingerprintDetectsTextToText(t *testing.T) {
	f := newMonFixture(t, false)
	f.clip.setText("first")
	f.tick(t)
	f.tick(t)
	f.clip.setText("second")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"second", "first"}) {
		t.Errorf("list = %v", f.list(t))
	}
	reads := f.clip.reads
	f.tick(t)
	f.clip.mu.Lock()
	changed := f.clip.reads - reads
	f.clip.mu.Unlock()
	if len(f.list(t)) != 2 || changed > 1 {
		t.Errorf("unchanged clipboard recorded again or reread (%d reads)", changed)
	}
}

func TestMonitorIgnorePatternSkips(t *testing.T) {
	f := newMonFixture(t, true)
	s := settings.Default()
	s.IgnorePatterns = []string{`^\d{4}$`}
	if err := f.m.SetConfig(s); err != nil {
		t.Fatal(err)
	}
	f.clip.setText("1234")
	f.tick(t)
	f.clip.setText("12345")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"12345"}) {
		t.Errorf("list = %v", f.list(t))
	}
	s.IgnorePatterns = []string{"("}
	if err := f.m.SetConfig(s); err == nil {
		t.Error("bad pattern accepted")
	}
}

func textHash(s string) string { return history.Capture{Kind: history.TextKind(s), Text: s}.Hash() }

func TestMonitorFingerprintDetectsFileListChange(t *testing.T) {
	f := newMonFixture(t, false)
	dir := t.TempDir()
	f.clip.setFiles(filepath.Join(dir, "a"))
	f.tick(t)
	f.clip.setFiles(filepath.Join(dir, "b"))
	f.tick(t)
	if n := len(f.list(t)); n != 2 {
		t.Errorf("entries = %d, want 2", n)
	}
}

func TestMonitorIgnoreNextNotSpentOnOwnWrite(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.IgnoreNext()
	f.m.Suppress(textHash("x"))
	f.clip.setText("x")
	f.tick(t)
	f.clip.setText("y")
	f.tick(t)
	f.clip.setText("z")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"z"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorIgnoreNextExpires(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.IgnoreNext()
	f.now = f.now.Add(3 * time.Minute)
	f.clip.setText("a")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"a"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorSuppressHoldsSeveralHashes(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.Suppress(textHash("x"))
	f.m.Suppress(textHash("y"))
	f.clip.setText("x")
	f.tick(t)
	f.clip.setText("y")
	f.tick(t)
	if l := f.list(t); len(l) != 0 {
		t.Errorf("list = %v", l)
	}
}

func TestMonitorUnsuppressRecordsCopy(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.Suppress(textHash("x"))
	f.m.Unsuppress(textHash("x"))
	f.clip.setText("x")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"x"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorResumeWakesPoll(t *testing.T) {
	f := newMonFixture(t, true)
	f.m.Pause(-1)
	f.m.Resume()
	select {
	case <-f.m.wake:
	default:
		t.Error("Resume did not wake the poll loop")
	}
}

func TestMonitorRetriesAfterStoreError(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("a")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	f.ctx = canceled
	f.tick(t)
	f.ctx = context.Background()
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"a"}) {
		t.Errorf("not retried: %v", f.list(t))
	}
}

func TestMonitorRecordsSourceApp(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.owners = []Owner{Owner{App: "com.tinyspeck.slackmacgap", Name: "Slack", Path: "/Applications/Slack.app"}}
	f.clip.setText("hello")
	f.tick(t)
	l, err := f.store.List(f.ctx, history.Filter{Tab: "all"})
	if err != nil || len(l) != 1 {
		t.Fatalf("list = %v, %v", l, err)
	}
	if l[0].SourceApp != "com.tinyspeck.slackmacgap" || l[0].SourceName != "Slack" {
		t.Errorf("source = %q / %q", l[0].SourceApp, l[0].SourceName)
	}
	missing, _ := f.store.AppIconsMissing(f.ctx)
	if len(missing) != 1 || missing[0].Path != "/Applications/Slack.app" {
		t.Errorf("icons to fetch = %+v", missing)
	}
}

func TestMonitorSkipsIgnoredApp(t *testing.T) {
	f := newMonFixture(t, true)
	s := settings.Default()
	s.IgnoreApps = []string{"slack"}
	if err := f.m.SetConfig(s); err != nil {
		t.Fatal(err)
	}
	f.clip.owners = []Owner{Owner{App: "com.tinyspeck.slackmacgap", Name: "Slack"}}
	f.clip.setText("from slack")
	f.tick(t)
	if l := f.list(t); len(l) != 0 {
		t.Errorf("recorded %v", l)
	}
	f.clip.owners = []Owner{Owner{App: "org.mozilla.firefox", Name: "Firefox"}}
	f.clip.setText("from firefox")
	f.tick(t)
	if l := f.list(t); !slices.Equal(l, []string{"from firefox"}) {
		t.Errorf("list = %v", l)
	}
}

func TestMonitorSkipsWhenAnyOwnerIgnored(t *testing.T) {
	f := newMonFixture(t, true)
	s := settings.Default()
	s.IgnoreApps = []string{"com.agilebits.onepassword7"}
	if err := f.m.SetConfig(s); err != nil {
		t.Fatal(err)
	}
	f.clip.owners = []Owner{{App: "org.mozilla.firefox"}, {App: "com.agilebits.onepassword7"}}
	f.clip.setText("secret")
	f.tick(t)
	if l := f.list(t); len(l) != 0 {
		t.Errorf("recorded %v", l)
	}
	f.clip.owners = []Owner{{App: "org.mozilla.firefox", Name: "Firefox"}}
	f.clip.setText("fine")
	f.tick(t)
	l, err := f.store.List(f.ctx, history.Filter{Tab: "all"})
	if err != nil || len(l) != 1 || l[0].SourceApp != "org.mozilla.firefox" {
		t.Errorf("list = %v, %v", l, err)
	}
}

func TestMonitorNewSurvivesInvalidIgnorePattern(t *testing.T) {
	f := newMonFixture(t, true)
	s := settings.Default()
	s.IgnorePatterns = []string{"(", `^\d{4}$`}
	f.m = New(f.clip, f.store, s, func() { f.n++ })
	f.m.now = func() time.Time { return f.now }
	f.clip.setText("1234")
	f.tick(t)
	if n := len(f.list(t)); n != 0 {
		t.Fatalf("ignored text captured: %v", f.list(t))
	}
	f.clip.setText("12345")
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"12345"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorDropsReadThatRacedAClipboardChange(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("public")
	f.clip.onRead = func() {
		f.clip.onRead = nil // once: the change happens during the first read only
		f.clip.setText("secret")
		f.clip.native = []string{"org.nspasteboard.ConcealedType"}
	}
	f.tick(t)
	f.tick(t)
	if n := len(f.list(t)); n != 0 {
		t.Errorf("entries = %v, want none", f.list(t))
	}
}

func TestMonitorBusyClipboardRetriesBeforeMarkingSeen(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("hi")
	f.clip.formats = nil // busy: no formats offered yet
	f.tick(t)
	f.tick(t)
	f.clip.formats = f.clip.data.Formats()
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"hi"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorBusyClipboardAbandonsAfterRetries(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("hi")
	f.clip.formats = nil
	for range 3 {
		f.tick(t)
	}
	f.clip.formats = f.clip.data.Formats() // back, but the change was already given up on
	f.tick(t)
	if n := len(f.list(t)); n != 0 {
		t.Errorf("abandoned change captured: %v", f.list(t))
	}
}

func TestMonitorReadErrorRetries(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("hi")
	f.clip.readErrs = []error{errors.New("wl-paste: busy")}
	f.tick(t)
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"hi"}) {
		t.Errorf("list = %v", f.list(t))
	}
}

func TestMonitorPausedTicksDoNotRead(t *testing.T) {
	f := newMonFixture(t, false)
	f.m.Pause(time.Hour)
	f.clip.setText("hi")
	for range 3 {
		f.tick(t)
	}
	if f.clip.reads != 0 || f.clip.formatReads != 0 {
		t.Errorf("reads = %d, formatReads = %d; want 0, 0", f.clip.reads, f.clip.formatReads)
	}
}

func TestMonitorSkipsReannouncedContent(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.setText("a")
	f.tick(t)
	f.clip.bump() // same content, new token: a re-announce, not a new copy
	f.tick(t)
	l, err := f.store.List(f.ctx, history.Filter{Tab: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != 1 || l[0].UseCount != 1 {
		t.Fatalf("entries = %d, use count = %v; want 1 entry used once", len(l), l)
	}
	if _, _, err := f.store.Delete(f.ctx, l[0].ID); err != nil {
		t.Fatal(err)
	}
	f.clip.bump()
	f.tick(t)
	if !slices.Equal(f.list(t), []string{"a"}) {
		t.Errorf("deleted content not recaptured: %v", f.list(t))
	}
}

func TestMonitorRecordsInvalidUTF8Text(t *testing.T) {
	f := newMonFixture(t, true)
	f.clip.owners = []Owner{{App: "caf\xe9", Name: "Caf\xe9"}}
	f.clip.setText("caf\xe9 latin1")
	f.tick(t)
	l, err := f.store.List(f.ctx, history.Filter{Tab: "all"})
	if err != nil || len(l) != 1 {
		t.Fatalf("list = %v, %v", l, err)
	}
	if got := historytest.Texts(l); !slices.Equal(got, []string{"caf\uFFFD latin1"}) {
		t.Errorf("texts = %q", got)
	}
	if l[0].SourceApp != "caf\uFFFD" || l[0].SourceName != "Caf\uFFFD" {
		t.Errorf("source = %q / %q", l[0].SourceApp, l[0].SourceName)
	}
}

func TestMonitorPruneCallsOnPrune(t *testing.T) {
	f := newMonFixture(t, true)
	s := settings.Default()
	s.MaxEntries = 50
	if err := f.m.SetConfig(s); err != nil {
		t.Fatal(err)
	}
	pruned := 0
	f.m.OnPrune = func() { pruned++ }
	for i := range 50 {
		f.clip.setText(fmt.Sprintf("entry %d", i))
		f.tick(t)
	}
	if pruned != 0 {
		t.Fatalf("OnPrune fired %d times within the limit", pruned)
	}
	f.clip.setText("one too many")
	f.tick(t)
	if pruned != 1 {
		t.Errorf("OnPrune fired %d times after exceeding the limit; want 1", pruned)
	}
}

func TestMonitorAbandonLogLevel(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	f := newMonFixture(t, true)
	f.clip.setText("hi")
	f.clip.formats = nil
	for range 3 {
		f.tick(t)
	}
	if out := buf.String(); strings.Contains(out, "level=WARN") || !strings.Contains(out, "clipboard empty") {
		t.Errorf("empty clipboard abandon logged %q; want a debug line and no warning", out)
	}

	buf.Reset()
	f.clip.setText("again")
	boom := errors.New("boom")
	f.clip.readErrs = []error{boom, boom, boom}
	for range 3 {
		f.tick(t)
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "clipboard change abandoned") {
		t.Errorf("read failures logged %q; want the abandon warning", out)
	}
}
