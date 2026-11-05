// Package accel normalizes keyboard shortcut strings so that equivalent
// spellings compare equal ("shift+ctrl+k" and "Control+Shift+K").
package accel

import (
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
	var mods [4]bool // Super, Ctrl, Alt, Shift
	const (
		superMod = iota
		ctrlMod
		altMod
		shiftMod
	)
	key := ""
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
