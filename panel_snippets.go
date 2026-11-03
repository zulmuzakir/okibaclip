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
		if len(a.snippets) == 0 {
			msg := "No matches"
			if a.query == "" {
				msg = "No snippets yet. Save text you reuse as a snippet."
			}
			ui.Text(c, msg).TextColor(t.TextMuted).Padding(16).Wrap()
		}
	}).Rows(func(r ui.ListRow) {
		c, i := r.Context, r.Index
		s := a.snippets[i]
		on := r.Selected() || i == a.chosenSnippet
		bg, text, muted := ui.Transparent, t.Text, t.TextMuted
		if on {
			bg, text, muted = t.Accent.Alpha(0.85), t.AccentText, t.AccentText.Alpha(0.75)
		}
		row := ui.Row(c).Padding(6, 10).Gap(10).AlignItems(ui.Center).Radius(8).Background(bg).Children(func() {
			ui.Icon(c, iconSnippet).FontSize(20).TextColor(muted).Size(28, 28)
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, s.Name).SingleLine().TextColor(text)
				ui.Text(c, textPreview(s.Body, 80)).FontSize(11).SingleLine().TextColor(muted)
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
	ui.Text(c, s.Name).Bold()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Text(c, strings.TrimRight(s.Body, "\n")).Wrap().Selectable()
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
