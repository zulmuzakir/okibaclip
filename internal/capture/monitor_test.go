package capture

import (
	"path/filepath"
	"slices"

	"context"
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
	mu       sync.Mutex
	useToken bool
	token    uint64
	formats  []transfer.Format
	native   []string
	data     transfer.Data
	readErrs []error
	reads    int
	owner    Owner
}

func (f *fakeClip) Token() (uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token, f.useToken
}
func (f *fakeClip) Formats() []transfer.Format { f.mu.Lock(); defer f.mu.Unlock(); return f.formats }
func (f *fakeClip) NativeFormats() []string    { f.mu.Lock(); defer f.mu.Unlock(); return f.native }
func (f *fakeClip) Owner() Owner               { f.mu.Lock(); defer f.mu.Unlock(); return f.owner }
func (f *fakeClip) ReadFormat(format transfer.Format) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data.Read(format)
}
func (f *fakeClip) Read(...transfer.Format) (transfer.Data, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
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

func textHash(s string) string { return history.Capture{Kind: history.KindText, Text: s}.Hash() }

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
	f.clip.owner = Owner{App: "com.tinyspeck.slackmacgap", Name: "Slack", Path: "/Applications/Slack.app"}
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
	f.clip.owner = Owner{App: "com.tinyspeck.slackmacgap", Name: "Slack"}
	f.clip.setText("from slack")
	f.tick(t)
	if l := f.list(t); len(l) != 0 {
		t.Errorf("recorded %v", l)
	}
	f.clip.owner = Owner{App: "org.mozilla.firefox", Name: "Firefox"}
	f.clip.setText("from firefox")
	f.tick(t)
	if l := f.list(t); !slices.Equal(l, []string{"from firefox"}) {
		t.Errorf("list = %v", l)
	}
}
