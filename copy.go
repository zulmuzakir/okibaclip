package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo/transfer"

	"okibaclip/internal/history"
	"okibaclip/internal/transform"
)

// textData offers text, and HTML when given.
func textData(text, html string) transfer.Data {
	reps := []transfer.Representation{transfer.Bytes(transfer.Text, []byte(text))}
	if html != "" {
		reps = append(reps, transfer.Bytes(transfer.HTML, []byte(html)))
	}
	return transfer.New(transfer.NewItem(reps...))
}

func imageData(png []byte) transfer.Data {
	return transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, png)))
}

// existingPaths keeps the paths that still exist.
func existingPaths(paths []string) (found []string, missing int) {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		} else {
			missing++
		}
	}
	return
}

// copyResult is what a copy produced, for the main thread to show.
type copyResult struct {
	ok    bool   // the panel may hide
	toast string // message to show, if any
	err   error
}

// writeEntry writes e (a full row) to the clipboard.
func (a *app) writeEntry(e history.Entry, plain bool) copyResult {
	switch e.Kind {
	case history.KindImage:
		if plain {
			return copyResult{toast: "Images have no plain text"}
		}
		if err := a.writeClipboard(imageData(e.Image)); err != nil {
			return copyResult{err: err}
		}
	case history.KindFiles:
		paths := strings.Split(e.Text, "\n")
		if plain {
			a.suppressText(e.Text)
			if err := a.writeClipboard(textData(e.Text, "")); err != nil {
				return copyResult{err: err}
			}
			break
		}
		found, missing := existingPaths(paths)
		if len(found) == 0 {
			return copyResult{toast: "Files no longer exist"}
		}
		d, err := transfer.FileData(found...)
		if err != nil {
			return copyResult{err: err}
		}
		if err := a.writeClipboard(d); err != nil {
			return copyResult{err: err}
		}
		if missing > 0 {
			return copyResult{ok: true, toast: fmt.Sprintf("%d missing files skipped", missing)}
		}
	default:
		html := e.HTML
		if plain {
			html = ""
		}
		if err := a.writeClipboard(textData(e.Text, html)); err != nil {
			return copyResult{err: err}
		}
	}
	return copyResult{ok: true}
}

func (a *app) suppressText(text string) {
	if a.mon != nil {
		a.mon.Suppress(history.Capture{Kind: history.TextKind(text), Text: text}.Hash())
	}
}

// finishCopy runs on the main thread after a write.
func (a *app) finishCopy(r copyResult) {
	if r.err != nil {
		slog.Error("copy", "err", r.err)
		a.toast = "Copy failed: " + r.err.Error()
		return
	}
	if r.toast != "" {
		a.toast = r.toast
	}
	if r.ok {
		a.hint = "Copied — press " + pasteKey() + " to paste"
		a.hide()
	}
}

// copyEntry copies a history entry (a list row, fetched in full first).
func (a *app) copyEntry(e history.Entry, plain bool) {
	moveToTop := a.cfg.MoveToTopOnCopy // read here: the closure runs off the main thread
	a.async(func() {
		full, err := a.store.Get(a.ctx, e.ID)
		if err != nil {
			a.post(func() { a.finishCopy(copyResult{err: err}) })
			return
		}
		if !moveToTop && a.mon != nil && !(plain && full.Kind == history.KindFiles) {
			a.mon.Suppress(full.Hash)
		}
		r := a.writeEntry(full, plain)
		if r.ok && moveToTop {
			if err := a.store.Touch(a.ctx, e.ID, time.Now()); err != nil {
				slog.Error("touch", "err", err)
			}
		}
		a.post(func() {
			a.finishCopy(r)
			if r.ok {
				a.changed()
			}
		})
	})
}

// copyMerged copies the selected entries, in list order, as one text, or as
// files when all are files.
func (a *app) copyMerged(ids []int64) {
	a.async(func() {
		var texts, paths []string
		allFiles, skippedImages := true, false
		for _, id := range ids {
			e, err := a.store.Get(a.ctx, id)
			if err != nil {
				continue
			}
			switch e.Kind {
			case history.KindImage:
				skippedImages = true
			case history.KindFiles:
				texts = append(texts, e.Text)
				paths = append(paths, strings.Split(e.Text, "\n")...)
			default:
				allFiles = false
				texts = append(texts, e.Text)
			}
		}
		if len(texts) == 0 {
			a.post(func() { a.finishCopy(copyResult{toast: "Images skipped"}) })
			return
		}
		var r copyResult
		if allFiles {
			found, missing := existingPaths(paths)
			if len(found) == 0 {
				a.post(func() { a.finishCopy(copyResult{toast: "Files no longer exist"}) })
				return
			}
			d, err := transfer.FileData(found...)
			if err == nil {
				err = a.writeClipboard(d)
			}
			r = copyResult{ok: err == nil, err: err}
			if missing > 0 {
				r.toast = fmt.Sprintf("%d missing files skipped", missing)
			}
		} else {
			text := strings.Join(texts, "\n")
			a.suppressText(text)
			err := a.writeClipboard(textData(text, ""))
			r = copyResult{ok: err == nil, err: err}
		}
		if skippedImages && r.toast == "" {
			r.toast = "Images skipped"
		}
		a.post(func() { a.finishCopy(r) })
	})
}

// copyTransformed writes t applied to the entry's text, kept out of history.
func (a *app) copyTransformed(e history.Entry, t transform.Transform) {
	a.async(func() {
		full, err := a.store.Get(a.ctx, e.ID)
		if err != nil {
			a.post(func() { a.finishCopy(copyResult{err: err}) })
			return
		}
		out, err := t.Fn(full.Text)
		if err != nil {
			a.post(func() { a.toast = fmt.Sprintf("%s: %v", t.Name, err) })
			return
		}
		a.suppressText(out)
		err = a.writeClipboard(textData(out, ""))
		a.post(func() { a.finishCopy(copyResult{ok: err == nil, err: err}) })
	})
}

func (a *app) copySnippet(s history.Snippet) {
	a.suppressText(s.Body)
	err := a.writeClipboard(textData(s.Body, ""))
	a.finishCopy(copyResult{ok: err == nil, err: err})
}

func pasteKey() string {
	if isDarwin {
		return "⌘V"
	}
	return "Ctrl+V"
}
