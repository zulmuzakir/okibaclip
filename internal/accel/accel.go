// Package accel normalizes keyboard shortcut strings so that equivalent
// spellings compare equal ("shift+ctrl+k" and "Control+Shift+K").
package accel

import (
	"errors"
	"runtime"
	"strings"
)

// Key returns the canonical comparison form of acc: modifiers in the order
// Super, Ctrl, Alt, Shift, then the key. Single-character keys are upper-cased.
// The empty string returns "".
func Key(acc string) string {
	if strings.TrimSpace(acc) == "" {
		return ""
	}
	mods, key := parse(acc)
	var parts []string
	for i, names := range [4]string{"Super", "Ctrl", "Alt", "Shift"} {
		if mods[i] {
			parts = append(parts, names)
		}
	}
	if key != "" {
		if len([]rune(key)) == 1 {
			key = strings.ToUpper(key)
		}
		parts = append(parts, key)
	}
	return strings.Join(parts, "+")
}

const (
	superMod = iota
	ctrlMod
	altMod
	shiftMod
)

// parse splits acc into its modifiers (Super, Ctrl, Alt, Shift) and the
// lower-cased key, "" when there is none.
func parse(acc string) (mods [4]bool, key string) {
	for _, tok := range strings.Split(acc, "+") {
		t := strings.ToLower(strings.TrimSpace(tok))
		switch t {
		case "":
		case "command", "cmd", "super", "meta", "win":
			mods[superMod] = true
		case "control", "ctrl":
			mods[ctrlMod] = true
		case "alt", "option", "altgr":
			mods[altMod] = true
		case "shift":
			mods[shiftMod] = true
		case "cmdorctrl", "commandorcontrol":
			if runtime.GOOS == "darwin" {
				mods[superMod] = true
			} else {
				mods[ctrlMod] = true
			}
		default:
			key = t
		}
	}
	return mods, key
}

var (
	ErrNoKey      = errors.New("Shortcut needs a key")
	ErrNoModifier = errors.New("Shortcut needs Ctrl, Alt, Cmd or Super")
	ErrReserved   = errors.New("That shortcut is reserved by the system")
)

// reserved holds canonical Key forms of system and editing shortcuts.
var reserved = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`Ctrl+A Ctrl+C Ctrl+V Ctrl+X Ctrl+Y Ctrl+Z Ctrl+Shift+Z Ctrl+Q Ctrl+W
Super+A Super+C Super+V Super+X Super+Y Super+Z Super+Shift+Z Super+Q Super+W
Super+tab Super+Shift+tab Super+space Alt+tab Alt+Shift+tab Alt+f4 Ctrl+Alt+delete`) {
		reserved[k] = true
	}
}

// Valid reports whether acc can be a global shortcut: a key plus at least
// one of Super, Ctrl or Alt (Shift alone is not enough), and not a
// system or editing shortcut.
func Valid(acc string) error {
	mods, key := parse(acc)
	switch {
	case key == "":
		return ErrNoKey
	case !mods[superMod] && !mods[ctrlMod] && !mods[altMod]:
		return ErrNoModifier
	case reserved[Key(acc)]:
		return ErrReserved
	}
	return nil
}
