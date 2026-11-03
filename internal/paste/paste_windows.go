//go:build windows

package paste

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procSendInput           = user32.NewProc("SendInput")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
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

var remembered uintptr

// Available reports whether pasting can work here.
func Available(string) (bool, string) { return true, "" }

// Remember notes the window that has focus, to return to it before pasting.
func (System) Remember() {
	remembered, _, _ = procGetForegroundWindow.Call()
}

func (System) Trusted(bool) bool { return true }

func key(vk uint16, flags uint32) input {
	return input{typ: inputKeyboard, ki: keybdInput{vk: vk, flags: flags}}
}

// Paste sends Ctrl+V, then Left left times.
func (System) Paste(left int) error {
	time.Sleep(settle)
	if remembered != 0 {
		procSetForegroundWindow.Call(remembered)
		time.Sleep(50 * time.Millisecond)
	}
	in := []input{key(vkControl, 0), key(vkV, 0), key(vkV, keyEventKeyUp), key(vkControl, keyEventKeyUp)}
	for range left {
		in = append(in, key(vkLeft, keyEventExtend), key(vkLeft, keyEventExtend|keyEventKeyUp))
	}
	n, _, _ := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(input{}))
	if int(n) != len(in) {
		return fmt.Errorf("SendInput sent %d of %d events", n, len(in))
	}
	return nil
}
