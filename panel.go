package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

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
			if a.tab == tabSnippets && len(a.snippets) > 0 {
				if iconButton(c, iconPlus, "Add Snippet", "New Snippet ("+cmdSym()+"N)", t.TextMuted).Clicked() {
					a.newSnippet("", "")
				}
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
		empty := len(a.items) == 0
		if a.tab == tabSnippets {
			empty = len(a.snippets) == 0
		}
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			list := ui.Column(c)
			if empty {
				list.Grow(1)
			} else {
				list.Width(340).Padding(0, 0, 0, 4)
			}
			list.Children(func() {
				if a.tab == tabSnippets {
					a.snippetList(c)
				} else {
					a.historyList(c)
				}
			})
			if empty {
				return
			}
			ui.Column(c).Grow(1).Margin(0, 12, 12, 4).Padding(14).Gap(10).Radius(12).Background(t.Text.Alpha(0.04)).Children(func() {
				if a.tab == tabSnippets {
					a.snippetPreview(c)
				} else {
					a.previewPane(c)
				}
			})
		})
		ui.Row(c).Padding(8, 16).Gap(12).AlignItems(ui.Center).Background(t.Text.Alpha(0.03)).Children(func() {
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
