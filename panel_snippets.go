package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) snippetList(c *ui.Context) {
	t := c.Theme()
	a.snipList.Label = func(i int) string { return a.snippets[i].Name }
	a.snipList.Selected = &a.chosenSnippet
	list := ui.List(c.Key("snippets"), &a.snipList, len(a.snippets)).
		ItemKey(func(i int) any { return a.snippets[i].ID }).
		Grow(1).Padding(6).Children(func() {
		switch {
		case len(a.snippets) > 0:
		case len(a.shown.Terms) > 0:
			a.noMatches(c)
		default:
			emptyState(c, iconSnippet, "No snippets yet", "Save text you reuse: New Snippet below, "+cmdSym()+"N, or Save as Snippet… on a history entry.")
		}
	}).Rows(func(r ui.ListRow) {
		c, i := r.Context, r.Index
		s := a.snippets[i]
		row := ui.Row(c).Padding(6, 10).Gap(10).AlignItems(ui.Center).Radius(8)
		hl := rowStyle(c, row, i == a.chosenSnippet, r.Selected())
		text, muted := t.Text, t.TextMuted
		row.Children(func() {
			ui.Icon(c, iconSnippet).FontSize(20).TextColor(muted).Size(28, 28)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				a.highlighted(c, s.Name, hl).SingleLine().TextColor(text)
				a.highlighted(c, textPreview(s.Body, 80), hl).FontSize(11).SingleLine().TextColor(muted)
			})
		})
		row.ContextMenu(func(m *ui.Menu) {
			if m.Item("Copy").Chosen() {
				a.pending = func() { a.copySnippet(s) }
			}
			if m.Item("Edit…").Chosen() {
				a.pending = func() {
					a.editor.open, a.editor.id, a.editor.name, a.editor.body, a.editor.err = true, s.ID, s.Name, s.Body, ""
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
	ui.Row(c).Padding(8).Children(func() {
		if ui.Button(c, "New Snippet").Grow(1).Clicked() {
			a.newSnippet("", "")
		}
	})
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
