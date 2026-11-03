package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"okibaclip/internal/history"
	"okibaclip/internal/history/historytest"
	"okibaclip/internal/settings"
)

type panelFixture struct {
	a      *app
	tt     *ui.Tester
	writes []transfer.Data
	mon    *fakeMon
}

// fakeMon records what the app asks of the capture monitor.
type fakeMon struct {
	suppressed   []string
	unsuppressed []string
	paused       bool
	resumes      int
}

func (m *fakeMon) Pause(time.Duration)               { m.paused = true }
func (m *fakeMon) Resume()                           { m.paused = false; m.resumes++ }
func (m *fakeMon) Paused() (bool, time.Time)         { return m.paused, time.Time{} }
func (m *fakeMon) IgnoreNext()                       {}
func (m *fakeMon) Suppress(h string)                 { m.suppressed = append(m.suppressed, h) }
func (m *fakeMon) Unsuppress(h string)               { m.unsuppressed = append(m.unsuppressed, h) }
func (m *fakeMon) SetConfig(settings.Settings) error { return nil }

func newPanelFixture(t *testing.T, items ...string) *panelFixture {
	t.Helper()
	f := &panelFixture{}
	a := newApp()
	a.store = historytest.NewStore(t)
	a.async = func(fn func()) { fn() }
	a.post = func(fn func()) { fn() }
	a.writeClipboard = func(d transfer.Data) error { f.writes = append(f.writes, d); return nil }
	f.mon = &fakeMon{}
	a.mon = f.mon
	f.a = a
	for i, s := range items {
		historytest.Add(t, a.store, history.Capture{Kind: history.KindText, Text: s}, historytest.T0.Add(time.Duration(i)*time.Second))
	}
	a.reload()
	f.tt = ui.NewTester(a.view, 720, 480)
	f.tt.Frame()
	return f
}

func (f *panelFixture) written(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, d := range f.writes {
		b, err := d.Read(transfer.Text)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}

func TestPanelTypingFiltersRows(t *testing.T) {
	f := newPanelFixture(t, "apple pie", "zebra stripes", "apple tart")
	if !f.tt.HasText("zebra stripes") || !f.tt.HasText("apple pie") {
		t.Fatalf("rows missing: %q", f.tt.Texts())
	}
	f.a.search.Focus()
	f.tt.Frame()
	f.tt.Type("ZEB")
	if !f.tt.HasText("zebra stripes") || f.tt.HasText("apple pie") {
		t.Errorf("filter: %q", f.tt.Texts())
	}
	f.tt.Type("zzz")
	if !f.tt.HasText("No matches") {
		t.Errorf("empty state: %q", f.tt.Texts())
	}
}

func TestPanelEmptyState(t *testing.T) {
	f := newPanelFixture(t)
	if !f.tt.HasText("Nothing copied yet") || !f.tt.HasText("Copy something and it will appear here.") {
		t.Errorf("texts: %q", f.tt.Texts())
	}
	if f.tt.HasText("result") {
		t.Errorf("result count without a search: %q", f.tt.Texts())
	}
}

func TestPanelSearchRanksBestMatchFirst(t *testing.T) {
	// Newest first without a search: "push gitlab" would lead.
	f := newPanelFixture(t, "git push origin", "push gitlab", "unrelated")
	f.a.search.Focus()
	f.tt.Frame()
	f.tt.Type("git push")
	if !f.tt.HasText("2 results") || f.tt.HasText("unrelated") {
		t.Fatalf("texts: %q", f.tt.Texts())
	}
	f.tt.Key(0, ui.KeyEnter)
	if got := f.written(t); len(got) != 1 || got[0] != "git push origin" {
		t.Errorf("Enter copied %q, want the whole-word match", got)
	}
}

func TestPanelSearchFiltersAndNoMatchTips(t *testing.T) {
	f := newPanelFixture(t, "plain words")
	historytest.Add(t, f.a.store, history.Capture{Kind: history.KindLink, Text: "https://example.com/words"}, historytest.T0.Add(time.Minute))
	f.a.reload()
	f.a.search.Focus()
	f.tt.Frame()
	f.tt.Type("words type:link")
	if !f.tt.HasText("1 result") || !f.tt.HasText("https://example.com/words") || f.tt.HasText("plain words") {
		t.Fatalf("texts: %q", f.tt.Texts())
	}
	f.tt.Type(" is:pinned")
	if !f.tt.HasText("No matches for “words type:link is:pinned”") || !f.tt.HasText("Or remove a filter") {
		t.Errorf("texts: %q", f.tt.Texts())
	}
}

func TestPanelPreviewPinButton(t *testing.T) {
	f := newPanelFixture(t, "keep me")
	if err := f.tt.Click("Pin"); err != nil {
		t.Fatalf("%v: %q", err, f.tt.Texts())
	}
	f.tt.Frame()
	l, _ := f.a.store.List(context.Background(), history.Filter{Tab: "all"})
	if len(l) != 1 || !l[0].Pinned {
		t.Fatalf("not pinned: %+v", l)
	}
	if !f.tt.HasText("Unpin") {
		t.Errorf("button not updated: %q", f.tt.Texts())
	}
}

func TestPanelDownEnterCopiesSecondRow(t *testing.T) {
	f := newPanelFixture(t, "oldest", "middle", "newest") // list: newest, middle, oldest
	f.tt.Key(0, ui.KeyDown)
	f.tt.Key(0, ui.KeyEnter)
	got := f.written(t)
	if len(got) != 1 || got[0] != "middle" {
		t.Fatalf("writes = %q", got)
	}
	l, _ := f.a.store.List(context.Background(), history.Filter{Tab: "all"})
	if l[0].Text != "middle" || l[0].UseCount != 2 {
		t.Errorf("copied entry should move to top: %v", historytest.Texts(l))
	}
}

func TestPanelShiftEnterCopiesPlainText(t *testing.T) {
	f := newPanelFixture(t)
	historytest.Add(t, f.a.store, history.Capture{Kind: history.KindText, Text: "rich", HTML: "<b>rich</b>"}, historytest.T0)
	f.a.reload()
	f.tt.Frame()
	f.tt.Key(ui.Shift, ui.KeyEnter)
	if len(f.writes) != 1 {
		t.Fatalf("writes = %d", len(f.writes))
	}
	if _, err := f.writes[0].Read(transfer.HTML); err == nil {
		t.Error("plain copy offered HTML")
	}
	f.tt.Key(0, ui.KeyEnter)
	if _, err := f.writes[1].Read(transfer.HTML); err != nil {
		t.Error("normal copy lost HTML")
	}
}

func TestPanelCmdNumberCopiesNthRow(t *testing.T) {
	f := newPanelFixture(t, "c", "b", "a")
	f.tt.Key(ui.Cmd, ui.Key3)
	if got := f.written(t); len(got) != 1 || got[0] != "c" {
		t.Errorf("writes = %q", got)
	}
}

func TestPanelRightClickPin(t *testing.T) {
	f := newPanelFixture(t, "keep me", "other")
	if err := f.tt.RightClick("keep me"); err != nil {
		t.Fatal(err)
	}
	if err := f.tt.ChooseMenuItem("Pin"); err != nil {
		t.Fatal(err)
	}
	f.tt.Frame()
	pinned, _ := f.a.store.Pinned(context.Background(), 10)
	if len(pinned) != 1 || pinned[0].Text != "keep me" {
		t.Errorf("pinned = %v", historytest.Texts(pinned))
	}
	if f.a.items[0].Text != "keep me" {
		t.Errorf("pinned entry should lead the list: %v", historytest.Texts(f.a.items))
	}
}

func TestPanelTransformMenuCopiesPlainOutput(t *testing.T) {
	f := newPanelFixture(t, "shout")
	if err := f.tt.RightClick("shout"); err != nil {
		t.Fatal(err)
	}
	if err := f.tt.ChooseMenuItem("Transform", "UPPERCASE"); err != nil {
		t.Fatal(err)
	}
	f.tt.Frame()
	if got := f.written(t); len(got) != 1 || got[0] != "SHOUT" {
		t.Errorf("writes = %q", got)
	}
}

func TestPanelTransformErrorKeepsPanelAndShowsToast(t *testing.T) {
	f := newPanelFixture(t, "not json")
	f.tt.RightClick("not json")
	f.tt.ChooseMenuItem("Transform", "JSON Minify")
	f.tt.Frame()
	if len(f.writes) != 0 {
		t.Error("wrote despite the error")
	}
	if !slices.ContainsFunc(f.tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "JSON Minify: ") }) {
		t.Errorf("toast missing: %q", f.tt.Texts())
	}
}

func TestPanelDeleteAndUndo(t *testing.T) {
	f := newPanelFixture(t, "first", "second")
	f.tt.Key(ui.Cmd, ui.KeyBackspace) // deletes "second", the chosen top row
	f.tt.Frame()
	if f.tt.HasText("second") {
		t.Fatalf("not deleted: %q", f.tt.Texts())
	}
	if !f.tt.HasText("Undo") {
		t.Fatalf("no Undo toast: %q", f.tt.Texts())
	}
	if err := f.tt.Click("Undo"); err != nil {
		t.Fatal(err)
	}
	f.tt.Frame()
	if !f.tt.HasText("second") || !f.tt.HasText("first") {
		t.Errorf("undo did not restore: %q", f.tt.Texts())
	}
}

func TestPanelMultiSelectMerge(t *testing.T) {
	f := newPanelFixture(t, "c", "b", "a") // list: a, b, c
	f.tt.Click("a")
	f.tt.ClickWith(ui.Shift, "c")
	f.tt.Key(0, ui.KeyEnter)
	if got := f.written(t); len(got) != 1 || got[0] != "a\nb\nc" {
		t.Errorf("merged = %q", got)
	}
	// merged output must not become an entry
	if !slices.Contains(f.mon.suppressed, history.Capture{Kind: history.KindText, Text: "a\nb\nc"}.Hash()) {
		t.Error("merged text not suppressed")
	}
}

func TestPanelSnippetFlow(t *testing.T) {
	f := newPanelFixture(t, "x")
	f.tt.Key(ui.Cmd, ui.KeyN)
	f.tt.Frame()
	if !f.tt.HasText("New Snippet") {
		t.Fatalf("editor not open: %q", f.tt.Texts())
	}
	if err := f.tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if !f.tt.HasText("Name is required") {
		t.Errorf("no name validation: %q", f.tt.Texts())
	}
	f.tt.Click("Name")
	f.tt.Type("Greeting")
	if err := f.tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if !f.tt.HasText("Body is required") {
		t.Errorf("no body validation: %q", f.tt.Texts())
	}
	f.tt.Click("Body")
	f.tt.Type("Hello there")
	f.tt.Click("Save")
	f.tt.Frame()
	if !f.tt.HasText("Greeting") {
		t.Fatalf("snippet row missing: %q", f.tt.Texts())
	}
	f.tt.Key(0, ui.KeyEnter)
	if got := f.written(t); len(got) != 1 || got[0] != "Hello there" {
		t.Errorf("snippet copy = %q", got)
	}
}

func TestPanelFilesCopySkipsMissing(t *testing.T) {
	f := newPanelFixture(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	historytest.Add(t, f.a.store, history.Capture{Kind: history.KindFiles, Text: real + "\n" + filepath.Join(dir, "gone.txt")}, historytest.T0)
	f.a.reload()
	f.tt.Frame()
	f.tt.Key(0, ui.KeyEnter)
	if len(f.writes) != 1 {
		t.Fatalf("writes = %d", len(f.writes))
	}
	files, err := f.writes[0].Files()
	if err != nil || len(files) != 1 || files[0] != real {
		t.Errorf("files = %v, %v", files, err)
	}
}

func TestPanelResumeButtonResumesOnce(t *testing.T) {
	f := newPanelFixture(t, "x")
	f.mon.paused = true
	f.a.reload()
	f.tt.Frame()
	if !f.tt.HasText("Capture paused until resumed") {
		t.Fatalf("no paused banner: %q", f.tt.Texts())
	}
	if err := f.tt.Click("Resume"); err != nil {
		t.Fatal(err)
	}
	if f.mon.resumes != 1 {
		t.Errorf("resumes = %d, want 1", f.mon.resumes)
	}
}

func TestPanelMoreMenuDeletesChosenEntry(t *testing.T) {
	f := newPanelFixture(t, "first", "second")
	if err := f.tt.Click("More actions"); err != nil {
		t.Fatalf("%v: %q", err, f.tt.Texts())
	}
	if err := f.tt.ChooseMenuItem("Delete"); err != nil {
		t.Fatalf("%v: %q", err, f.tt.Texts())
	}
	f.tt.Frame()
	if f.tt.HasText("second") || !f.tt.HasText("first") || !f.tt.HasText("Undo") {
		t.Errorf("delete not applied: %q", f.tt.Texts())
	}
}

func TestPanelFilterChipShowsOnlyLinks(t *testing.T) {
	f := newPanelFixture(t, "plain words")
	historytest.Add(t, f.a.store, history.Capture{Kind: history.KindLink, Text: "https://example.com"}, historytest.T0.Add(time.Minute))
	f.a.reload()
	f.tt.Frame()
	if err := f.tt.Click("Links"); err != nil {
		t.Fatalf("%v: %q", err, f.tt.Texts())
	}
	f.tt.Frame()
	if !f.tt.HasText("https://example.com") || f.tt.HasText("plain words") {
		t.Fatalf("links chip: %q", f.tt.Texts())
	}
	f.tt.Key(ui.Ctrl, ui.KeyTab)
	f.tt.Frame()
	if !f.tt.HasText("No images yet") {
		t.Errorf("next tab: %q", f.tt.Texts())
	}
}

func TestPanelArrowsSwitchFilterOnlyWhenSearchEmpty(t *testing.T) {
	f := newPanelFixture(t, "plain words")
	f.a.search.Focus()
	f.tt.Frame()
	f.tt.Key(0, ui.KeyRight)
	f.tt.Frame()
	if f.a.tab != 1 || !f.tt.HasText("Nothing pinned") {
		t.Fatalf("→ from All: tab %d, %q", f.a.tab, f.tt.Texts())
	}
	f.tt.Key(0, ui.KeyLeft)
	f.tt.Key(0, ui.KeyLeft) // wraps from All to Snippets
	f.tt.Frame()
	if f.a.tab != tabSnippets {
		t.Fatalf("← wrap: tab %d", f.a.tab)
	}
	f.tt.Key(0, ui.KeyRight) // back to All
	f.tt.Frame()
	f.tt.Type("pl")
	f.tt.Frame()
	f.tt.Key(0, ui.KeyLeft)
	f.tt.Frame()
	if f.a.tab != 0 || f.a.query != "pl" {
		t.Errorf("← with text switched filter: tab %d, query %q", f.a.tab, f.a.query)
	}
}

func TestPanelInvalidColorRowShowsIcon(t *testing.T) {
	f := newPanelFixture(t)
	historytest.Add(t, f.a.store, history.Capture{Kind: history.KindColor, Text: "red"}, historytest.T0)
	f.a.reload()
	f.tt.Frame()
	if !f.tt.HasText("red") {
		t.Error("row not shown")
	}
}

func TestPanelEnterBeforeReloadCopiesNewResults(t *testing.T) {
	f := newPanelFixture(t, "apple", "banana")
	var queue []func()
	f.a.async = func(fn func()) { queue = append(queue, fn) }
	f.tt.Type("ban")
	f.tt.Key(0, ui.KeyEnter)
	if len(f.writes) != 0 {
		t.Fatalf("copied before results landed: %v", f.written(t))
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		fn()
	}
	if got := f.written(t); !slices.Equal(got, []string{"banana"}) {
		t.Errorf("written = %v", got)
	}
}

func TestPanelSnippetSaveFailureKeepsEditor(t *testing.T) {
	f := newPanelFixture(t)
	f.a.newSnippet("n", "body")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.a.ctx = ctx
	f.a.saveEditor()
	if !f.a.editor.open || !strings.HasPrefix(f.a.editor.err, "Save failed") {
		t.Errorf("open=%v err=%q", f.a.editor.open, f.a.editor.err)
	}
}

func TestPanelFilesCopySuppressesWrittenPaths(t *testing.T) {
	f := newPanelFixture(t)
	f.a.cfg.MoveToTopOnCopy = false
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	historytest.Add(t, f.a.store, history.Capture{Kind: history.KindFiles, Text: real + "\n" + filepath.Join(dir, "gone.txt")}, historytest.T0)
	f.a.reload()
	f.tt.Frame()
	f.tt.Key(0, ui.KeyEnter)
	if want := filesHash([]string{real}); !slices.Contains(f.mon.suppressed, want) || len(f.mon.suppressed) != 1 {
		t.Errorf("suppressed = %v, want [%s]", f.mon.suppressed, want)
	}
}

func TestPanelFailedWriteLiftsSuppression(t *testing.T) {
	f := newPanelFixture(t, "x")
	f.a.cfg.MoveToTopOnCopy = false
	f.a.writeClipboard = func(transfer.Data) error { return errors.New("boom") }
	f.tt.Key(0, ui.KeyEnter)
	if len(f.mon.suppressed) == 0 || !slices.Equal(f.mon.suppressed, f.mon.unsuppressed) {
		t.Errorf("suppressed %v, unsuppressed %v", f.mon.suppressed, f.mon.unsuppressed)
	}
}

func TestPanelMergeOfDeletedEntriesSaysSo(t *testing.T) {
	f := newPanelFixture(t)
	f.a.copyMerged([]int64{999, 998})
	if f.a.toast != "Entries no longer exist" {
		t.Errorf("toast = %q", f.a.toast)
	}
}
