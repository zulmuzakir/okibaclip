package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/egoist/mygo"

	"pastello/internal/history"
)

// exportTo writes the whole history and snippets to path as JSON.
func (a *app) exportTo(ctx context.Context, path string) error {
	f, err := a.store.Export(ctx)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// importFrom merges an export file into the store and applies retention.
func (a *app) importFrom(ctx context.Context, path string, maxEntries int, retention time.Duration) (entries, snippets, dupes int, err error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0, 0, err
	}
	if st.Size() > history.MaxImportSize {
		return 0, 0, 0, fmt.Errorf("file is larger than %d MiB", history.MaxImportSize>>20)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, err
	}
	f, err := history.DecodeExport(b)
	if err != nil {
		return 0, 0, 0, err
	}
	entries, snippets, dupes, err = a.store.Import(ctx, f)
	if err != nil {
		return
	}
	_, err = a.store.Prune(ctx, maxEntries, retention, time.Now())
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
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      parent,
			Title:       "Export clipboard history",
			DefaultPath: "pastello-export-" + time.Now().Format("2006-01-02") + ".json",
			Filters:     []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil || path == "" {
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
	maxEntries, retention := a.cfg.MaxEntries, time.Duration(a.cfg.RetentionDays)*24*time.Hour
	a.async(func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  parent,
			Title:   "Import clipboard history",
			Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		e, s, d, err := a.importFrom(a.ctx, paths[0], maxEntries, retention)
		if err != nil {
			mygo.Dialog.Error("Import failed", err.Error())
			return
		}
		a.postSettings(func() {
			a.settingsUI.toast = fmt.Sprintf("Imported %d entries and %d snippets (%d duplicates merged)", e, s, d)
			a.changed()
		})
	})
}
