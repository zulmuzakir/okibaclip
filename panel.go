package main

import (
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"pastello/internal/history"
	"pastello/internal/search"
	"pastello/internal/transform"
)

var digitKeys = []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}

// keys spells a shortcut as the platform does: ⌥⇧V on macOS.
func keys(shortcut string) string {
	if runtime.GOOS != "darwin" {
		return shortcut
	}
	return strings.NewReplacer("CmdOrCtrl+", "⌘", "Cmd+", "⌘", "Ctrl+", "⌃", "Alt+", "⌥", "Shift+", "⇧").Replace(shortcut)
}

func cmdSym() string {
	if isDarwin {
		return "⌘"
	}
	return "Ctrl+"
}

// selectedIDs returns the ids to act on: the multi-selection in list order,
// or the chosen row.
func (a *app) selectedIDs() []int64 {
	var ids []int64
	if a.selected.Len() > 1 {
		for _, e := range a.items {
			if a.selected.Has(e.ID) {
				ids = append(ids, e.ID)
			}
		}
		if len(ids) > 1 {
			return ids
		}
	}
	if a.chosen >= 0 && a.chosen < len(a.items) {
		return []int64{a.items[a.chosen].ID}
	}
	return nil
}

func (a *app) chosenEntry() (history.Entry, bool) {
	if a.tab == tabSnippets || a.chosen < 0 || a.chosen >= len(a.items) {
		return history.Entry{}, false
	}
	return a.items[a.chosen], true
}

func (a *app) chosenSnip() (history.Snippet, bool) {
	if a.tab != tabSnippets || a.chosenSnippet < 0 || a.chosenSnippet >= len(a.snippets) {
		return history.Snippet{}, false
	}
	return a.snippets[a.chosenSnippet], true
}

func (a *app) move(delta int) {
	a.selected.Clear()
	if a.tab == tabSnippets {
		a.chosenSnippet = max(0, min(a.chosenSnippet+delta, len(a.snippets)-1))
		a.snipList.ScrollIntoView(a.chosenSnippet)
		return
	}
	a.chosen = max(0, min(a.chosen+delta, len(a.items)-1))
	if len(a.items) > 0 {
		a.list.ScrollIntoView(a.chosen)
	}
	a.loadPreview()
}

func (a *app) setTab(i int) {
	a.tab = (i%len(tabLabels) + len(tabLabels)) % len(tabLabels)
	a.chosen, a.chosenSnippet = 0, 0
	a.selected.Clear()
	a.preview, a.previewBitmap = history.Entry{}, nil
	a.reload()
}

func (a *app) copyChosen(plain bool) {
	if a.tab == tabSnippets {
		if s, ok := a.chosenSnip(); ok {
			a.copySnippet(s)
		}
		return
	}
	ids := a.selectedIDs()
	if len(ids) > 1 && !plain {
		a.copyMerged(ids)
		return
	}
	if e, ok := a.chosenEntry(); ok {
		a.copyEntry(e, plain)
	}
}

func (a *app) copyNth(n int) {
	if a.tab == tabSnippets {
		if n < len(a.snippets) {
			a.copySnippet(a.snippets[n])
		}
		return
	}
	if n < len(a.items) {
		a.copyEntry(a.items[n], false)
	}
}

func (a *app) newSnippet(name, body string) {
	if a.tab != tabSnippets {
		a.setTab(tabSnippets)
	}
	a.editor.open, a.editor.id, a.editor.name, a.editor.body, a.editor.err = true, 0, name, body, ""
}

func (a *app) saveAsSnippet(e history.Entry) {
	a.async(func() {
		full, err := a.store.Get(a.ctx, e.ID)
		if err != nil {
			return
		}
		name := textPreview(full.Text, 40)
		if e.Kind != history.KindText {
			name = preview(full)
		}
		name = firstRunes(name, 40)
		a.post(func() { a.newSnippet(name, full.Text) })
	})
}

func (a *app) saveEditor() {
	name := firstRunes(strings.TrimSpace(a.editor.name), 100)
	switch {
	case name == "":
		a.editor.err = "Name is required"
		return
	case strings.TrimSpace(a.editor.body) == "":
		a.editor.err = "Body is required"
		return
	case len(a.editor.body) > history.MaxTextBytes:
		a.editor.err = "Body is over 1 MiB"
		return
	}
	sn := history.Snippet{ID: a.editor.id, Name: name, Body: a.editor.body}
	a.editor.open, a.editor.err = false, ""
	a.async(func() {
		if _, err := a.store.SaveSnippet(a.ctx, sn); err != nil {
			slog.Error("save snippet", "err", err)
		}
		a.post(a.changed)
	})
}

func (a *app) thumb(e history.Entry) *ui.Bitmap {
	if b, ok := a.thumbs[e.ID]; ok {
		return b
	}
	var b *ui.Bitmap
	if len(e.Thumb) > 0 {
		b, _ = ui.DecodeBitmap(e.Thumb)
	}
	a.thumbs[e.ID] = b
	return b
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	root := c.Root()
	if c.Vibrancy() {
		root.Background(ui.Transparent)
	} else {
		root.Background(t.Background)
	}
	a.shortcuts(c)
	a.showToast(c)

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(14, 16).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconSearch).FontSize(18).TextColor(t.TextMuted)
			// The field takes Enter before the panel's shortcuts see it.
			in := ui.TextInputBase(c, &a.query).Bind(&a.search).Grow(1).FontSize(18).
				Placeholder(`Search: words, "phrase", type:link, is:pinned, after:7d`).Label("Search clipboard").
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEnter {
						return false
					}
					a.copyChosen(k.Mods&ui.Shift != 0)
					return true
				})
			if in.Changed() {
				a.chosen, a.chosenSnippet = 0, 0
				a.selected.Clear()
				a.reload()
			}
			if count := a.resultCount(); count != "" {
				ui.Text(c, count).FontSize(12).TextColor(t.TextMuted)
			}
		})
		if ui.Tabs(c, &a.tab, tabLabels...).Changed() {
			a.setTab(a.tab)
		}
		a.banners(c)
		ui.Divider(c)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(300).Children(func() {
				if a.tab == tabSnippets {
					a.snippetList(c)
				} else {
					a.historyList(c)
				}
			})
			ui.Box(c).Width(1).Background(t.Border)
			ui.Column(c).Grow(1).Padding(12).Gap(8).Children(func() {
				if a.tab == tabSnippets {
					a.snippetPreview(c)
				} else {
					a.previewPane(c)
				}
			})
		})
		ui.Divider(c)
		ui.Row(c).Padding(8, 16).Gap(16).Children(func() {
			note := a.footerNote
			switch {
			case note != "":
			case a.tab != tabSnippets && a.selected.Len() > 1:
				note = fmt.Sprintf("%d selected — ↵ Copy merged", a.selected.Len())
			default:
				cm := cmdSym()
				note = fmt.Sprintf("%s to show · ↵ Copy · ⇧↵ Plain · %sP Pin · esc Close", keys(a.shortcut), cm)
			}
			ui.Text(c, note).FontSize(11).TextColor(t.TextMuted).Grow(1)
		})
	})
	a.editorModal(c)
}

func (a *app) shortcuts(c *ui.Context) {
	if a.editor.open {
		return
	}
	if a.query != "" && c.Shortcut(0, ui.KeyEscape) {
		a.query = ""
		a.chosen, a.chosenSnippet = 0, 0
		a.reload()
	}
	if c.Shortcut(0, ui.KeyDown) {
		a.move(1)
	}
	if c.Shortcut(0, ui.KeyUp) {
		a.move(-1)
	}
	if c.Shortcut(0, ui.KeyEnter) {
		a.copyChosen(false)
	}
	if c.Shortcut(ui.Shift, ui.KeyEnter) {
		a.copyChosen(true)
	}
	for i, k := range digitKeys {
		c.OnShortcut(ui.Cmd, k, func() { a.copyNth(i) })
	}
	c.OnShortcut(ui.Cmd, ui.KeyP, func() {
		if e, ok := a.chosenEntry(); ok {
			a.setPinned(e.ID, !e.Pinned)
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyBackspace, func() {
		if s, ok := a.chosenSnip(); ok {
			a.deleteSnippet(s)
			return
		}
		a.deleteEntries(a.selectedIDs())
	})
	c.OnShortcut(ui.Ctrl, ui.KeyTab, func() { a.setTab(a.tab + 1) })
	c.OnShortcut(ui.Ctrl|ui.Shift, ui.KeyTab, func() { a.setTab(a.tab - 1) })
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { a.search.Focus() })
	c.OnShortcut(ui.Cmd, ui.KeyN, func() { a.newSnippet("", "") })
	c.OnShortcut(ui.Cmd, ui.KeyComma, a.openSettings)
	c.OnShortcut(ui.Cmd, ui.KeyQ, mygo.App.Quit)
}

func (a *app) deleteSnippet(s history.Snippet) {
	a.async(func() {
		if err := a.store.DeleteSnippet(a.ctx, s.ID); err != nil {
			slog.Error("delete snippet", "err", err)
		}
		a.post(a.changed)
	})
}

func (a *app) showToast(c *ui.Context) {
	if a.toast == "" {
		return
	}
	msg, undo := a.toast, a.toastUndo
	a.toast = ""
	if len(undo) > 0 {
		c.AddToast(ui.Toast{ID: msg, Title: msg, Action: "Undo", OnAction: a.undoDelete, Timeout: 8 * time.Second})
		return
	}
	c.Toast(msg)
}

func (a *app) banners(c *ui.Context) {
	t := c.Theme()
	line := func(msg string, button string, fn func()) {
		ui.Row(c).Padding(4, 16).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, msg).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1)
			if button != "" {
				ui.Button(c, button).OnClick(fn)
			}
		})
	}
	if a.waylandMode == "wayland" {
		line("Native Wayland: copies are recorded only while this window is focused.", "", nil)
	}
	if paused, until := a.paused(); paused {
		msg := "Capture paused until resumed"
		if !until.IsZero() {
			msg = "Capture paused until " + until.Format("15:04")
		}
		line(msg, "Resume", func() { a.mon.Resume() })
	}
	if a.trayErr != "" {
		line("Tray icon unavailable: "+a.trayErr, "", nil)
	}
}

func (a *app) leading(c *ui.Context, e history.Entry, fg ui.Color) {
	switch e.Kind {
	case history.KindImage:
		if b := a.thumb(e); b != nil {
			ui.Image(c, b).Size(28, 28).Fit(ui.Contain)
			return
		}
	case history.KindColor:
		ui.Box(c).Size(28, 28).Radius(6).Background(ui.Hex(strings.TrimSpace(e.Text))).Border(1, c.Theme().Border)
		return
	}
	ui.Icon(c, kindIcon(e.Kind)).FontSize(20).TextColor(fg).Size(28, 28)
}

// resultCount labels how many results the shown search found, or is empty
// when nothing is searched.
func (a *app) resultCount() string {
	if a.shown.IsZero() {
		return ""
	}
	n := len(a.items)
	if a.tab == tabSnippets {
		n = len(a.snippets)
	}
	switch {
	case n == 1:
		return "1 result"
	case n >= listLimit && a.tab != tabSnippets:
		return fmt.Sprintf("%d+ results", listLimit)
	}
	return fmt.Sprintf("%d results", n)
}

// highlighted shows s with what the shown search matches in it in bold on
// hl.
func (a *app) highlighted(c *ui.Context, s string, hl ui.Color) ui.Element {
	rs := search.Highlight(s, a.shown.Terms)
	if len(rs) == 0 {
		return ui.Text(c, s)
	}
	spans := make([]ui.Span, 0, 2*len(rs)+1)
	at := 0
	for _, r := range rs {
		if r.Start > at {
			spans = append(spans, ui.Span{Text: s[at:r.Start]})
		}
		spans = append(spans, ui.Span{Text: s[r.Start:r.End], Weight: 700, Background: hl, Radius: 3})
		at = r.End
	}
	if at < len(s) {
		spans = append(spans, ui.Span{Text: s[at:]})
	}
	return ui.RichText(c, spans...)
}

// emptyState fills an empty list with an icon, a title and tips.
func emptyState(c *ui.Context, icon *ui.SVG, title string, tips ...string) {
	t := c.Theme()
	ui.Column(c).Padding(24, 16).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(28).TextColor(t.TextMuted)
		ui.Text(c, title).Bold().Wrap().TextAlign(ui.Center)
		for _, tip := range tips {
			ui.Text(c, tip).FontSize(11).TextColor(t.TextMuted).Wrap().TextAlign(ui.Center)
		}
	})
}

// tabEmpty is the icon, title and tip of each history tab when it is empty
// and nothing is searched, in tabLabels order.
var tabEmpty = []struct {
	icon       *ui.SVG
	title, tip string
}{
	{iconClipboard, "Nothing copied yet", "Copy something and it will appear here."},
	{iconPin, "Nothing pinned", "Pin entries you reuse with " + cmdSym() + "P or their context menu."},
	{iconClipboard, "No text yet", "Copied text and colors appear here."},
	{iconLink, "No links yet", "Copied web addresses appear here."},
	{iconImage, "No images yet", "Copied images and screenshots appear here."},
	{iconFile, "No files yet", "Files copied in the file manager appear here."},
}

// noMatches fills a list the search found nothing for.
func (a *app) noMatches(c *ui.Context) {
	tips := []string{"Try fewer or shorter words."}
	if a.shown.HasFilters() {
		tips = append(tips, "Or remove a filter: type:, is:, after:, before:.")
	}
	if a.tab != 0 && a.tab != tabSnippets {
		tips = append(tips, "Or search All instead of "+tabLabels[a.tab]+".")
	}
	tips = append(tips, "",
		`"exact phrase" · is:pinned · is:unpinned`,
		"type:text, link, color, image or file",
		"after:/before: 7d, today, 2026-01-31")
	emptyState(c, iconSearch, "No matches for “"+strings.TrimSpace(a.query)+"”", tips...)
}

func (a *app) historyList(c *ui.Context) {
	a.list.Label = func(i int) string { return preview(a.items[i]) }
	a.list.Selected = &a.chosen
	now := time.Now()
	list := ui.List(c.Key("history"), &a.list, len(a.items)).
		ItemKey(func(i int) any { return a.items[i].ID }).
		Selection(&a.selected).
		Grow(1).Padding(6).Children(func() {
		switch {
		case len(a.items) > 0:
		case !a.shown.IsZero():
			a.noMatches(c)
		default:
			e := tabEmpty[a.tab]
			tips := []string{e.tip}
			if a.tab == 0 && a.shortcut != "" {
				tips = append(tips, keys(a.shortcut)+" opens this panel.")
			}
			emptyState(c, e.icon, e.title, tips...)
		}
	}).Rows(func(r ui.ListRow) { a.row(r, now) })
	if list.Changed() {
		a.loadPreview()
	}
	if list.Submitted() {
		a.copyChosen(false)
	}
	a.runPending()
}

// runPending applies what a context menu chose, once the list is built.
func (a *app) runPending() {
	if fn := a.pending; fn != nil {
		a.pending = nil
		fn()
	}
}

// row builds one history row; a choice in its context menu is noted in
// a.pending, to run once the list is built.
func (a *app) row(r ui.ListRow, now time.Time) {
	c, i := r.Context, r.Index
	e := a.items[i]
	t := c.Theme()
	on := r.Selected() || i == a.chosen // the keyboard moves a.chosen without filling the selection set
	bg := ui.Transparent
	text, muted, hl := t.Text, t.TextMuted, t.Accent.Alpha(0.22)
	if on {
		bg = t.Accent.Alpha(0.85)
		text, muted, hl = t.AccentText, t.AccentText.Alpha(0.75), t.AccentText.Alpha(0.28)
	}
	title, detail := rowLines(e)
	row := ui.Row(c).Padding(6, 10).Gap(10).AlignItems(ui.Center).Radius(8).Background(bg).Children(func() {
		a.leading(c, e, muted)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			a.highlighted(c, title, hl).SingleLine().TextColor(text)
			a.highlighted(c, detail, hl).FontSize(11).SingleLine().TextColor(muted)
		})
		ui.Column(c).Gap(4).AlignItems(ui.End).Children(func() {
			ui.Text(c, relTime(now, e.UsedAt)).FontSize(11).TextColor(muted)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if e.Pinned {
					ui.Icon(c, iconPin).FontSize(12).TextColor(muted)
				}
				if i < 9 {
					ui.Text(c, fmt.Sprintf("%s%d", cmdSym(), i+1)).FontSize(10).TextColor(muted)
				}
			})
		})
	})
	row.ContextMenu(func(m *ui.Menu) {
		ids := a.selectedIDs()
		if len(ids) > 1 && !a.selected.Has(e.ID) {
			ids = []int64{e.ID}
		}
		if len(ids) > 1 {
			if m.Item(fmt.Sprintf("Copy %d Merged", len(ids))).Chosen() {
				a.pending = func() { a.copyMerged(ids) }
			}
			if m.Item(fmt.Sprintf("Delete %d", len(ids))).Chosen() {
				a.pending = func() { a.deleteEntries(ids) }
			}
			m.Separator()
		}
		if m.Item("Copy").Chosen() {
			a.pending = func() { a.copyEntry(e, false) }
		}
		if m.Item("Copy as Plain Text").Chosen() {
			a.pending = func() { a.copyEntry(e, true) }
		}
		if e.Kind == history.KindText || e.Kind == history.KindLink || e.Kind == history.KindColor {
			m.Submenu("Transform", func(m *ui.Menu) {
				for _, tr := range transform.All {
					if m.Item(tr.Name).Chosen() {
						a.pending = func() { a.copyTransformed(e, tr) }
					}
				}
			})
		}
		label := "Pin"
		if e.Pinned {
			label = "Unpin"
		}
		if m.Item(label).Chosen() {
			a.pending = func() { a.setPinned(e.ID, !e.Pinned) }
		}
		if e.Kind != history.KindImage && m.Item("Save as Snippet…").Chosen() {
			a.pending = func() { a.saveAsSnippet(e) }
		}
		if e.Kind == history.KindLink && m.Item("Open Link").Chosen() {
			a.pending = func() { openLink(strings.TrimSpace(e.Text)) }
		}
		if e.Kind == history.KindFiles && m.Item("Show in Folder").Chosen() {
			a.pending = func() { showInFolder(strings.Split(e.Text, "\n")[0]) }
		}
		m.Separator()
		if m.Item("Delete").Chosen() {
			a.pending = func() { a.deleteEntries([]int64{e.ID}) }
		}
	})
}

func openLink(u string) {
	if err := mygo.Shell.OpenExternal(u); err != nil {
		slog.Error("open link", "err", err)
	}
}

func showInFolder(p string) { mygo.Shell.ShowItemInFolder(p) }

func (a *app) previewPane(c *ui.Context) {
	t := c.Theme()
	e, ok := a.chosenEntry()
	if !ok {
		return
	}
	if a.preview.ID == e.ID {
		e = a.preview
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, kindIcon(e.Kind)).FontSize(16).TextColor(t.TextMuted)
		ui.Text(c, kindTitle(e)).Bold().SingleLine().Grow(1)
		ui.Button(c, "Copy").Tooltip("Copy (↵)").OnClick(func() { a.copyEntry(e, false) })
		pin := "Pin"
		if e.Pinned {
			pin = "Unpin"
		}
		ui.Button(c, pin).Tooltip(pin + " (" + cmdSym() + "P)").OnClick(func() { a.setPinned(e.ID, !e.Pinned) })
		ui.Button(c, "Delete").Tooltip("Delete (" + cmdSym() + "⌫)").OnClick(func() { a.deleteEntries([]int64{e.ID}) })
	})
	ui.Text(c, details(e, time.Now())).FontSize(11).TextColor(t.TextMuted).Wrap()
	ui.Divider(c)
	hl := t.Accent.Alpha(0.22)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			switch e.Kind {
			case history.KindImage:
				if a.previewBitmap != nil {
					ui.Image(c, a.previewBitmap).Fit(ui.Contain).Height(300)
				} else if b := a.thumb(e); b != nil {
					ui.Image(c, b).Fit(ui.Contain).Height(300)
				}
			case history.KindFiles:
				for _, p := range strings.Split(e.Text, "\n") {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						a.highlighted(c, p, hl).Wrap().Grow(1).Selectable()
						ui.Button(c, "Show in Folder").OnClick(func() { showInFolder(p) })
					})
				}
			default:
				text := e.Text
				if n := utf8.RuneCountInString(text); n > 20000 {
					text = string([]rune(text)[:20000]) + fmt.Sprintf("… (%d more characters)", n-20000)
				}
				a.highlighted(c, text, hl).Wrap().Selectable()
				if e.Kind == history.KindLink && ui.Button(c, "Open Link").Clicked() {
					openLink(strings.TrimSpace(e.Text))
				}
			}
		})
	})
}
