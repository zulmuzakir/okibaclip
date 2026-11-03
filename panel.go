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

	"okibaclip/internal/history"
	"okibaclip/internal/search"
	"okibaclip/internal/transform"
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
	a.preview, a.previewBitmap, a.previewStats, a.previewCode = history.Entry{}, nil, "", false
	a.requery()
}

func (a *app) copyChosen(plain bool) {
	if a.listStale {
		a.afterList = func() { a.copyChosen(plain) }
		return
	}
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
	if a.listStale {
		a.afterList = func() { a.copyNth(n) }
		return
	}
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
	a.editor.folder, a.editor.shortcut = "", ""
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
	acc := strings.TrimSpace(a.editor.shortcut)
	if acc != "" {
		if strings.EqualFold(acc, a.shortcut) {
			a.editor.err = "That shortcut opens the panel"
			return
		}
		if id, ok := a.snipKeys[acc]; ok && id != a.editor.id {
			a.editor.err = "That shortcut belongs to another snippet"
			return
		}
	}
	sn := history.Snippet{ID: a.editor.id, Name: name, Body: a.editor.body, Folder: firstRunes(strings.TrimSpace(a.editor.folder), 100), Shortcut: acc}
	if a.editor.saving {
		return
	}
	old := ""
	for k, id := range a.snipKeys {
		if id == a.editor.id {
			old = k
		}
	}
	newly := acc != "" && acc != old
	if newly {
		if err := a.hotkeys.Register(acc, func() { a.post(func() { a.fireSnippet(acc) }) }); err != nil {
			a.editor.err = "Shortcut unavailable: " + err.Error()
			return
		}
	}
	a.editor.saving, a.editor.err = true, ""
	a.async(func() {
		id, err := a.store.SaveSnippet(a.ctx, sn)
		a.post(func() {
			a.editor.saving = false
			if err != nil {
				slog.Error("save snippet", "err", err)
				a.editor.err = "Save failed: " + err.Error()
				if newly {
					a.hotkeys.Unregister(acc)
				}
				return
			}
			if old != "" && old != acc {
				a.hotkeys.Unregister(old)
				delete(a.snipKeys, old)
			}
			if acc != "" {
				a.snipKeys[acc] = id
			}
			a.editor.open = false
			a.changed()
		})
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
			ui.Icon(c, iconSearch).FontSize(16).TextColor(t.TextMuted)
			// The field takes Enter, ⌘⌫, and ←/→ while empty, before the panel's shortcuts see them.
			in := ui.TextInputBase(c, &a.query).Bind(&a.search).Grow(1).FontSize(16).
				Placeholder("Search clipboard…").Label("Search clipboard").
				OnTextKey(func(k ui.TextKey) bool {
					switch {
					case k.Key == ui.KeyEnter:
						a.copyChosen(k.Mods&ui.Shift != 0)
						return true
					case k.Mods == ui.Cmd && k.Key == ui.KeyBackspace:
						a.deleteChosen()
						return true
					case k.Mods == 0 && (k.Key == ui.KeyLeft || k.Key == ui.KeyRight) && k.Text() == "":
						// An empty field has no caret to move: the arrows switch filters.
						if k.Key == ui.KeyLeft {
							a.setTab(a.tab - 1)
						} else {
							a.setTab(a.tab + 1)
						}
						return true
					}
					return false
				})
			if in.Changed() {
				a.chosen, a.chosenSnippet = 0, 0
				a.selected.Clear()
				a.requery()
			}
			if count := a.resultCount(); count != "" {
				ui.Text(c, count).FontSize(12).TextColor(t.TextMuted)
			}
			help := iconButton(c, iconInfo, "Search syntax", "Search syntax", t.TextMuted)
			if help.Clicked() {
				a.syntaxOpen = !a.syntaxOpen
				if !a.syntaxOpen {
					a.search.Focus()
				}
			}
			if ui.Popover(c, help, &a.syntaxOpen, func() { a.syntaxHelp(c) }).Dismissed() {
				a.search.Focus()
			}
		})
		a.filterBar(c)
		a.banners(c)
		ui.Divider(c)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(340).Children(func() {
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
		ui.Row(c).Padding(8, 16).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.RichText(c, a.hintSpans(t)...).FontSize(11).SingleLine().Grow(1)
			if s := a.footerStatus(); s != "" {
				ui.Text(c, s).FontSize(11).TextColor(t.TextMuted).SingleLine()
			}
		})
	})
	a.editorModal(c)
	a.entryEditModal(c)
}

func (a *app) shortcuts(c *ui.Context) {
	if a.editor.open || a.edit.open {
		return
	}
	if a.query != "" && !a.syntaxOpen && c.Shortcut(0, ui.KeyEscape) {
		a.query = ""
		a.chosen, a.chosenSnippet = 0, 0
		a.requery()
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
	c.OnShortcut(ui.Cmd, ui.KeyBackspace, a.deleteChosen)
	c.OnShortcut(ui.Ctrl, ui.KeyTab, func() { a.setTab(a.tab + 1) })
	c.OnShortcut(ui.Ctrl|ui.Shift, ui.KeyTab, func() { a.setTab(a.tab - 1) })
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { a.search.Focus() })
	c.OnShortcut(ui.Cmd, ui.KeyN, func() { a.newSnippet("", "") })
	c.OnShortcut(ui.Cmd, ui.KeyComma, a.openSettings)
	c.OnShortcut(ui.Cmd, ui.KeyQ, mygo.App.Quit)
}

// deleteChosen deletes the chosen snippet, or the selected or chosen entries.
func (a *app) deleteChosen() {
	if s, ok := a.chosenSnip(); ok {
		a.deleteSnippet(s)
		return
	}
	a.deleteEntries(a.selectedIDs())
}

func (a *app) deleteSnippet(s history.Snippet) {
	for acc, id := range a.snipKeys {
		if id == s.ID {
			a.hotkeys.Unregister(acc)
			delete(a.snipKeys, acc)
		}
	}
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
	if a.waylandMode == "wayland" && !a.waylandCapture {
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
		if history.TextKind(e.Text) == history.KindColor {
			// Imports can carry an invalid color; ui.Hex panics on it.
			ui.Box(c).Size(28, 28).Radius(6).Background(ui.Hex(strings.TrimSpace(e.Text))).Border(1, c.Theme().Border)
			return
		}
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

// chipIcons is the icon of each filter chip, nil for none, in tabLabels order.
var chipIcons = []*ui.SVG{nil, iconPin, nil, nil, nil, nil, iconSnippet}

// syntaxRows is the search syntax help: what to type and what it does.
var syntaxRows = [][2]string{
	{"words", "Every word must match; 3+ letters also match fuzzily"},
	{`"exact phrase"`, "Match these words exactly, in order"},
	{"type:link", "text, link, color, image or file"},
	{"is:pinned", "or is:unpinned"},
	{"after:7d", "Last used since: 12h, 3d, 2w, today, yesterday, 2026-01-31"},
	{"before:2026-01-31", "Last used before that day"},
	{"app:slack", "Copied from that app (name, bundle ID or executable)"},
}

// iconButton is a borderless button showing icon in fg, named label for
// assistive technology (and Tester.Click), with tip as its tooltip.
func iconButton(c *ui.Context, icon *ui.SVG, label, tip string, fg ui.Color) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Padding(6).Radius(6)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, icon).Size(16, 16).TextColor(fg) })
	return b
}

// rowStyle paints a list row: the chosen row gets an accent tint and an
// accent bar on its left edge, other rows of a multi-selection a lighter
// tint, a hovered row the theme's hover face. It returns the color to
// highlight search matches with. Row text keeps the theme's Text and
// TextMuted colors in every state.
func rowStyle(c *ui.Context, row ui.Element, chosen, selected bool) (hl ui.Color) {
	t := c.Theme()
	switch {
	case chosen:
		accent := t.Accent
		row.Background(t.Accent.Alpha(0.16)).DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X, Y: r.Y + 8, W: 3, H: r.H - 16}, accent, 1.5)
		})
		return t.Accent.Alpha(0.35)
	case selected:
		row.Background(t.Accent.Alpha(0.10))
		return t.Accent.Alpha(0.35)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	return t.Accent.Alpha(0.22)
}

// filterBar shows the history filters as chips, with Snippets set apart
// at the end. It is a tab list: arrows, Home and End move the choice
// while a chip has the focus.
func (a *app) filterBar(c *ui.Context) {
	t := c.Theme()
	tabs := ui.TabsBase(c, &a.tab, len(tabLabels))
	tabs.List.FillWidth().Padding(2, 16, 10, 16).Gap(6).AlignItems(ui.Center).Children(func() {
		for i, label := range tabLabels {
			if i == tabSnippets {
				ui.Box(c).Grow(1)
			}
			on := i == a.tab
			chip := tabs.Tab(i).Padding(3, 10).Radius(999).Gap(5).AlignItems(ui.Center).Border(1, t.Border)
			fg := t.TextMuted
			switch {
			case on:
				chip.Background(t.Accent.Alpha(0.16)).BorderColor(t.Accent.Alpha(0.6))
				fg = t.Text
			case chip.Hovered():
				chip.Background(t.SurfaceHover)
				fg = t.Text
			}
			chip.Children(func() {
				if icon := chipIcons[i]; icon != nil {
					ui.Icon(c, icon).Size(13, 13).TextColor(fg)
				}
				txt := ui.Text(c, label).FontSize(12).SingleLine().TextColor(fg)
				if on {
					txt.FontWeight(600)
				}
			})
		}
	})
	if tabs.List.Changed() {
		a.setTab(a.tab)
	}
}

// syntaxHelp is the content of the search syntax popover.
func (a *app) syntaxHelp(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(320).Padding(12).Gap(6).Children(func() {
		ui.Text(c, "Search syntax").Bold().FontSize(13)
		for _, r := range syntaxRows {
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, r[0]).Font("monospace").FontSize(12).Width(120)
				ui.Text(c, r[1]).FontSize(12).TextColor(t.TextMuted).Wrap().Grow(1)
			})
		}
	})
}

// hintSpans is the footer's key hints, each key as a keycap.
func (a *app) hintSpans(t *ui.Theme) []ui.Span {
	cm := cmdSym()
	filter := [2]string{"←→", "Filter"}
	if a.query != "" {
		filter[0] = keys("Ctrl+Tab")
	}
	pairs := [][2]string{{"↵", "Copy"}, {"⇧↵", "Plain"}, filter, {cm + "P", "Pin"}, {cm + "⌫", "Delete"}, {"esc", "Close"}}
	switch {
	case a.tab == tabSnippets:
		pairs = [][2]string{{"↵", "Copy"}, filter, {cm + "N", "New"}, {cm + "⌫", "Delete"}, {"esc", "Close"}}
	case a.selected.Len() > 1:
		pairs[0][1] = "Copy merged"
	}
	var spans []ui.Span
	for i, p := range pairs {
		action := " " + p[1]
		if i < len(pairs)-1 {
			action += "    "
		}
		spans = append(spans,
			ui.Span{Text: " " + p[0] + " ", Size: 10, Weight: 600, Color: t.Text, Background: t.Text.Alpha(0.08), Radius: 4},
			ui.Span{Text: action, Color: t.TextMuted})
	}
	return spans
}

// footerStatus is the footer's right-hand note, or empty.
func (a *app) footerStatus() string {
	switch {
	case a.tab != tabSnippets && a.selected.Len() > 1:
		return fmt.Sprintf("%d selected", a.selected.Len())
	case a.footerNote != "":
		return a.footerNote
	case a.shortcut != "":
		return keys(a.shortcut) + " to show"
	}
	return ""
}

// noMatches fills a list the search found nothing for.
func (a *app) noMatches(c *ui.Context) {
	tips := []string{"Try fewer or shorter words."}
	if a.shown.HasFilters() {
		tips = append(tips, "Or remove a filter: type:, is:, app:, after:, before:.")
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
	a.list.Reorder = nil
	if a.tab == tabPinned && a.query == "" {
		a.list.Reorder = func(rows []int, to int) { a.pending = func() { a.reorderPinned(rows, to) } }
	}
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
	title, detail := rowLines(e)
	row := ui.Row(c).Padding(6, 10).Gap(10).AlignItems(ui.Center).Radius(8)
	hl := rowStyle(c, row, i == a.chosen, r.Selected()) // the keyboard moves a.chosen without filling the selection set
	text, muted := t.Text, t.TextMuted
	row.Children(func() {
		a.leading(c, e, muted)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			a.highlighted(c, title, hl).SingleLine().TextColor(text)
			a.highlighted(c, detail, hl).FontSize(11).SingleLine().TextColor(muted)
		})
		ui.Column(c).Gap(4).AlignItems(ui.End).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				if b := a.appIcons[e.SourceApp]; b != nil {
					ui.Image(c, b).Size(12, 12)
				}
				ui.Text(c, relTime(now, e.UsedAt)).FontSize(11).TextColor(muted)
			})
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
	row.ContextMenu(func(m *ui.Menu) { a.entryMenu(m, e) })
}

// entryMenu fills e's menu, shared by its row's context menu and the
// preview's More actions button. What it does applies to the
// multi-selection when e is in one. A choice is noted in a.pending, to
// run once the view is built.
func (a *app) entryMenu(m *ui.Menu, e history.Entry) {
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
	if e.Kind == history.KindText || e.Kind == history.KindLink || e.Kind == history.KindColor {
		if m.Item("Edit…").Chosen() {
			a.pending = func() { a.openEntryEdit(e, false) }
		}
	}
	label := "Pin"
	if e.Pinned {
		label = "Unpin"
	}
	if m.Item(label).Chosen() {
		a.pending = func() { a.setPinned(e.ID, !e.Pinned) }
	}
	if e.Pinned && m.Item("Rename…").Chosen() {
		a.pending = func() { a.openEntryEdit(e, true) }
	}
	if e.Kind == history.KindImage {
		if m.Item("Save Image As…").Chosen() {
			a.pending = func() { a.saveImage(e) }
		}
		if m.Item("Copy as File").Chosen() {
			a.pending = func() { a.copyImageFile(e) }
		}
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
}

func openLink(u string) {
	if history.TextKind(u) != history.KindLink {
		slog.Warn("open link: not an http(s) URL")
		return
	}
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
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, kindIcon(e.Kind)).FontSize(14).TextColor(t.TextMuted)
		ui.Text(c, kindTitle(e)).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1)
		pin, pinFg := "Pin", t.TextMuted
		if e.Pinned {
			pin, pinFg = "Unpin", t.Accent
		}
		iconButton(c, iconPin, pin, pin+" ("+cmdSym()+"P)", pinFg).OnClick(func() { a.setPinned(e.ID, !e.Pinned) })
		iconButton(c, iconMore, "More actions", "More actions", t.TextMuted).Menu(func(m *ui.Menu) { a.entryMenu(m, e) })
		ui.PrimaryButton(c, "Copy").Tooltip("Copy (↵)").OnClick(func() { a.copyEntry(e, false) })
	})
	ui.Divider(c)
	hl := t.Accent.Alpha(0.22)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			switch e.Kind {
			case history.KindImage:
				if a.preview.ID == e.ID && a.previewBitmap != nil {
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
			case history.KindColor:
				// ui.Hex panics on bad input, and imports can carry an invalid color.
				if history.TextKind(e.Text) != history.KindColor {
					a.textBody(c, e, hl)
					break
				}
				col := ui.Hex(strings.TrimSpace(e.Text))
				hex, rgb := colorValues(col)
				ui.Box(c).FillWidth().Height(120).Radius(10).Background(col).Border(1, t.Border)
				ui.Text(c, hex).Font("monospace").FontSize(13).Selectable()
				ui.Text(c, rgb).Font("monospace").FontSize(13).Selectable()
			default:
				a.textBody(c, e, hl)
			}
		})
	})
	stats := ""
	if a.preview.ID == e.ID {
		stats = a.previewStats
	}
	ui.Text(c, details(e, stats, time.Now())).FontSize(11).TextColor(t.TextMuted).Wrap()
	// A More actions choice is reported while this pane builds, after the
	// list already ran its pending work.
	a.runPending()
}

// textBody shows e's text, capped, in monospace when it reads as code, and
// an Open Link button for links.
func (a *app) textBody(c *ui.Context, e history.Entry, hl ui.Color) {
	text := e.Text
	if n := utf8.RuneCountInString(text); n > 20000 {
		text = string([]rune(text)[:20000]) + fmt.Sprintf("… (%d more characters)", n-20000)
	}
	h := a.highlighted(c, text, hl)
	if a.preview.ID == e.ID && a.previewCode {
		h.Font("monospace").FontSize(12)
	}
	h.Wrap().Selectable()
	if e.Kind == history.KindLink && ui.Button(c, "Open Link").Clicked() {
		openLink(strings.TrimSpace(e.Text))
	}
}
