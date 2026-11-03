package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/egoist/mygo"

	"okibaclip/internal/fsutil"
	"okibaclip/internal/history"
)

// exportTo writes the whole history and snippets to path as JSON.
func (a *app) exportTo(ctx context.Context, path string) (size int, err error) {
	f, err := a.store.Export(ctx)
	if err != nil {
		return 0, err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := fsutil.WriteFileAtomic(path, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// importFrom merges an export file into the store and applies retention.
func (a *app) importFrom(ctx context.Context, path string, l history.Limits) (entries, snippets, dupes int, pruned int64, err error) {
	fh, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, history.MaxImportSize+1))
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if len(b) > history.MaxImportSize {
		return 0, 0, 0, 0, fmt.Errorf("file is larger than %d MiB", history.MaxImportSize>>20)
	}
	f, err := history.DecodeExport(b)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	entries, snippets, dupes, err = a.store.Import(ctx, f)
	if err != nil {
		return
	}
	pruned, err = a.store.Prune(ctx, l, time.Now())
	if err == nil && pruned > 0 {
		if cerr := a.store.Compact(ctx); cerr != nil {
			slog.Error("compact", "err", cerr)
		}
	}
	return
}

func (a *app) postSettings(fn func()) {
	if a.settingsWin != nil {
		a.settingsWin.Update(fn)
		return
	}
	fn()
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
		if err != nil || path == "" {
			return
		}
		msg := "Exported to " + path
		if size, err := a.exportTo(a.ctx, path); err != nil {
			slog.Error("export", "err", err)
			msg = "Export failed: " + err.Error()
		} else if size > history.MaxImportSize {
			msg += fmt.Sprintf(" (warning: %d MiB is over the %d MiB import limit)", size>>20, history.MaxImportSize>>20)
		}
		a.postSettings(func() { a.settingsUI.toast = msg })
	})
}

func (a *app) importData() {
	// Main-thread state is read here: the closure runs off the main thread.
	parent := a.settingsWin
	limits := a.cfg.Limits()
	a.async(func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  parent,
			Title:   "Import clipboard history",
			Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		e, s, d, pruned, err := a.importFrom(a.ctx, paths[0], limits)
		if err != nil {
			mygo.Dialog.Error("Import failed", err.Error())
			return
		}
		a.postSettings(func() {
			msg := fmt.Sprintf("Imported %d entries and %d snippets (%d duplicates merged)", e, s, d)
			if pruned > 0 {
				msg += fmt.Sprintf("; %d entries removed by history limits", pruned)
			}
			a.settingsUI.toast = msg
			a.changed()
			a.registerSnippetShortcuts()
		})
	})
}
