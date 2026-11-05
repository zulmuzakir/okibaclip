package main

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"okibaclip/internal/accel"
	"okibaclip/internal/history"
)

func (a *app) snippetList(c *ui.Context) {
	t := c.Theme()
	a.snipList.Label = func(i int) string { return a.snippets[i].Name }
	a.snipList.Selected = &a.chosenSnippet
	a.folderSelect(c)
	ui.Themed(c, a.listTheme(t)).Background(ui.Transparent).Grow(1).Children(func() {
		list := ui.List(c.Key("snippets"), &a.snipList, len(a.snippets)).
			ItemKey(func(i int) any { return a.snippets[i].ID }).
			Grow(1).Padding(6).Children(func() {
			switch {
			case len(a.snippets) > 0:
			case len(a.shown.Terms) > 0:
				a.noMatches(c)
			default:
				emptyState(c, iconSnippet, "No snippets yet", "Press "+cmdSym()+"N to create one, or right-click a history entry and choose Save as Snippet…")
				ui.Themed(c, t).Background(ui.Transparent).FillWidth().AlignItems(ui.Center).Children(func() {
					if ui.PrimaryButton(c, "Create Snippet").Clicked() {
						a.newSnippet("", "")
					}
				})
			}
		}).Rows(func(r ui.ListRow) {
			c, i := r.Context, r.Index
			s := a.snippets[i]
			row := ui.Row(c).Padding(8, 10).Gap(12).AlignItems(ui.Center).Radius(10)
			hl := rowStyle(t, row, i == a.chosenSnippet, r.Selected())
			text, muted := t.Text, t.TextMuted
			row.Children(func() {
				ui.Icon(c, iconSnippet).FontSize(20).TextColor(muted).Size(28, 28)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					a.highlighted(c, s.Name, hl).SingleLine().TextColor(text)
					a.highlighted(c, textPreview(s.Body, 80), hl).FontSize(11).SingleLine().TextColor(muted)
				})
				if _, _, ok := a.snippetHotkey(s.ID); ok {
					ui.Text(c, keys(s.Shortcut)).FontSize(10).TextColor(muted)
				}
			})
			row.ContextMenu(func(m *ui.Menu) { a.snippetMenu(m, s) })
		})
		if list.Submitted() {
			a.copyChosen(false)
		}
	})
	a.runPending()
}

// snippetMenu fills s's menu, shared by its row's context menu and the
// preview's More actions button. A choice is noted in a.pending.
func (a *app) snippetMenu(m *ui.Menu, s history.Snippet) {
	if m.Item("Copy").Chosen() {
		a.pending = func() { a.copySnippet(s, false) }
	}
	if m.Item("Edit…").Chosen() {
		a.pending = func() { a.openSnippetEditor(s) }
	}
	m.Separator()
	if m.Item("Delete").Chosen() {
		a.pending = func() { a.deleteSnippet(s) }
	}
}

// snipPreview caches the snippet preview's display text; it is recomputed only when the chosen snippet or its source changes.
type snipPreview struct {
	id   int64
	src  string
	body string
}

func (a *app) snippetPreview(c *ui.Context) {
	t := c.Theme()
	s, ok := a.chosenSnip()
	if !ok {
		return
	}
	hl := t.Accent.Alpha(0.22)
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconSnippet).FontSize(14).TextColor(t.TextMuted)
		kind := "Snippet"
		if s.Folder != "" {
			kind = s.Folder
		}
		ui.Text(c, kind).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1)
		iconButton(c, iconMore, "More actions", "More actions", t.TextMuted).Menu(func(m *ui.Menu) { a.snippetMenu(m, s) })
		ui.PrimaryButton(c, "Copy").Tooltip("Copy (↵)").OnClick(func() { a.copySnippet(s, false) })
	})
	a.highlighted(c, s.Name, hl).Bold().FontSize(15)
	src := strings.TrimRight(s.Body, "\n")
	if a.snipPrev.id != s.ID || a.snipPrev.src != src {
		head, more := truncateRunes(src, 20000)
		if more > 0 {
			head += fmt.Sprintf("… (%d more characters)", more)
		}
		a.snipPrev = snipPreview{id: s.ID, src: src, body: head}
	}
	ui.Scroll(c).Grow(1).Children(func() {
		a.highlighted(c, a.snipPrev.body, hl).Wrap().Selectable()
	})
	ui.Text(c, a.snippetDetails(s)).FontSize(11).TextColor(t.TextMuted).Wrap()
	// A More actions choice is reported while this pane builds.
	a.runPending()
}

// snippetDetails is the preview's footnote: length, the global shortcut
// when registered, and how often it was used.
func (a *app) snippetDetails(s history.Snippet) string {
	parts := []string{fmt.Sprintf("%d characters", utf8.RuneCountInString(s.Body))}
	if _, _, ok := a.snippetHotkey(s.ID); ok {
		parts = append(parts, "shortcut "+keys(s.Shortcut))
	}
	switch {
	case s.UseCount == 1:
		parts = append(parts, "used once")
	case s.UseCount > 1:
		parts = append(parts, fmt.Sprintf("used %d times", s.UseCount))
	}
	return strings.Join(parts, " · ")
}

func (a *app) editorModal(c *ui.Context) {
	t := c.Theme()
	if !a.editor.open {
		return
	}
	ui.Modal(c, &a.editor.open, func() {
		ui.Themed(c, a.fieldTheme(t)).Background(ui.Transparent).Width(440).Padding(16).Gap(8).Children(func() {
			title := "New Snippet"
			if a.editor.id != 0 {
				title = "Edit Snippet"
			}
			ui.Text(c, title).Bold().FontSize(16)
			ui.Field(c, "Name", func() {
				ui.TextInput(c, &a.editor.name).Label("Name").Placeholder("e.g. Email sign-off").Width(408)
			})
			ui.Field(c, "Body", func() {
				ui.TextArea(c, &a.editor.body).Label("Body").Placeholder("Text to paste").Width(408).Height(110)
			}).Description("Placeholders: {date} {time} {datetime} {clipboard} {cursor}")
			ui.Row(c).Gap(12).Children(func() {
				ui.Field(c, "Folder", func() {
					ui.TextInput(c, &a.editor.folder).Label("Folder").Placeholder("None").Width(198)
				})
				ui.Field(c, "Shortcut", func() {
					ui.TextInput(c, &a.editor.shortcut).Label("Shortcut").Placeholder("e.g. CmdOrCtrl+Alt+1").Width(198)
				})
			})
			if a.editor.err != "" {
				ui.Text(c, a.editor.err).TextColor(t.Danger).FontSize(12)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if softButton(c, "Cancel", t.Text).Clicked() {
					a.editor.open, a.editor.err = false, ""
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
					a.saveEditor()
				}
			})
		})
	})
}

// folderSelect is the folder filter above the snippet list; it shows only
// when some snippet has a folder.
func (a *app) folderSelect(c *ui.Context) {
	if len(a.snipFolders) == 0 {
		return
	}
	if a.snipFolderSel == "" {
		a.snipFolderSel = allFolders
	}
	opts := append(append([]string{allFolders}, a.snipFolders...), noFolder)
	ui.Row(c).Padding(4, 6, 2, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconFolder).Size(14, 14).TextColor(c.Theme().TextMuted)
		if ui.Select(c, &a.snipFolderSel, opts).Label("Folder").Grow(1).Changed() {
			a.snipFolder = a.snipFolderSel
			if a.snipFolder == allFolders {
				a.snipFolder = ""
			}
			a.chosenSnippet = 0
			a.requery()
		}
	})
}

// fireSnippet copies the snippet registered under key (an accel.Key form).
// Hotkey callbacks already run on the main thread.
func (a *app) fireSnippet(key string) {
	h, ok := a.snipKeys[key]
	if !ok {
		return
	}
	a.rememberTarget() // the panel is usually hidden: remember the app in front now
	a.async(func() {
		s, err := a.store.Snippet(a.ctx, h.id)
		if err != nil {
			return
		}
		a.copySnippet(s, false)
	})
}

// registerSnippetShortcuts (re)registers every snippet's global shortcut.
// Failures are logged and leave the snippet without one.
func (a *app) registerSnippetShortcuts() {
	for _, h := range a.snipKeys {
		a.hotkeys.Unregister(h.acc)
	}
	clear(a.snipKeys)
	a.async(func() {
		snips, err := a.store.Snippets(a.ctx, nil)
		if err != nil {
			if a.ctx.Err() == nil {
				slog.Error("snippet shortcuts", "err", err)
			}
			return
		}
		a.post(func() {
			for _, s := range snips {
				acc := s.Shortcut
				if acc == "" {
					continue
				}
				key := accel.Key(acc)
				if err := accel.Valid(acc); err != nil {
					slog.Warn("snippet shortcut", "accelerator", acc, "err", err)
					continue
				}
				if _, dup := a.snipKeys[key]; dup || key == accel.Key(a.shortcut) {
					slog.Warn("snippet shortcut", "accelerator", acc, "err", "already in use")
					continue
				}
				if err := a.hotkeys.Register(acc, func() { a.fireSnippet(key) }); err != nil {
					slog.Warn("snippet shortcut", "accelerator", acc, "err", err)
					continue
				}
				a.snipKeys[key] = snipHotkey{acc: acc, id: s.ID}
			}
			a.refreshTray()
		})
	})
}

func (a *app) openSnippetEditor(s history.Snippet) {
	if a.tab != tabSnippets {
		a.setTab(tabSnippets)
	}
	a.editor.open, a.editor.id, a.editor.name, a.editor.body, a.editor.err = true, s.ID, s.Name, s.Body, ""
	a.editor.folder, a.editor.shortcut = s.Folder, s.Shortcut
}

// newSnippet opens the editor for a new snippet prefilled with name and body.
func (a *app) newSnippet(name, body string) {
	a.openSnippetEditor(history.Snippet{Name: name, Body: body})
}

func (a *app) saveAsSnippet(e history.Entry) {
	a.async(func() {
		full, err := a.store.Get(a.ctx, e.ID)
		if err != nil {
			slog.Error("save as snippet", "err", err)
			a.post(func() { a.toast = "Could not read entry: " + err.Error() })
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
	name := firstRunes(strings.TrimSpace(a.editor.name), history.MaxSnippetNameRunes)
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
	key := accel.Key(acc)
	if key != "" {
		if err := accel.Valid(acc); err != nil {
			a.editor.err = err.Error()
			return
		}
		if key == accel.Key(a.shortcut) {
			a.editor.err = "That shortcut opens the panel"
			return
		}
		if h, ok := a.snipKeys[key]; ok && h.id != a.editor.id {
			a.editor.err = "That shortcut belongs to another snippet"
			return
		}
	}
	sn := history.Snippet{ID: a.editor.id, Name: name, Body: a.editor.body, Folder: firstRunes(strings.TrimSpace(a.editor.folder), history.MaxSnippetFolderRunes), Shortcut: acc}
	if a.editor.saving {
		return
	}
	oldKey, old, hadOld := a.snippetHotkey(a.editor.id)
	newly := key != "" && key != oldKey
	if newly {
		if err := a.hotkeys.Register(acc, func() { a.fireSnippet(key) }); err != nil {
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
			if hadOld && oldKey != key {
				a.hotkeys.Unregister(old.acc)
				delete(a.snipKeys, oldKey)
			}
			if key != "" {
				h := snipHotkey{acc: acc, id: id}
				if !newly {
					h.acc = old.acc // still registered under the spelling it was given
				}
				a.snipKeys[key] = h
			}
			a.editor.open = false
			a.changed()
		})
	})
}
