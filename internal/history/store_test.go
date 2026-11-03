package history_test

import (
	"slices"

	"context"
	"my-clipboard/internal/history"
	"my-clipboard/internal/history/historytest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStoreFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix modes")
	}
	p := filepath.Join(t.TempDir(), "h.db")
	s, err := history.Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
}

func TestCaptureDedupeBumpsUse(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id1, created, _ := s.Capture(ctx, history.Capture{Kind: history.KindText, Text: "a"}, nil, historytest.T0)
	if !created {
		t.Error("first capture should create")
	}
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "b"}, historytest.T0.Add(time.Second))
	id2, created, _ := s.Capture(ctx, history.Capture{Kind: history.KindText, Text: "a", HTML: "<i>a</i>"}, nil, historytest.T0.Add(time.Minute))
	if id1 != id2 || created {
		t.Errorf("ids %d/%d created=%v", id1, id2, created)
	}
	list, _ := s.List(ctx, history.Filter{Tab: "all"})
	if !slices.Equal(historytest.Texts(list), []string{"a", "b"}) {
		t.Errorf("order = %v", historytest.Texts(list))
	}
	if list[0].UseCount != 2 || !list[0].UsedAt.Equal(historytest.T0.Add(time.Minute)) {
		t.Errorf("a = %+v", list[0])
	}
}

func TestSearchIsCaseInsensitiveUnicode(t *testing.T) {
	s := historytest.NewStore(t)
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "ÄBC def"}, historytest.T0)
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "other"}, historytest.T0.Add(time.Second))
	list, err := s.List(context.Background(), history.Filter{Tab: "all", Query: "äb"})
	if err != nil || !slices.Equal(historytest.Texts(list), []string{"ÄBC def"}) {
		t.Errorf("got %v, %v", historytest.Texts(list), err)
	}
	list, _ = s.List(context.Background(), history.Filter{Tab: "all", Query: "ÄBC"})
	if len(list) != 1 {
		t.Errorf("upper-case query: %v", historytest.Texts(list))
	}
}

func TestTabFilters(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "t"}, historytest.T0)
	historytest.Add(t, s, history.Capture{Kind: history.KindColor, Text: "#fff"}, historytest.T0.Add(1*time.Second))
	historytest.Add(t, s, history.Capture{Kind: history.KindLink, Text: "https://a.b"}, historytest.T0.Add(2*time.Second))
	historytest.Add(t, s, history.Capture{Kind: history.KindImage, Image: historytest.PNG(t, 3, 3), Width: 3, Height: 3}, historytest.T0.Add(3*time.Second))
	historytest.Add(t, s, history.Capture{Kind: history.KindFiles, Text: "/x"}, historytest.T0.Add(4*time.Second))
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "pinme"}, historytest.T0.Add(5*time.Second))
	s.SetPinned(ctx, id, true)
	count := func(tab string) int {
		l, err := s.List(ctx, history.Filter{Tab: tab})
		if err != nil {
			t.Fatal(err)
		}
		return len(l)
	}
	want := map[string]int{"all": 6, "pinned": 1, "text": 3, "link": 1, "image": 1, "files": 1}
	for tab, n := range want {
		if got := count(tab); got != n {
			t.Errorf("tab %s: %d, want %d", tab, got, n)
		}
	}
	all, _ := s.List(ctx, history.Filter{Tab: "all"})
	if all[0].Text != "pinme" {
		t.Error("pinned must come first")
	}
}

func TestPruneKeepsPinnedAndRespectsMaxAndRetention(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	now := historytest.T0.Add(100 * 24 * time.Hour)
	old := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "old-pinned"}, historytest.T0)
	s.SetPinned(ctx, old, true)
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "old"}, historytest.T0)
	for i := 0; i < 5; i++ {
		historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: string(rune('a' + i))}, now.Add(-time.Duration(5-i)*time.Hour))
	}
	if _, err := s.Prune(ctx, 1000, 30*24*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	l, _ := s.List(ctx, history.Filter{Tab: "all"})
	if len(l) != 6 {
		t.Fatalf("retention left %v", historytest.Texts(l))
	}
	if _, err := s.Prune(ctx, 2, 0, now); err != nil {
		t.Fatal(err)
	}
	l, _ = s.List(ctx, history.Filter{Tab: "all"})
	if !slices.Equal(historytest.Texts(l), []string{"old-pinned", "e", "d"}) {
		t.Errorf("after max: %v", historytest.Texts(l))
	}
}

func TestDeleteRestoreRoundTrip(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	png := historytest.PNG(t, 5, 5)
	id := historytest.Add(t, s, history.Capture{Kind: history.KindImage, Image: png, Width: 5, Height: 5}, historytest.T0)
	id2 := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "hello", HTML: "<b>hello</b>"}, historytest.T0.Add(time.Second))
	removed, err := s.Delete(ctx, id, id2)
	if err != nil || len(removed) != 2 {
		t.Fatalf("removed %d, %v", len(removed), err)
	}
	if l, _ := s.List(ctx, history.Filter{Tab: "all"}); len(l) != 0 {
		t.Fatal("not deleted")
	}
	if err := s.Restore(ctx, removed); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, id2)
	if err != nil || got.Text != "hello" || got.HTML != "<b>hello</b>" {
		t.Errorf("restored %+v, %v", got, err)
	}
	img, _ := s.Get(ctx, id)
	if string(img.Image) != string(png) || len(img.Thumb) == 0 {
		t.Error("image or thumb lost in restore")
	}
	if l, _ := s.List(ctx, history.Filter{Tab: "all", Query: "hel"}); len(l) != 1 {
		t.Error("search column lost in restore")
	}
}

func TestClearKeepsPinned(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "gone"}, historytest.T0)
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "kept"}, historytest.T0)
	s.SetPinned(ctx, id, true)
	n, err := s.Clear(ctx, true)
	if err != nil || n != 1 {
		t.Fatalf("cleared %d, %v", n, err)
	}
	l, _ := s.List(ctx, history.Filter{Tab: "all"})
	if !slices.Equal(historytest.Texts(l), []string{"kept"}) {
		t.Errorf("left %v", historytest.Texts(l))
	}
}

func TestSnippetsCRUDAndSearch(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id, err := s.SaveSnippet(ctx, history.Snippet{Name: "Greeting", Body: "Hello there"})
	if err != nil || id == 0 {
		t.Fatal(id, err)
	}
	s.SaveSnippet(ctx, history.Snippet{Name: "Sig", Body: "Best, Me"})
	if _, err := s.SaveSnippet(ctx, history.Snippet{ID: id, Name: "Greeting", Body: "Hi there"}); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Snippets(ctx, "HI T")
	if len(l) != 1 || l[0].Body != "Hi there" {
		t.Errorf("search: %+v", l)
	}
	s.DeleteSnippet(ctx, id)
	if l, _ := s.Snippets(ctx, ""); len(l) != 1 {
		t.Errorf("after delete: %+v", l)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	src := historytest.NewStore(t)
	ctx := context.Background()
	png := historytest.PNG(t, 6, 6)
	historytest.Add(t, src, history.Capture{Kind: history.KindText, Text: "one", HTML: "<p>one</p>"}, historytest.T0)
	pid := historytest.Add(t, src, history.Capture{Kind: history.KindLink, Text: "https://x.y"}, historytest.T0.Add(time.Second))
	src.SetPinned(ctx, pid, true)
	historytest.Add(t, src, history.Capture{Kind: history.KindImage, Image: png, Width: 6, Height: 6}, historytest.T0.Add(2*time.Second))
	src.SaveSnippet(ctx, history.Snippet{Name: "n", Body: "b"})
	f, err := src.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dst := historytest.NewStore(t)
	e, sn, d, err := dst.Import(ctx, f)
	if err != nil || e != 3 || sn != 1 || d != 0 {
		t.Fatalf("import = %d %d %d %v", e, sn, d, err)
	}
	l, _ := dst.List(ctx, history.Filter{Tab: "all"})
	if len(l) != 3 || l[0].Text != "https://x.y" || !l[0].Pinned {
		t.Errorf("imported %v pinned=%v", historytest.Texts(l), l[0].Pinned)
	}
	var img history.Entry
	for _, x := range l {
		if x.Kind == history.KindImage {
			img, _ = dst.Get(ctx, x.ID)
		}
	}
	if string(img.Image) != string(png) || len(img.Thumb) == 0 || img.Width != 6 {
		t.Errorf("image not restored: %+v", img.Width)
	}
	e, sn, d, err = dst.Import(ctx, f)
	if err != nil || e != 0 || sn != 0 || d != 4 {
		t.Errorf("re-import = %d %d %d %v", e, sn, d, err)
	}
	if l, _ := dst.List(ctx, history.Filter{Tab: "all"}); len(l) != 3 {
		t.Errorf("re-import duplicated rows: %d", len(l))
	}
	if sns, _ := dst.Snippets(ctx, ""); len(sns) != 1 {
		t.Errorf("snippets duplicated: %d", len(sns))
	}
}

func TestDecodeExportRejects(t *testing.T) {
	for _, in := range []string{`{`, `{"format":"other","version":1}`, `{"format":"my-clipboard-export","version":2}`} {
		if _, err := history.DecodeExport([]byte(in)); err == nil {
			t.Errorf("accepted %s", in)
		}
	}
	if _, err := history.DecodeExport([]byte(`{"format":"my-clipboard-export","version":1,"entries":[],"snippets":[]}`)); err != nil {
		t.Error(err)
	}
}
