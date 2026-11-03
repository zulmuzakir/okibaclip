package main

import (
	"log/slog"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"okibaclip/internal/history"
)

// openEntryEdit opens the entry-text modal on e: to rename a pinned entry,
// or to edit its text (fetched in full, as list rows hold only the start).
func (a *app) openEntryEdit(e history.Entry, rename bool) {
	open := func(value string) {
		a.edit.open, a.edit.id, a.edit.rename, a.edit.value, a.edit.err, a.edit.saving = true, e.ID, rename, value, "", false
	}
	if rename {
		open(e.Title)
		return
	}
	a.async(func() {
		full, err := a.store.Get(a.ctx, e.ID)
		if err != nil {
			return
		}
		a.post(func() { open(full.Text) })
	})
}

// saveEntryEdit writes the modal's text or name. andCopy then copies the
// edited entry.
func (a *app) saveEntryEdit(andCopy bool) {
	if a.edit.saving {
		return
	}
	id, rename, v := a.edit.id, a.edit.rename, a.edit.value
	if rename {
		v = firstRunes(strings.TrimSpace(v), 100)
	} else {
		switch {
		case strings.TrimSpace(v) == "":
			a.edit.err = "Text is required"
			return
		case len(v) > history.MaxTextBytes:
			a.edit.err = "Text is over 1 MiB"
			return
		}
	}
	a.edit.saving, a.edit.err = true, ""
	a.async(func() {
		newID := id
		var err error
		if rename {
			err = a.store.SetTitle(a.ctx, id, v)
		} else {
			newID, err = a.store.UpdateText(a.ctx, id, v, time.Now())
		}
		a.post(func() {
			a.edit.saving = false
			if err != nil {
				slog.Error("save entry", "err", err)
				a.edit.err = "Save failed: " + err.Error()
				return
			}
			a.edit.open = false
			a.selected.Clear()
			a.changed()
			if andCopy && !rename {
				a.copyEntry(history.Entry{ID: newID}, false)
			}
		})
	})
}

func (a *app) entryEditModal(c *ui.Context) {
	t := c.Theme()
	if !a.edit.open {
		return
	}
	ui.Modal(c, &a.edit.open, func() {
		ui.Column(c).Width(440).Padding(16).Gap(8).Children(func() {
			title := "Edit Entry"
			if a.edit.rename {
				title = "Rename Pinned Entry"
			}
			ui.Text(c, title).Bold().FontSize(16)
			if a.edit.rename {
				ui.Field(c, "Name", func() {
					ui.TextInput(c, &a.edit.value).Label("Name").Placeholder("Name (empty shows the text)").Width(408)
				})
			} else {
				ui.Field(c, "Text", func() {
					ui.TextArea(c, &a.edit.value).Label("Text").Width(408).Height(200)
				})
			}
			if a.edit.err != "" {
				ui.Text(c, a.edit.err).TextColor(t.Danger).FontSize(12)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.edit.open, a.edit.err = false, ""
				}
				if ui.Button(c, "Save").Clicked() {
					a.saveEntryEdit(false)
				}
				if !a.edit.rename && ui.PrimaryButton(c, "Save & Copy").Clicked() {
					a.saveEntryEdit(true)
				}
			})
		})
	})
}

// reorderPinned applies a drag in the Pinned tab: rows go before row to
// (len(a.items) for the end). The list shows the new order at once; the
// store follows.
func (a *app) reorderPinned(rows []int, to int) {
	isMoved := map[int]bool{}
	var moved []history.Entry
	for _, r := range rows {
		if r >= 0 && r < len(a.items) && !isMoved[r] {
			isMoved[r] = true
			moved = append(moved, a.items[r])
		}
	}
	if len(moved) == 0 {
		return
	}
	out := make([]history.Entry, 0, len(a.items))
	inserted := false
	for i, e := range a.items {
		if isMoved[i] {
			continue
		}
		if !inserted && i >= to {
			out = append(out, moved...)
			inserted = true
		}
		out = append(out, e)
	}
	if !inserted {
		out = append(out, moved...)
	}
	a.items = out
	for i, e := range out {
		if e.ID == moved[0].ID {
			a.chosen = i
			break
		}
	}
	ids := make([]int64, len(out))
	for i, e := range out {
		ids[i] = e.ID
	}
	a.async(func() {
		if err := a.store.ReorderPinned(a.ctx, ids); err != nil {
			slog.Error("reorder", "err", err)
		}
		a.post(a.changed)
	})
}
