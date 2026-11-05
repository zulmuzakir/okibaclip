package main

import (
	"errors"
	"log/slog"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"

	"okibaclip/internal/accel"
)

var defaultShortcuts = []string{"Alt+Shift+V", "CmdOrCtrl+Alt+Shift+V"}

func (a *app) onHotkey() { a.toggle(fromHotkey) }

func (a *app) registerShortcut() {
	cands := defaultShortcuts
	if a.cfg.Shortcut != "" {
		cands = []string{a.cfg.Shortcut}
	}
	a.shortcut, a.shortcutErr = "", ""
	for _, s := range cands {
		if err := a.hotkeys.Register(s, a.onHotkey); err != nil {
			slog.Warn("shortcut unavailable", "accelerator", s, "err", err)
			a.shortcutErr = err.Error()
			continue
		}
		a.shortcut, a.shortcutErr = s, ""
		return
	}
}

// applyShortcut replaces the global shortcut; on failure the old one stays.
func (a *app) applyShortcut(acc string) error {
	acc = strings.TrimSpace(acc)
	if acc != "" {
		if err := accel.Valid(acc); err != nil {
			return err
		}
	}
	old := a.shortcut
	if old != "" {
		a.hotkeys.Unregister(old)
	}
	restore := func() {
		if old == "" {
			return
		}
		if err := a.hotkeys.Register(old, a.onHotkey); err != nil {
			slog.Warn("shortcut restore failed", "accelerator", old, "err", err)
			a.shortcut, a.shortcutErr = "", err.Error()
			return
		}
		a.shortcut = old
	}
	if acc == "" {
		prev := a.cfg.Shortcut
		a.cfg.Shortcut = ""
		a.registerShortcut()
		if a.shortcut == "" {
			a.cfg.Shortcut = prev
			failure := errors.New(a.shortcutErr)
			restore()
			return failure
		}
		return nil
	}
	if err := a.hotkeys.Register(acc, a.onHotkey); err != nil {
		restore()
		return err
	}
	a.shortcut, a.shortcutErr = acc, ""
	return nil
}

var digitKeys = []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}

// keys spells a shortcut as the platform does: ⌥⇧V on macOS.
func keys(shortcut string) string {
	if runtime.GOOS != "darwin" {
		return shortcut
	}
	return strings.NewReplacer("CmdOrCtrl+", "⌘", "Cmd+", "⌘", "Ctrl+", "⌃", "Alt+", "⌥", "Shift+", "⇧").Replace(shortcut)
}

func cmdSym() string {
	if isDarwin {
		return "⌘"
	}
	return "Ctrl+"
}

// snipHotkey is a registered snippet shortcut: the accelerator as the user
// typed it (what Register and Unregister take) and the snippet it fires.
type snipHotkey struct {
	acc string
	id  int64
}

// snippetHotkey returns the registration of snippet id, if it has one. key is
// the accel.Key form, which is the snipKeys map key.
func (a *app) snippetHotkey(id int64) (key string, h snipHotkey, ok bool) {
	for k, v := range a.snipKeys {
		if v.id == id {
			return k, v, true
		}
	}
	return "", snipHotkey{}, false
}
