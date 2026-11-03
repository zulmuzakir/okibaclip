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

// textHash is the history hash of text as the monitor would capture it.
func textHash(text string) string {
	return history.Capture{Kind: history.TextKind(text), Text: text}.Hash()
}

func filesHash(paths []string) string {
	return history.Capture{Kind: history.KindFiles, Text: strings.Join(paths, "\n")}.Hash()
}

// write suppresses hash (if non-empty) before writing d, so the next poll
// can't record the app's own write; a failed write lifts the suppression.
func (a *app) write(hash string, d transfer.Data) error {
	if hash != "" && a.mon != nil {
		a.mon.Suppress(hash)
	}
	err := a.writeClipboard(d)
	if err != nil && hash != "" && a.mon != nil {
		a.mon.Unsuppress(hash)
	}
	return err
}

// writeEntry writes e (a full row) to the clipboard. With suppress, the
// monitor drops the resulting capture; plain file-path text is always dropped.
func (a *app) writeEntry(e history.Entry, plain, suppress bool) copyResult {
	h := func(s string) string {
		if suppress {
			return s
		}
		return ""
	}
	switch e.Kind {
	case history.KindImage:
		if plain {
			return copyResult{toast: "Images have no plain text"}
		}
		if err := a.write(h(e.Hash), imageData(e.Image)); err != nil {
			return copyResult{err: err}
		}
	case history.KindFiles:
		paths := strings.Split(e.Text, "\n")
		if plain {
			if err := a.write(textHash(e.Text), textData(e.Text, "")); err != nil {
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
		if err := a.write(h(filesHash(found)), d); err != nil {
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
		if err := a.write(h(e.Hash), textData(e.Text, html)); err != nil {
			return copyResult{err: err}
		}
	}
	return copyResult{ok: true}
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
		r := a.writeEntry(full, plain, !moveToTop)
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
			toast := "Entries no longer exist"
			if skippedImages {
				toast = "Images skipped"
			}
			a.post(func() { a.finishCopy(copyResult{toast: toast}) })
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
				err = a.write(filesHash(found), d)
			}
			r = copyResult{ok: err == nil, err: err}
			if missing > 0 {
				r.toast = fmt.Sprintf("%d missing files skipped", missing)
			}
		} else {
			text := strings.Join(texts, "\n")
			err := a.write(textHash(text), textData(text, ""))
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
		err = a.write(textHash(out), textData(out, ""))
		a.post(func() { a.finishCopy(copyResult{ok: err == nil, err: err}) })
	})
}

func (a *app) copySnippet(s history.Snippet) {
	err := a.write(textHash(s.Body), textData(s.Body, ""))
	a.finishCopy(copyResult{ok: err == nil, err: err})
}

func pasteKey() string {
	if isDarwin {
		return "⌘V"
	}
	return "Ctrl+V"
}
