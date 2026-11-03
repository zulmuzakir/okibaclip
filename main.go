package main

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
)

// formerName is the app's name before it became pastello; its data folder
// is moved over on the first launch after the rename.
const formerName = "my-clipboard"

func main() {
	mygo.App.SetName("pastello")
	if !mygo.App.RequestSingleInstanceLock() {
		return // the running instance gets our arguments (OnSecondInstance)
	}
	if base, err := mygo.App.Path(mygo.PathAppData); err == nil {
		if err := migrateDataDir(base, formerName, mygo.App.Name()); err != nil {
			slog.Warn("migrate data folder", "err", err)
		}
	}
	a := newApp()
	a.waylandMode = configureLinuxBackend()
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnSecondInstance(func(args []string, _ string) { a.command(args, true) })
	mygo.App.WhenReady(a.ready)
	if err := mygo.App.Run(); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
}

// migrateDataDir renames base/from to base/to when only the former exists,
// so history and settings survive renaming the app.
func migrateDataDir(base, from, to string) error {
	newDir := filepath.Join(base, to)
	if _, err := os.Lstat(newDir); !errors.Is(err, fs.ErrNotExist) {
		return err // already migrated or used, or unreadable
	}
	oldDir := filepath.Join(base, from)
	if _, err := os.Lstat(oldDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // nothing to migrate
		}
		return err
	}
	return os.Rename(oldDir, newDir)
}

// configureLinuxBackend forces XWayland where it exists, so the clipboard
// and global shortcuts work (GTK3 under native Wayland sees clipboard
// changes only while focused). It returns "x11", "xwayland" or "wayland",
// and "" off Linux.
func configureLinuxBackend() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	wayland := os.Getenv("WAYLAND_DISPLAY") != ""
	display := os.Getenv("DISPLAY") != ""
	switch {
	case wayland && display && os.Getenv("GDK_BACKEND") == "" && os.Getenv("PASTELLO_NATIVE_WAYLAND") == "":
		os.Setenv("GDK_BACKEND", "x11")
		return "xwayland"
	case wayland && !display:
		return "wayland"
	case wayland && os.Getenv("GDK_BACKEND") == "x11":
		return "xwayland"
	case wayland:
		return "wayland"
	}
	return "x11"
}
