package history_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"okibaclip/internal/history"
	"okibaclip/internal/history/historytest"
	"okibaclip/internal/search"
)

// query parses s as typed in the panel, with relative dates from T0.
func query(s string) search.Query { return search.Parse(s, historytest.T0) }

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
	list, err := s.List(context.Background(), history.Filter{Tab: "all", Query: query("äb")})
	if err != nil || !slices.Equal(historytest.Texts(list), []string{"ÄBC def"}) {
		t.Errorf("got %v, %v", historytest.Texts(list), err)
	}
	list, _ = s.List(context.Background(), history.Filter{Tab: "all", Query: query("ÄBC")})
	if len(list) != 1 {
		t.Errorf("upper-case query: %v", historytest.Texts(list))
	}
}

func TestListSearch(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	at := func(min int) time.Time { return historytest.T0.Add(time.Duration(min) * time.Minute) }
	// Newest last: without search words, List returns them newest first.
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "push to git"}, at(0))
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "git push origin main"}, at(1))
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "gadget input pushing"}, at(2))
	historytest.Add(t, s, history.Capture{Kind: history.KindLink, Text: "https://github.com/push"}, at(3))
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: strings.Repeat("x", 5000) + " needle"}, at(4))
	if err := s.SetPinned(ctx, mustID(t, s, "push to git"), true); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 4000) // what a row holds of the needle text
	for _, tc := range []struct {
		q    string
		want []string
	}{
		// Every word, in any order; whole-word matches before partial ones,
		// then pinned, then newest. "gadget input" is too loose a fuzzy "git".
		{"git push", []string{"push to git", "git push origin main", "https://github.com/push"}},
		{`"git push"`, []string{"git push origin main"}},
		{"gtpsh", []string{"git push origin main"}}, // fuzzy: letters in order, close together
		{"push type:link", []string{"https://github.com/push"}},
		{"push is:unpinned type:text", []string{"git push origin main", "gadget input pushing"}},
		{"is:pinned", []string{"push to git"}},
		{"after:2026-01-01", nil},
		{"push before:" + at(2).Format("2006-01-02"), nil}, // the start of T0's day
		{"needle", []string{long}},                         // found past the start of the text a row holds
		{"zzz", nil},
	} {
		list, err := s.List(ctx, history.Filter{Tab: "all", Query: query(tc.q)})
		if err != nil {
			t.Fatal(err)
		}
		if got := historytest.Texts(list); !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %.60q, want %.60q", tc.q, got, tc.want)
		}
	}
}

func mustID(t *testing.T, s *history.Store, text string) int64 {
	t.Helper()
	l, err := s.List(context.Background(), history.Filter{Tab: "all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l {
		if e.Text == text {
			return e.ID
		}
	}
	t.Fatalf("no entry %q", text)
	return 0
}

func TestListSearchLimitAppliesAfterRanking(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "abc exact"}, historytest.T0)
	// Newer rows that pass the SQL fuzzy prefilter but are too spread out
	// to match: they must not take the limit's places.
	for i := range 5 {
		historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: fmt.Sprintf("a......b......c %d", i)}, historytest.T0.Add(time.Duration(i+1)*time.Minute))
	}
	// More real matches than the limit: the best ones are kept.
	for i := range 3 {
		historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: fmt.Sprintf("xabcx %d", i)}, historytest.T0.Add(time.Duration(i+10)*time.Minute))
	}
	list, err := s.List(ctx, history.Filter{Tab: "all", Query: query("abc"), Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := historytest.Texts(list), []string{"abc exact", "xabcx 2", "xabcx 1"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
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
	if _, err := s.Prune(ctx, history.Limits{MaxEntries: 1000, Retention: 30 * 24 * time.Hour}, now); err != nil {
		t.Fatal(err)
	}
	l, _ := s.List(ctx, history.Filter{Tab: "all"})
	if len(l) != 6 {
		t.Fatalf("retention left %v", historytest.Texts(l))
	}
	if _, err := s.Prune(ctx, history.Limits{MaxEntries: 2}, now); err != nil {
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
	n, removed, err := s.Delete(ctx, id, id2)
	if err != nil || n != 2 || len(removed) != 2 {
		t.Fatalf("removed %d, %v", n, err)
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
	if l, _ := s.List(ctx, history.Filter{Tab: "all", Query: query("hel")}); len(l) != 1 {
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
	l, _ := s.Snippets(ctx, query("HI T").Terms)
	if len(l) != 1 || l[0].Body != "Hi there" {
		t.Errorf("search: %+v", l)
	}
	s.DeleteSnippet(ctx, id)
	if l, _ := s.Snippets(ctx, nil); len(l) != 1 {
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
	var buf bytes.Buffer
	if err := src.WriteExport(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	dst := historytest.NewStore(t)
	e, sn, d, err := dst.Import(ctx, bytes.NewReader(buf.Bytes()))
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
	e, sn, d, err = dst.Import(ctx, bytes.NewReader(buf.Bytes()))
	if err != nil || e != 0 || sn != 0 || d != 4 {
		t.Errorf("re-import = %d %d %d %v", e, sn, d, err)
	}
	if l, _ := dst.List(ctx, history.Filter{Tab: "all"}); len(l) != 3 {
		t.Errorf("re-import duplicated rows: %d", len(l))
	}
	if sns, _ := dst.Snippets(ctx, nil); len(sns) != 1 {
		t.Errorf("snippets duplicated: %d", len(sns))
	}
}

func TestImportRejectsForeignFiles(t *testing.T) {
	ctx := context.Background()
	for _, in := range []string{`{`, `{"format":"other","version":1}`, `{"format":"okibaclip-export","version":3}`} {
		e, sn, _, err := historytest.NewStore(t).Import(ctx, strings.NewReader(in))
		if err == nil || e != 0 || sn != 0 {
			t.Errorf("accepted %s: %d %d %v", in, e, sn, err)
		}
	}
	if _, _, _, err := historytest.NewStore(t).Import(ctx, strings.NewReader(`{"format":"okibaclip-export","version":1,"entries":[],"snippets":[]}`)); err != nil {
		t.Error(err)
	}
}

func TestImportReclassifiesAndRejectsInvalid(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	f := []history.ExportEntry{
		{Kind: history.KindColor, Text: "red"},
		{Kind: history.KindLink, Text: "file:///etc/passwd"},
		{Kind: history.KindText, Text: strings.Repeat("a", history.MaxTextBytes+1)},
		{Kind: history.KindImage, Image: []byte("junk")},
		{Kind: history.KindText, Text: "future", UsedAt: time.Now().Add(1000 * time.Hour).UnixMilli()},
	}
	entries, _, _, err := s.Import(ctx, historytest.Export(t, f, nil))
	if err != nil {
		t.Fatal(err)
	}
	if entries != 3 {
		t.Errorf("entries = %d, want 3", entries)
	}
	l, err := s.List(ctx, history.Filter{Tab: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != 3 {
		t.Fatalf("listed %d", len(l))
	}
	for _, e := range l {
		if e.Kind != history.KindText {
			t.Errorf("%q kind = %v, want text", e.Text, e.Kind)
		}
		if e.UsedAt.After(time.Now()) {
			t.Errorf("%q used in the future: %v", e.Text, e.UsedAt)
		}
	}
}

func TestImportTwiceKeepsUseCount(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	f := []history.ExportEntry{{Kind: history.KindText, Text: "x", UseCount: 5}}
	for range 2 {
		if _, _, _, err := s.Import(ctx, historytest.Export(t, f, nil)); err != nil {
			t.Fatal(err)
		}
	}
	l, _ := s.List(ctx, history.Filter{Tab: "all"})
	if len(l) != 1 || l[0].UseCount != 5 {
		t.Errorf("list = %+v", l)
	}
}

func TestSearchScoresTitle(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "zzz"}, historytest.T0)
	if err := s.SetTitle(ctx, id, "Deploy script"); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, history.Filter{Tab: "all", Query: query("deploy")})
	if err != nil {
		t.Fatal(err)
	}
	if got := historytest.Texts(list); !slices.Equal(got, []string{"zzz"}) {
		t.Errorf("got %q, want [zzz]", got)
	}
}

func TestAppFilterUnicodeCase(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "x", SourceApp: "ÉDITION"}, historytest.T0)
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "y", SourceApp: "other"}, historytest.T0.Add(time.Minute))
	list, err := s.List(ctx, history.Filter{Tab: "all", Query: search.Query{Apps: []string{"édition"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := historytest.Texts(list); !slices.Equal(got, []string{"x"}) {
		t.Errorf("got %q, want [x]", got)
	}
}

func TestListTermsAttachThumbs(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id, _, err := s.Capture(ctx, history.Capture{Kind: history.KindImage, Image: historytest.PNG(t, 1, 1), Width: 1, Height: 1}, []byte{1, 2, 3}, historytest.T0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle(ctx, id, "cat"); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, history.Filter{Tab: "all", Query: query("cat")})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !bytes.Equal(list[0].Thumb, []byte{1, 2, 3}) {
		t.Errorf("result thumb = %v, want [1 2 3]", list)
	}
}

func TestWriteExportStreamsAllEntries(t *testing.T) {
	s := historytest.NewStore(t)
	for i := range 450 {
		historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: fmt.Sprintf("e%d", i)}, historytest.T0.Add(time.Duration(i)*time.Minute))
	}
	var buf bytes.Buffer
	if err := s.WriteExport(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	var f struct{ Entries []history.ExportEntry }
	if err := json.Unmarshal(buf.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != 450 {
		t.Fatalf("exported %d entries, want 450", len(f.Entries))
	}
	if f.Entries[0].Text != "e449" || f.Entries[449].Text != "e0" {
		t.Errorf("order: first %q last %q, want newest first", f.Entries[0].Text, f.Entries[449].Text)
	}
}
