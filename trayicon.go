package main

import "embed"

// brand holds the tray icons and the About icon, rendered from the logo's
// small-size cut (a clipboard with a plum clip and two rows).
//
//go:embed brand/*.png
var brand embed.FS

// trayIcon is the tray's PNG. macOS gets black on transparent, which it
// tints to the menu bar. Windows gets ink, or cream when dark reports a dark
// taskbar. Linux gets ink with opaque cream rows, which reads on any panel,
// since the app can't tell the panel's color there. Paused, pause bars
// replace the rows.
func trayIcon(paused, dark bool) []byte {
	name := "light"
	switch {
	case isDarwin:
		name = "template"
	case isLinux:
		name = "neutral"
	case dark:
		name = "dark"
	}
	if paused {
		name += "-paused"
	}
	return brandPNG(name)
}

// brandPNG returns the embedded brand/<name>.png.
func brandPNG(name string) []byte {
	b, err := brand.ReadFile("brand/" + name + ".png")
	if err != nil {
		panic(err) // embedded at build time: a missing name is a programming error
	}
	return b
}
