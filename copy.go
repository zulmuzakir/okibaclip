package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"okibaclip/internal/fsutil"
	"okibaclip/internal/history"
	"okibaclip/internal/placeholder"
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
	ok       bool   // the panel may hide
	toast    string // message to show, if any
	err      error
	left     int  // runes to move the caret back after pasting
	fromTray bool // copied from the tray menu: never auto-paste
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

// writeEntry writes e (a full row) to the clipboard. Every write is suppressed
// so the monitor drops its own capture; plain file-path text is never recorded.
func (a *app) writeEntry(e history.Entry, plain bool) copyResult {
	switch e.Kind {
	case history.KindImage:
		if plain {
			return copyResult{toast: "Images have no plain text"}
		}
		if err := a.write(e.Hash, imageData(e.Image)); err != nil {
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
		if err := a.write(filesHash(found), d); err != nil {
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
		if err := a.write(e.Hash, textData(e.Text, html)); err != nil {
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
		if a.cfg.PasteAfterCopy && a.pasteOK && !r.fromTray {
			if a.paster.Trusted(false) {
				a.hint = ""
				p, t, left := a.paster, a.pasteTarget, r.left
				a.async(func() {
					if err := p.Paste(t, left); err != nil {
						slog.Warn("paste", "err", err)
						a.post(func() { a.hint = "Copied — press " + pasteKey() + " to paste" })
					}
				})
			} else {
				a.hint = "Copied — allow " + appName + " in Accessibility settings to paste automatically"
			}
		}
	}
}

// copyEntry copies a history entry (a list row, fetched in full first).
// Tray copies never auto-paste: the user is in another app's menu.
func (a *app) copyEntry(e history.Entry, plain, fromTray bool) {
	moveToTop := a.cfg.MoveToTopOnCopy // read here: the closure runs off the main thread
	a.async(func() {
		full, err := a.store.Get(a.ctx, e.ID)
		if err != nil {
			a.post(func() { a.finishCopy(copyResult{err: err}) })
			return
		}
		r := a.writeEntry(full, plain)
		r.fromTray = fromTray
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

// copySnippet writes a snippet with its placeholders expanded. The clipboard
// write runs off the main thread; the result is finished on the main thread.
func (a *app) copySnippet(s history.Snippet, fromTray bool) {
	a.async(func() {
		clip := func() string {
			b, err := a.src.ReadFormat(transfer.Text)
			if err != nil {
				return ""
			}
			return string(b)
		}
		text, left := placeholder.Expand(s.Body, time.Now(), clip)
		err := a.write(textHash(text), textData(text, ""))
		if err == nil {
			if err := a.store.UseSnippet(a.ctx, s.ID, time.Now()); err != nil {
				slog.Error("use snippet", "err", err)
			}
		}
		a.post(func() {
			a.finishCopy(copyResult{ok: err == nil, err: err, left: left, fromTray: fromTray})
			if err == nil {
				a.changed()
			}
		})
	})
}

// saveImage asks where to save an image entry, then writes its PNG there.
func (a *app) saveImage(e history.Entry) {
	parent := a.panel
	a.async(func() {
		path, err := a.saveDialog(mygo.SaveDialogOptions{
			Parent:      parent,
			Title:       "Save Image",
			DefaultPath: "Image " + e.CreatedAt.Format("2006-01-02 15.04.05") + ".png",
			Filters:     []mygo.FileFilter{{Name: "PNG", Extensions: []string{"png"}}},
		})
		if err != nil || path == "" {
			return
		}
		full, err := a.store.Get(a.ctx, e.ID)
		if err == nil {
			err = fsutil.WriteFileAtomic(path, full.Image)
		}
		msg := "Saved to " + path
		if err != nil {
			slog.Error("save image", "err", err)
			msg = "Save failed: " + err.Error()
		}
		a.post(func() { a.toast = msg })
	})
}

// imageFileDir holds the PNG files that Copy as File puts on the clipboard.
func (a *app) imageFileDir() string { return filepath.Join(a.dataDir, "files") }

// copyImageFile copies an image entry as a PNG file, for apps that take
// files and not images.
func (a *app) copyImageFile(e history.Entry) {
	a.async(func() {
		full, err := a.store.Get(a.ctx, e.ID)
		var p string
		if err == nil {
			dir := a.imageFileDir()
			p = filepath.Join(dir, "Image-"+full.Hash[:12]+".png")
			if err = os.MkdirAll(dir, 0o700); err == nil {
				if _, serr := os.Stat(p); serr != nil {
					err = fsutil.WriteFileAtomic(p, full.Image)
				}
			}
		}
		if err == nil {
			var d transfer.Data
			if d, err = transfer.FileData(p); err == nil {
				err = a.write(filesHash([]string{p}), d)
			}
		}
		a.post(func() { a.finishCopy(copyResult{ok: err == nil, err: err}) })
	})
}

// cleanImageFiles removes copied-as-file images older than a day.
func (a *app) cleanImageFiles() {
	dir := a.imageFileDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range ents {
		if info, err := de.Info(); err == nil && !info.IsDir() && time.Since(info.ModTime()) > 24*time.Hour {
			os.Remove(filepath.Join(dir, de.Name()))
		}
	}
}

func pasteKey() string {
	if isDarwin {
		return "⌘V"
	}
	return "Ctrl+V"
}
