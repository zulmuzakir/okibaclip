package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"okibaclip/internal/history"
)

// undoDelete restores entries deleted by the toast that offered the undo.
func (a *app) undoDelete(entries []history.Entry) {
	if len(entries) == 0 {
		return
	}
	a.async(func() {
		if err := a.store.Restore(a.ctx, entries); err != nil {
			slog.Error("restore", "err", err)
			a.post(func() {
				a.toast = "Restore failed: " + err.Error()
				a.changed()
			})
			return
		}
		a.post(a.changed)
	})
}

// deleteEntries removes entries and offers Undo.
func (a *app) deleteEntries(ids []int64) {
	if len(ids) == 0 {
		return
	}
	a.async(func() {
		n, undo, err := a.store.Delete(a.ctx, ids...)
		if err != nil {
			slog.Error("delete", "err", err)
			a.post(func() { a.toast = "Delete failed: " + err.Error() })
			return
		}
		a.cleanImageFiles()
		a.post(func() {
			a.selected.Clear()
			a.toast = deletedMessage(int(n))
			if n > 0 && undo == nil {
				a.toast += " (too large to undo)"
			}
			a.toastUndo = undo
			a.changed()
		})
	})
}

func deletedMessage(n int) string {
	if n == 1 {
		return "Entry deleted"
	}
	return fmt.Sprintf("%d entries deleted", n)
}

func (a *app) setPinned(id int64, pinned bool) {
	a.async(func() {
		if err := a.store.SetPinned(a.ctx, id, pinned); err != nil {
			slog.Error("pin", "err", err)
			a.post(func() { a.toast = "Pin failed: " + err.Error() })
			return
		}
		a.post(a.changed)
	})
}

// confirmClear asks, then clears history except pinned entries.
func (a *app) confirmClear(parent *mygo.Window) {
	a.async(func() {
		res, err := a.messageDialog(mygo.MessageOptions{
			Parent: parent, Type: mygo.MessageWarning, Title: "Clear History",
			Message:            "Clear clipboard history?",
			Detail:             "Pinned entries and snippets are kept.",
			Buttons:            []string{"Clear History", "Cancel"},
			DefaultButton:      1,
			CancelButton:       1,
			DestructiveButtons: []int{0},
		})
		if err != nil || res.Button != 0 {
			return
		}
		if _, err := a.store.Clear(a.ctx, true); err != nil {
			slog.Error("clear", "err", err)
			return
		}
		a.cleanImageFiles()
		a.post(a.changed)
	})
}

// clearFromPanel hides the panel, then asks to clear history. The panel
// hides on blur anyway once the dialog takes focus (app.go OnBlur), so the
// dialog is unparented, as from the tray.
func (a *app) clearFromPanel() {
	a.hide()
	a.confirmClear(nil)
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
	a.clearPreview()
	a.requery()
}

func (a *app) copyChosen(plain bool) {
	if a.listStale {
		a.afterList = func() { a.copyChosen(plain) }
		return
	}
	if a.tab == tabSnippets {
		if s, ok := a.chosenSnip(); ok {
			a.copySnippet(s, false)
		}
		return
	}
	ids := a.selectedIDs()
	if len(ids) > 1 && !plain {
		a.copyMerged(ids)
		return
	}
	if e, ok := a.chosenEntry(); ok {
		a.copyEntry(e, plain, false)
	}
}

func (a *app) copyNth(n int) {
	if a.listStale {
		a.afterList = func() { a.copyNth(n) }
		return
	}
	if a.tab == tabSnippets {
		if n < len(a.snippets) {
			a.copySnippet(a.snippets[n], false)
		}
		return
	}
	if n < len(a.items) {
		a.copyEntry(a.items[n], false, false)
	}
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
	a.async(func() {
		err := a.store.DeleteSnippet(a.ctx, s.ID)
		a.post(func() {
			if err != nil {
				slog.Error("delete snippet", "err", err)
				a.toast = "Delete snippet failed: " + err.Error()
				a.changed()
				return
			}
			if key, h, ok := a.snippetHotkey(s.ID); ok {
				a.hotkeys.Unregister(h.acc)
				delete(a.snipKeys, key)
			}
			a.changed()
		})
	})
}

func (a *app) showToast(c *ui.Context) {
	if a.toast == "" {
		return
	}
	msg, undo := a.toast, a.toastUndo
	a.toast, a.toastUndo = "", nil
	if len(undo) > 0 {
		c.AddToast(ui.Toast{ID: msg, Title: msg, Action: "Undo", OnAction: func() { a.undoDelete(undo) }, Timeout: 8 * time.Second})
		return
	}
	c.Toast(msg)
}

// runPending applies what a context menu chose, once the list is built.
func (a *app) runPending() {
	if fn := a.pending; fn != nil {
		a.pending = nil
		fn()
	}
}
