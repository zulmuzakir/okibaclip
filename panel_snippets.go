package main

import (
	"log/slog"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) snippetList(c *ui.Context) {
	t := c.Theme()
	a.snipList.Label = func(i int) string { return a.snippets[i].Name }
	a.snipList.Selected = &a.chosenSnippet
	a.folderSelect(c)
	list := ui.List(c.Key("snippets"), &a.snipList, len(a.snippets)).
		ItemKey(func(i int) any { return a.snippets[i].ID }).
		Grow(1).Padding(6).Children(func() {
		switch {
		case len(a.snippets) > 0:
		case len(a.shown.Terms) > 0:
			a.noMatches(c)
		default:
			emptyState(c, iconSnippet, "No snippets yet", "Press "+cmdSym()+"N to create one, or right-click a history entry and choose Save as Snippet…")
			ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, "Create Snippet").Clicked() {
					a.newSnippet("", "")
				}
			})
		}
	}).Rows(func(r ui.ListRow) {
		c, i := r.Context, r.Index
		s := a.snippets[i]
		row := ui.Row(c).Padding(8, 10).Gap(12).AlignItems(ui.Center).Radius(10)
		hl := rowStyle(c, row, i == a.chosenSnippet, r.Selected())
		text, muted := t.Text, t.TextMuted
		row.Children(func() {
			ui.Icon(c, iconSnippet).FontSize(20).TextColor(muted).Size(28, 28)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				a.highlighted(c, s.Name, hl).SingleLine().TextColor(text)
				a.highlighted(c, textPreview(s.Body, 80), hl).FontSize(11).SingleLine().TextColor(muted)
			})
			if s.Shortcut != "" && a.snipKeys[s.Shortcut] == s.ID {
				ui.Text(c, keys(s.Shortcut)).FontSize(10).TextColor(muted)
			}
		})
		row.ContextMenu(func(m *ui.Menu) {
			if m.Item("Copy").Chosen() {
				a.pending = func() { a.copySnippet(s) }
			}
			if m.Item("Edit…").Chosen() {
				a.pending = func() {
					a.editor.open, a.editor.id, a.editor.name, a.editor.body, a.editor.err = true, s.ID, s.Name, s.Body, ""
					a.editor.folder, a.editor.shortcut = s.Folder, s.Shortcut
				}
			}
			m.Separator()
			if m.Item("Delete").Chosen() {
				a.pending = func() { a.deleteSnippet(s) }
			}
		})
	})
	if list.Submitted() {
		a.copyChosen(false)
	}
	a.runPending()
}

func (a *app) snippetPreview(c *ui.Context) {
	s, ok := a.chosenSnip()
	if !ok {
		return
	}
	hl := c.Theme().Accent.Alpha(0.22)
	a.highlighted(c, s.Name, hl).Bold()
	ui.Scroll(c).Grow(1).Children(func() {
		a.highlighted(c, strings.TrimRight(s.Body, "\n"), hl).Wrap().Selectable()
	})
}

func (a *app) editorModal(c *ui.Context) {
	t := c.Theme()
	if !a.editor.open {
		return
	}
	ui.Modal(c, &a.editor.open, func() {
		ui.Column(c).Width(440).Padding(16).Gap(10).Children(func() {
			title := "New Snippet"
			if a.editor.id != 0 {
				title = "Edit Snippet"
			}
			ui.Text(c, title).Bold().FontSize(16)
			ui.TextInput(c, &a.editor.name).Label("Name").Placeholder("Name").Width(408)
			ui.TextArea(c, &a.editor.body).Label("Body").Placeholder("Body").Width(408).Height(160)
			ui.Text(c, "Placeholders: {date} {time} {datetime} {clipboard} {cursor}").FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &a.editor.folder).Label("Folder").Placeholder("No folder").Width(408)
			ui.TextInput(c, &a.editor.shortcut).Label("Shortcut").Placeholder("e.g. CmdOrCtrl+Alt+1").Width(408)
			if a.editor.err != "" {
				ui.Text(c, a.editor.err).TextColor(t.Danger).FontSize(12)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
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
	ui.Row(c).Padding(6, 6, 0, 6).Children(func() {
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

// fireSnippet copies the snippet registered under accelerator acc.
func (a *app) fireSnippet(acc string) {
	id, ok := a.snipKeys[acc]
	if !ok {
		return
	}
	a.paster.Remember() // the panel is usually hidden: remember the app in front now
	a.async(func() {
		s, err := a.store.Snippet(a.ctx, id)
		if err != nil {
			return
		}
		a.post(func() { a.copySnippet(s) })
	})
}

// registerSnippetShortcuts (re)registers every snippet's global shortcut.
// Failures are logged and leave the snippet without one.
func (a *app) registerSnippetShortcuts() {
	for acc := range a.snipKeys {
		a.hotkeys.Unregister(acc)
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
				if _, dup := a.snipKeys[acc]; dup || strings.EqualFold(acc, a.shortcut) {
					slog.Warn("snippet shortcut", "accelerator", acc, "err", "already in use")
					continue
				}
				if err := a.hotkeys.Register(acc, func() { a.post(func() { a.fireSnippet(acc) }) }); err != nil {
					slog.Warn("snippet shortcut", "accelerator", acc, "err", err)
					continue
				}
				a.snipKeys[acc] = s.ID
			}
			a.refreshTray()
		})
	})
}
