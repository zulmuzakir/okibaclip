package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/egoist/mygo"

	"okibaclip/internal/history"
)

func (a *app) setupTray() {
	t, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "main",
		Icon:           trayIcon(false),
		IconIsTemplate: isDarwin,
		ToolTip:        appName,
	})
	if err != nil {
		a.trayErr = err.Error()
		slog.Error("tray", "err", err)
		return
	}
	a.tray = t
	if isLinux {
		// AppIndicator is menu-only; the menu is installed for good.
		t.SetMenu(a.buildTrayMenu())
		return
	}
	t.OnClick(func() { a.toggle(fromTray) })
	t.OnRightClick(func() {
		a.hide()
		t.SetMenu(a.buildTrayMenu())
		defer t.SetMenu(nil)
		t.PopUpMenu()
	})
}

func trayLabel(e history.Entry) string {
	if e.Kind == history.KindImage {
		return fmt.Sprintf("Image %d×%d", e.Width, e.Height)
	}
	s := preview(e)
	if s == "" {
		s = "(blank)"
	}
	return truncRunes(s, 40)
}

func (a *app) buildTrayMenu() *mygo.Menu {
	item := func(e history.Entry) *mygo.MenuItem {
		return &mygo.MenuItem{Label: trayLabel(e), Click: func(*mygo.MenuItem, *mygo.Window) { a.copyEntry(e, false) }}
	}
	var items []*mygo.MenuItem
	items = append(items,
		&mygo.MenuItem{Label: "Open Clipboard History", Accelerator: a.shortcut, Click: func(*mygo.MenuItem, *mygo.Window) { a.show(fromHotkey) }},
		mygo.Separator())
	if len(a.trayRecent) == 0 {
		items = append(items, &mygo.MenuItem{Label: "No history yet", Disabled: true})
	}
	for _, e := range a.trayRecent {
		items = append(items, item(e))
	}
	var pinned []*mygo.MenuItem
	for _, e := range a.trayPinned {
		pinned = append(pinned, item(e))
	}
	if len(pinned) == 0 {
		pinned = []*mygo.MenuItem{{Label: "None", Disabled: true}}
	}
	var snips []*mygo.MenuItem
	for _, s := range a.traySnippets {
		snips = append(snips, &mygo.MenuItem{Label: truncRunes(s.Name, 40), Click: func(*mygo.MenuItem, *mygo.Window) { a.copySnippet(s) }})
	}
	if len(snips) == 0 {
		snips = []*mygo.MenuItem{{Label: "None", Disabled: true}}
	}
	items = append(items,
		&mygo.MenuItem{Label: "Pinned", Submenu: pinned},
		&mygo.MenuItem{Label: "Snippets", Submenu: snips},
		mygo.Separator())
	if paused, _ := a.paused(); paused {
		items = append(items, &mygo.MenuItem{Label: "Resume Capture", Click: func(*mygo.MenuItem, *mygo.Window) { a.mon.Resume() }})
	} else {
		items = append(items, &mygo.MenuItem{Label: "Pause Capture", Submenu: []*mygo.MenuItem{
			{Label: "For 15 Minutes", Click: func(*mygo.MenuItem, *mygo.Window) { a.mon.Pause(15 * time.Minute) }},
			{Label: "For 1 Hour", Click: func(*mygo.MenuItem, *mygo.Window) { a.mon.Pause(time.Hour) }},
			{Label: "Until Resumed", Click: func(*mygo.MenuItem, *mygo.Window) { a.mon.Pause(-1) }},
		}})
	}
	items = append(items,
		&mygo.MenuItem{Label: "Ignore Next Copy", Click: func(*mygo.MenuItem, *mygo.Window) { a.mon.IgnoreNext() }},
		&mygo.MenuItem{Label: "Clear History…", Click: func(*mygo.MenuItem, *mygo.Window) { a.confirmClear(nil) }},
		mygo.Separator(),
		&mygo.MenuItem{Label: "Settings…", Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }},
		&mygo.MenuItem{Label: "Open at Login", Type: mygo.MenuItemCheckbox, ID: "login", Checked: mygo.App.OpenAtLogin(),
			Click: func(it *mygo.MenuItem, _ *mygo.Window) {
				if err := mygo.App.SetOpenAtLogin(it.Checked); err != nil {
					slog.Error("open at login", "err", err)
				}
			}},
		mygo.Separator(),
		&mygo.MenuItem{Role: mygo.RoleQuit})
	return mygo.NewMenu(items)
}

// refreshTray updates the tooltip and icon at once and, on Linux, the
// permanent menu after a 500 ms debounce.
func (a *app) refreshTray() {
	if a.tray == nil {
		return
	}
	paused, _ := a.paused()
	tip := fmt.Sprintf("%s — %d items", appName, a.counts.entries)
	if paused {
		tip = appName + " — paused"
	}
	a.tray.SetToolTip(tip)
	if paused != a.trayPaused {
		a.trayPaused = paused
		if err := a.tray.SetIcon(trayIcon(paused), isDarwin); err != nil {
			slog.Error("tray icon", "err", err)
		}
	}
	if !isLinux {
		return
	}
	if a.trayTimer != nil {
		a.trayTimer.Stop()
	}
	a.trayTimer = time.AfterFunc(500*time.Millisecond, func() {
		a.post(func() {
			if a.tray != nil && !a.quitting {
				a.tray.SetMenu(a.buildTrayMenu())
			}
		})
	})
}
