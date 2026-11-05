//go:build windows

package paste

import (
	"fmt"
	"slices"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	procSendInput             = user32.NewProc("SendInput")
	procGetForegroundWindow   = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procIsWindow              = user32.NewProc("IsWindow")
	procGetClassNameW         = user32.NewProc("GetClassNameW")
	procGetWindowThreadProcId = user32.NewProc("GetWindowThreadProcessId")
	procGetAsyncKeyState      = user32.NewProc("GetAsyncKeyState")
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentProcessId   = kernel32.NewProc("GetCurrentProcessId")
)

const (
	inputKeyboard  = 1
	keyEventExtend = 0x1
	keyEventKeyUp  = 0x2
	vkControl      = 0x11
	vkV            = 0x56
	vkLeft         = 0x25
)

type keybdInput struct {
	vk, scan    uint16
	flags, time uint32
	extra       uintptr
}

// input is INPUT: 40 bytes on amd64 and arm64.
type input struct {
	typ uint32
	ki  keybdInput
	_   [8]byte
}

// shellClasses are taskbar windows: foreground there, Ctrl+V goes nowhere useful.
var shellClasses = []string{"Shell_TrayWnd", "Shell_SecondaryTrayWnd", "NotifyIconOverflowWindow"}

// modifierKeys are released before Ctrl+V, in this order. extend marks keys
// that need the extended-key flag.
var modifierKeys = []struct {
	vk     uint16
	extend bool
}{
	{0xA0, false}, // Shift L
	{0xA1, false}, // Shift R
	{0xA2, false}, // Ctrl L
	{0xA3, true},  // Ctrl R
	{0xA4, false}, // Alt L
	{0xA5, true},  // Alt R
	{0x5B, true},  // Win L
	{0x5C, true},  // Win R
}

// Available reports whether pasting can work here.
func Available(string) (bool, string) { return true, "" }

// Remember returns the foreground window as the paste target. It is empty when
// the foreground is this process or a taskbar window.
func (System) Remember() Target {
	h, _, _ := procGetForegroundWindow.Call()
	if !validTarget(h) {
		return Target{}
	}
	return Target{Window: h}
}

// validTarget reports whether h is a window Paste can refocus and type into.
func validTarget(h uintptr) bool {
	if h == 0 {
		return false
	}
	if ok, _, _ := procIsWindow.Call(h); ok == 0 {
		return false
	}
	var pid uint32
	procGetWindowThreadProcId.Call(h, uintptr(unsafe.Pointer(&pid)))
	if self, _, _ := procGetCurrentProcessId.Call(); pid == uint32(self) {
		return false
	}
	var buf [256]uint16
	n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return !slices.Contains(shellClasses, syscall.UTF16ToString(buf[:n]))
}

// keyDown reports whether the virtual key vk is currently down.
func keyDown(vk int32) bool {
	state, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return state&0x8000 != 0
}

func (System) Trusted(bool) bool { return true }

func key(vk uint16, flags uint32) input {
	return input{typ: inputKeyboard, ki: keybdInput{vk: vk, flags: flags}}
}

// releaseHeld returns a key-up for each modifier that held reports as down, so
// a key the user still holds from the panel hotkey doesn't turn Ctrl+V into another chord.
func releaseHeld(held func(vk int32) bool) []input {
	var out []input
	for _, m := range modifierKeys {
		if !held(int32(m.vk)) {
			continue
		}
		flags := uint32(keyEventKeyUp)
		if m.extend {
			flags |= keyEventExtend
		}
		out = append(out, key(m.vk, flags))
	}
	return out
}

// Paste refocuses t.Window, then sends Ctrl+V and Left left times. It sends
// nothing and returns ErrNoTarget when t has no usable window.
func (System) Paste(t Target, left int) error {
	if t.Window == 0 {
		return ErrNoTarget
	}
	time.Sleep(settle)
	if !validTarget(t.Window) {
		return ErrNoTarget
	}
	procSetForegroundWindow.Call(t.Window)
	time.Sleep(50 * time.Millisecond)
	in := append(releaseHeld(keyDown), key(vkControl, 0), key(vkV, 0), key(vkV, keyEventKeyUp), key(vkControl, keyEventKeyUp))
	for range left {
		in = append(in, key(vkLeft, keyEventExtend), key(vkLeft, keyEventExtend|keyEventKeyUp))
	}
	n, _, _ := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(input{}))
	if int(n) != len(in) {
		return fmt.Errorf("SendInput sent %d of %d events", n, len(in))
	}
	return nil
}
