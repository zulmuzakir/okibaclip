package history_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"

	"okibaclip/internal/history"
	"okibaclip/internal/history/historytest"
)

func userVersion(t *testing.T, path string) int64 {
	t.Helper()
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(context.Background(), "PRAGMA user_version")
	if err != nil {
		t.Fatal(err)
	}
	return rows.Values[0][0].(int64)
}

func TestOpenMigratesV1Database(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "history.db")
	db, err := sqlite.Open(ctx, p, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE entries (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, text TEXT NOT NULL DEFAULT '', search TEXT NOT NULL DEFAULT '', html TEXT, image BLOB, thumb BLOB, width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, size INTEGER NOT NULL, hash TEXT NOT NULL UNIQUE, pinned INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, used_at INTEGER NOT NULL, use_count INTEGER NOT NULL DEFAULT 1)`,
		`CREATE INDEX entries_order ON entries(pinned DESC, used_at DESC)`,
		`CREATE TABLE snippets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, body TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`INSERT INTO entries (kind,text,search,size,hash,pinned,created_at,used_at) VALUES ('text','pinned','pinned',6,'h1',1,1000,5000)`,
		`INSERT INTO entries (kind,text,search,size,hash,pinned,created_at,used_at) VALUES ('text','plain','plain',5,'h2',0,1000,4000)`,
		`PRAGMA user_version = 1`,
	} {
		if _, err := db.Execute(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := history.Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.List(ctx, history.Filter{Tab: "all"})
	if err != nil || !slices.Equal(historytest.Texts(l), []string{"pinned", "plain"}) {
		t.Fatalf("list = %v, %v", historytest.Texts(l), err)
	}
	if l[0].PinOrder != -5000 {
		t.Errorf("PinOrder = %d, want -5000", l[0].PinOrder)
	}
	s.Close()
	if v := userVersion(t, p); v != 3 {
		t.Errorf("user_version = %d", v)
	}
	db, _ = sqlite.Open(ctx, p, sqlite.OpenOptions{})
	defer db.Close()
	rows, _ := db.Query(ctx, "PRAGMA auto_vacuum")
	if rows.Values[0][0].(int64) != 2 {
		t.Errorf("auto_vacuum = %v", rows.Values[0][0])
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "history.db")
	db, err := sqlite.Open(ctx, p, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	db.Execute(ctx, "PRAGMA user_version = 99")
	db.Close()
	if _, err := history.Open(ctx, p); err == nil || !strings.Contains(err.Error(), "newer than this Okiba Clip supports") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRecoversHalfCreatedV1(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "history.db")
	db, err := sqlite.Open(ctx, p, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(ctx, `CREATE TABLE entries (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, text TEXT NOT NULL DEFAULT '', search TEXT NOT NULL DEFAULT '', html TEXT, image BLOB, thumb BLOB, width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, size INTEGER NOT NULL, hash TEXT NOT NULL UNIQUE, pinned INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, used_at INTEGER NOT NULL, use_count INTEGER NOT NULL DEFAULT 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := history.Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if v := userVersion(t, p); v != 3 {
		t.Errorf("user_version = %d", v)
	}
}

func blob(n int, b byte) []byte { return bytes.Repeat([]byte{b}, n) }

func TestPruneStorageBudgetKeepsPinnedAndNewest(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	const sz = 400 << 10
	pin := historytest.Add(t, s, history.Capture{Kind: history.KindImage, Image: blob(sz, 1)}, historytest.T0)
	s.SetPinned(ctx, pin, true)
	historytest.Add(t, s, history.Capture{Kind: history.KindImage, Image: blob(sz, 2)}, historytest.T0.Add(time.Second))
	historytest.Add(t, s, history.Capture{Kind: history.KindImage, Image: blob(sz, 3)}, historytest.T0.Add(2*time.Second))
	newest := historytest.Add(t, s, history.Capture{Kind: history.KindImage, Image: blob(sz, 4)}, historytest.T0.Add(3*time.Second))
	n, err := s.Prune(ctx, history.Limits{MaxEntries: 500, MaxBytes: 1 << 20}, historytest.T0.Add(time.Minute))
	if err != nil || n != 2 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	l, _ := s.List(ctx, history.Filter{Tab: "all"})
	var ids []int64
	for _, e := range l {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, []int64{pin, newest}) {
		t.Errorf("left %v, want [%d %d]", ids, pin, newest)
	}
	// A budget smaller than the newest entry still keeps it.
	if _, err := s.Prune(ctx, history.Limits{MaxEntries: 500, MaxBytes: 1}, historytest.T0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if l, _ = s.List(ctx, history.Filter{Tab: "all"}); len(l) != 2 {
		t.Errorf("tiny budget left %d entries", len(l))
	}
	u, err := s.Usage(ctx)
	if err != nil || u.Bytes != 2*sz || u.ImageBytes != 2*sz {
		t.Errorf("usage = %+v, %v", u, err)
	}
}

func TestCompactShrinksFile(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "history.db")
	s, err := history.Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 5; i++ {
		historytest.Add(t, s, history.Capture{Kind: history.KindImage, Image: blob(400<<10, byte(i+1))}, historytest.T0.Add(time.Duration(i)*time.Second))
	}
	before, _ := os.Stat(p)
	if _, err := s.Clear(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p)
	if after.Size() >= before.Size()/2 {
		t.Errorf("size %d -> %d", before.Size(), after.Size())
	}
}

func TestListAppFilter(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "from slack", SourceApp: "com.tinyspeck.slackmacgap", SourceName: "Slack"}, historytest.T0)
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "from safari", SourceApp: "com.apple.safari", SourceName: "Safari"}, historytest.T0.Add(time.Second))
	l, err := s.List(ctx, history.Filter{Tab: "all", Query: query("app:sla")})
	if err != nil || !slices.Equal(historytest.Texts(l), []string{"from slack"}) {
		t.Errorf("app:sla = %v, %v", historytest.Texts(l), err)
	}
	if l[0].SourceName != "Slack" {
		t.Errorf("SourceName = %q", l[0].SourceName)
	}
}

func TestReorderPinnedOrdersPinnedTab(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	var ids []int64
	for i, x := range []string{"a", "b", "c"} {
		id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: x}, historytest.T0.Add(time.Duration(i)*time.Second))
		s.SetPinned(ctx, id, true)
		ids = append(ids, id)
	}
	// Newest pin first.
	l, _ := s.Pinned(ctx, 10)
	if !slices.Equal(historytest.Texts(l), []string{"c", "b", "a"}) {
		t.Fatalf("default pin order %v", historytest.Texts(l))
	}
	if err := s.ReorderPinned(ctx, []int64{ids[0], ids[2], ids[1]}); err != nil {
		t.Fatal(err)
	}
	l, _ = s.Pinned(ctx, 10)
	if !slices.Equal(historytest.Texts(l), []string{"a", "c", "b"}) {
		t.Errorf("Pinned = %v", historytest.Texts(l))
	}
	l, _ = s.List(ctx, history.Filter{Tab: "pinned"})
	if !slices.Equal(historytest.Texts(l), []string{"a", "c", "b"}) {
		t.Errorf("List = %v", historytest.Texts(l))
	}
}

func TestSetTitleIsSearchable(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "192.168.1.5"}, historytest.T0)
	s.SetPinned(ctx, id, true)
	if err := s.SetTitle(ctx, id, "  Router address "); err != nil {
		t.Fatal(err)
	}
	l, _ := s.List(ctx, history.Filter{Tab: "all", Query: query("router")})
	if len(l) != 1 || l[0].Title != "Router address" {
		t.Fatalf("list = %+v", l)
	}
}

func TestUnpinClearsTitle(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "secret value"}, historytest.T0)
	s.SetPinned(ctx, id, true)
	s.SetTitle(ctx, id, "router")
	s.SetPinned(ctx, id, false)
	e, _ := s.Get(ctx, id)
	if e.Title != "" || e.Pinned || e.PinOrder != 0 {
		t.Errorf("entry = %+v", e)
	}
	if l, _ := s.List(ctx, history.Filter{Tab: "all", Query: query("router")}); len(l) != 0 {
		t.Errorf("title still searchable: %v", historytest.Texts(l))
	}
}

func TestUpdateTextMergesIntoExisting(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	a := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "alpha"}, historytest.T0)
	b := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "beta"}, historytest.T0.Add(time.Second))
	s.SetPinned(ctx, a, true)
	got, err := s.UpdateText(ctx, a, "beta", historytest.T0.Add(time.Minute))
	if err != nil || got != b {
		t.Fatalf("merged id = %d, %v (want %d)", got, err, b)
	}
	l, _ := s.List(ctx, history.Filter{Tab: "all"})
	if len(l) != 1 || l[0].ID != b || !l[0].Pinned {
		t.Fatalf("list = %+v", l)
	}
	id, err := s.UpdateText(ctx, b, "https://example.com", historytest.T0.Add(time.Hour))
	if err != nil || id != b {
		t.Fatal(id, err)
	}
	e, _ := s.Get(ctx, b)
	if e.Kind != history.KindLink || e.Text != "https://example.com" {
		t.Errorf("entry = %+v", e)
	}
}

func TestUpdateTextRejectsImages(t *testing.T) {
	s := historytest.NewStore(t)
	id := historytest.Add(t, s, history.Capture{Kind: history.KindImage, Image: historytest.PNG(t, 4, 4), Width: 4, Height: 4}, historytest.T0)
	if _, err := s.UpdateText(context.Background(), id, "x", historytest.T0); err == nil {
		t.Fatal("edited an image")
	}
}

func TestSnippetsOrderByUse(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	var ids = map[string]int64{}
	for _, n := range []string{"a", "b", "c"} {
		id, err := s.SaveSnippet(ctx, history.Snippet{Name: n, Body: n})
		if err != nil {
			t.Fatal(err)
		}
		ids[n] = id
	}
	s.UseSnippet(ctx, ids["b"], historytest.T0)
	s.UseSnippet(ctx, ids["b"], historytest.T0)
	sn, _ := s.Snippets(ctx, nil)
	var names []string
	for _, x := range sn {
		names = append(names, x.Name)
	}
	if !slices.Equal(names, []string{"b", "a", "c"}) {
		t.Errorf("order = %v", names)
	}
}

func TestExportImportKeepsV2Fields(t *testing.T) {
	src := historytest.NewStore(t)
	ctx := context.Background()
	id := historytest.Add(t, src, history.Capture{Kind: history.KindText, Text: "keep me", SourceApp: "app.x", SourceName: "X"}, historytest.T0)
	src.SetPinned(ctx, id, true)
	src.SetTitle(ctx, id, "Named")
	src.SaveSnippet(ctx, history.Snippet{Name: "n", Body: "b", Folder: "Work", Shortcut: "CmdOrCtrl+1"})
	var buf bytes.Buffer
	if err := src.WriteExport(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	f, err := history.DecodeExport(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	dst := historytest.NewStore(t)
	if _, _, _, err := dst.Import(ctx, f); err != nil {
		t.Fatal(err)
	}
	l, _ := dst.List(ctx, history.Filter{Tab: "pinned"})
	if len(l) != 1 || l[0].Title != "Named" || l[0].SourceName != "X" || l[0].PinOrder == 0 {
		t.Errorf("entry = %+v", l)
	}
	if l, _ := dst.List(ctx, history.Filter{Tab: "all", Query: query("named")}); len(l) != 1 {
		t.Errorf("imported title not searchable")
	}
	sn, _ := dst.Snippets(ctx, nil)
	if len(sn) != 1 || sn[0].Folder != "Work" || sn[0].Shortcut != "CmdOrCtrl+1" {
		t.Errorf("snippets = %+v", sn)
	}
	if f2, err := history.DecodeExport([]byte(`{"format":"okibaclip-export","version":1,"entries":[{"kind":"text","text":"old"}],"snippets":[]}`)); err != nil {
		t.Fatal(err)
	} else if _, _, _, err := dst.Import(ctx, f2); err != nil {
		t.Fatal(err)
	}
}

func TestIDsNotReusedAfterDelete(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id1 := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "a"}, historytest.T0)
	if _, _, err := s.Delete(ctx, id1); err != nil {
		t.Fatal(err)
	}
	id2 := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "b"}, historytest.T0)
	if id2 <= id1 {
		t.Errorf("id %d reused after deleting %d", id2, id1)
	}
}

func TestRestoreMergesIntoRecapturedEntry(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "a"}, historytest.T0)
	if err := s.SetPinned(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle(ctx, id, "T"); err != nil {
		t.Fatal(err)
	}
	_, undo, err := s.Delete(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "a"}, historytest.T0.Add(time.Minute))
	if err := s.Restore(ctx, undo); err != nil {
		t.Fatal(err)
	}
	l, _ := s.List(ctx, history.Filter{Tab: "all"})
	if len(l) != 1 || !l[0].Pinned || l[0].Title != "T" {
		t.Errorf("entries = %+v, want one pinned entry titled T", l)
	}
}

func TestDeleteSkipsUndoOverCap(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	thumb := make([]byte, 33<<20)
	var ids []int64
	for _, w := range []int{1, 2} {
		id, _, err := s.Capture(ctx, history.Capture{Kind: history.KindImage, Image: historytest.PNG(t, w, w), Width: w, Height: w}, thumb, historytest.T0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	n, undo, err := s.Delete(ctx, ids...)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || undo != nil {
		t.Errorf("deleted %d, undo %v; want 2 and nil undo", n, undo)
	}
	if l, _ := s.List(ctx, history.Filter{Tab: "all"}); len(l) != 0 {
		t.Errorf("%d entries left", len(l))
	}
}

func TestPruneRetentionKeepsNewest(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "only"}, historytest.T0)
	n, err := s.Prune(ctx, history.Limits{Retention: time.Minute}, historytest.T0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("pruned %d, want 0: the newest unpinned entry stays", n)
	}
}

func TestCaptureDedupeRefreshesHTML(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "a"}, historytest.T0)
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "a", HTML: "<i>a</i>"}, historytest.T0.Add(time.Second))
	e, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if e.HTML != "<i>a</i>" {
		t.Fatalf("html = %q after rich re-copy", e.HTML)
	}
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "a"}, historytest.T0.Add(2*time.Second))
	if e, _ = s.Get(ctx, id); e.HTML != "<i>a</i>" {
		t.Errorf("plain re-copy dropped html: %q", e.HTML)
	}
}

func TestSourceAppsNameFromNewestNamed(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "x1", SourceApp: "app.a", SourceName: "Old"}, historytest.T0)
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "x2", SourceApp: "app.a"}, historytest.T0.Add(time.Minute))
	apps, err := s.SourceApps(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "Old" {
		t.Fatalf("apps = %+v, want Old", apps)
	}
	historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "x3", SourceApp: "app.a", SourceName: "New"}, historytest.T0.Add(2*time.Minute))
	if apps, _ = s.SourceApps(ctx, 10); len(apps) != 1 || apps[0].Name != "New" {
		t.Errorf("apps = %+v, want New", apps)
	}
}

func TestUpdateTextRejectsOversize(t *testing.T) {
	s := historytest.NewStore(t)
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "a"}, historytest.T0)
	if _, err := s.UpdateText(context.Background(), id, strings.Repeat("x", history.MaxTextBytes+1), historytest.T0); err != history.ErrTooLarge {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

func TestSetTitleCapsRunes(t *testing.T) {
	s := historytest.NewStore(t)
	ctx := context.Background()
	id := historytest.Add(t, s, history.Capture{Kind: history.KindText, Text: "a"}, historytest.T0)
	if err := s.SetTitle(ctx, id, strings.Repeat("é", 150)); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(e.Title)); n != history.MaxTitleRunes {
		t.Errorf("title has %d runes, want %d", n, history.MaxTitleRunes)
	}
}

func TestImportSkipsInvalidSnippetsAndHugeImages(t *testing.T) {
	ctx := context.Background()
	now := historytest.T0.UnixMilli()
	f := history.ExportFile{
		Format:  history.ExportFormat,
		Version: history.ExportVersion,
		Entries: []history.ExportEntry{{Kind: history.KindImage, Image: historytest.PNGHeader(8001, 5001), Width: 8001, Height: 5001, CreatedAt: now, UsedAt: now}},
		Snippets: []history.ExportSnippet{
			{Name: "  ", Body: "x"},
			{Name: "huge", Body: strings.Repeat("x", history.MaxTextBytes+1)},
			{Name: " Hi ", Body: "hello"},
		},
	}
	dst := historytest.NewStore(t)
	entries, snippets, _, err := dst.Import(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 0 || snippets != 1 {
		t.Errorf("imported %d entries and %d snippets; want 0 and 1", entries, snippets)
	}
	if sn, _ := dst.Snippets(ctx, nil); len(sn) != 1 || sn[0].Name != "Hi" {
		t.Errorf("snippets = %+v, want one named Hi", sn)
	}
}
