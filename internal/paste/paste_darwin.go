//go:build darwin

package paste

import (
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	keyV          = 9    // kVK_ANSI_V
	keyLeft       = 0x7B // kVK_LeftArrow
	flagCommand   = 0x100000
	tapHIDSession = 0
)

var (
	once                  sync.Once
	loadErr               error
	cgEventCreateKeyboard func(src uintptr, key uint16, down bool) uintptr
	cgEventSetFlags       func(ev uintptr, flags uint64)
	cgEventPost           func(tap uint32, ev uintptr)
	cfRelease             func(uintptr)
	axIsProcessTrusted    func(dict uintptr) bool
)

func load() error {
	once.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		app, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		purego.RegisterLibFunc(&cgEventCreateKeyboard, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cfRelease, cg, "CFRelease")
		purego.RegisterLibFunc(&axIsProcessTrusted, app, "AXIsProcessTrustedWithOptions")
	})
	return loadErr
}

// Available reports whether pasting can work here. The panel is a
// non-activating window, so the app the user was in still has the keyboard
// after it hides.
func Available(string) (bool, string) { return true, "" }

func (System) Remember() {}

// Trusted reports whether the app may post key events (Accessibility). With
// prompt, macOS asks the user to allow it.
func (System) Trusted(prompt bool) bool {
	if load() != nil {
		return false
	}
	if !prompt {
		return axIsProcessTrusted(0)
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	defer pool.Send(objc.RegisterName("drain"))
	key := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "AXTrustedCheckOptionPrompt")
	yes := objc.ID(objc.GetClass("NSNumber")).Send(objc.RegisterName("numberWithBool:"), true)
	dict := objc.ID(objc.GetClass("NSDictionary")).Send(objc.RegisterName("dictionaryWithObject:forKey:"), yes, key)
	return axIsProcessTrusted(uintptr(dict))
}

func tap(code uint16, flags uint64) {
	for _, down := range []bool{true, false} {
		ev := cgEventCreateKeyboard(0, code, down)
		if ev == 0 {
			continue
		}
		cgEventSetFlags(ev, flags)
		cgEventPost(tapHIDSession, ev)
		cfRelease(ev)
	}
}

// Paste sends Command+V, then Left left times.
func (System) Paste(left int) error {
	if err := load(); err != nil {
		return err
	}
	time.Sleep(settle)
	tap(keyV, flagCommand)
	for range left {
		tap(keyLeft, 0)
	}
	return nil
}
