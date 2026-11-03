package main

import (
	"log/slog"
	"os"
	"runtime"

	"github.com/egoist/mygo"
)

func main() {
	mygo.App.SetName("my-clipboard")
	if !mygo.App.RequestSingleInstanceLock() {
		return // the running instance gets our arguments (OnSecondInstance)
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
	case wayland && display && os.Getenv("GDK_BACKEND") == "" && os.Getenv("MY_CLIPBOARD_NATIVE_WAYLAND") == "":
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
