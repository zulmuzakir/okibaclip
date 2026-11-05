package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
)

const (
	// appName is shown in menus, dialogs, the tray and login items.
	appName = "Okiba Clip"
	// dataDirName names the user-data folder, in plain ASCII like the
	// build's slug.
	dataDirName = "okibaclip"
)

func main() {
	mygo.App.SetName(appName)
	if !mygo.App.RequestSingleInstanceLock() {
		return // the running instance gets our arguments (OnSecondInstance)
	}
	if base, err := dataBase(); err == nil {
		mygo.App.SetPath(mygo.PathUserData, filepath.Join(base, dataDirName))
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
	case wayland && display && os.Getenv("GDK_BACKEND") == "" && os.Getenv("OKIBACLIP_NATIVE_WAYLAND") == "":
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
