package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/egoist/mygo"

	"okibaclip/internal/fsutil"
	"okibaclip/internal/history"
)

// exportTo streams the whole history and snippets to path as JSON.
func (a *app) exportTo(ctx context.Context, path string) error {
	return fsutil.WriteFileAtomicFunc(path, func(w io.Writer) error { return a.store.WriteExport(ctx, w) })
}

// importFrom merges an export file into the store and applies retention. It
// prunes even when the import fails, since earlier batches stay committed.
func (a *app) importFrom(ctx context.Context, path string, l history.Limits) (entries, snippets, dupes int, pruned int64, err error) {
	fh, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer fh.Close()
	entries, snippets, dupes, err = a.store.Import(ctx, fh)
	pruned, perr := a.store.Prune(ctx, l, time.Now())
	if err == nil {
		err = perr
	}
	return
}

// postSettings runs fn on the settings window's thread; with no window open
// there is nothing to update, so fn is dropped.
func (a *app) postSettings(fn func()) {
	if a.settingsWin != nil {
		a.settingsWin.Update(fn)
	}
}

func (a *app) exportData() {
	parent := a.settingsWin
	a.async(func() {
		path, err := a.saveDialog(mygo.SaveDialogOptions{
			Parent:      parent,
			Title:       "Export clipboard history",
			DefaultPath: history.ExportFormat + "-" + time.Now().Format("2006-01-02") + ".json",
			Filters:     []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil {
			slog.Error("export dialog", "err", err)
			a.postSettings(func() { a.settingsUI.toast = "Export failed: " + err.Error() })
			return
		}
		if path == "" {
			return
		}
		msg := "Exported to " + path
		if err := a.exportTo(a.ctx, path); err != nil {
			slog.Error("export", "err", err)
			msg = "Export failed: " + err.Error()
		}
		a.postSettings(func() { a.settingsUI.toast = msg })
	})
}

func (a *app) importData() {
	// Main-thread state is read here: the closure runs off the main thread.
	parent := a.settingsWin
	limits := a.cfg.Limits()
	a.async(func() {
		paths, err := a.openDialog(mygo.OpenDialogOptions{
			Parent:  parent,
			Title:   "Import clipboard history",
			Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil {
			slog.Error("import dialog", "err", err)
			a.postSettings(func() { a.settingsUI.toast = "Import failed: " + err.Error() })
			return
		}
		if len(paths) == 0 {
			return
		}
		e, s, d, pruned, err := a.importFrom(a.ctx, paths[0], limits)
		if pruned > 0 {
			a.cleanImageFiles()
		}
		if err != nil {
			mygo.Dialog.Error(appName, "Import failed: "+err.Error()+"\n\nEntries and snippets read before the error were kept.")
			a.post(func() {
				a.changed()
				a.registerSnippetShortcuts()
			})
			return
		}
		msg := fmt.Sprintf("Imported %d entries and %d snippets (%d duplicates merged)", e, s, d)
		if pruned > 0 {
			msg += fmt.Sprintf("; %d entries removed by history limits", pruned)
		}
		a.postSettings(func() { a.settingsUI.toast = msg })
		a.post(func() {
			a.changed()
			a.registerSnippetShortcuts()
		})
	})
}
