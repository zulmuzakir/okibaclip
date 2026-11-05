//go:build darwin

package paste

import (
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	keyV          = 9    // fallback: kVK_ANSI_V
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
	cfDataGetBytePtr      func(data uintptr) uintptr
	axIsProcessTrusted    func(dict uintptr) bool

	tisCopyCurrentKeyboardLayout func() uintptr
	tisGetInputSourceProperty    func(src, key uintptr) uintptr
	lmGetKbdType                 func() uint8
	ucKeyTranslate               func(layout uintptr, keyCode, keyAction uint16, modState, kbdType, options uint32, deadState unsafe.Pointer, maxLen uint32, actualLen, str unsafe.Pointer) int32
	// kTISPropertyUnicodeKeyLayoutData, the key that holds the layout bytes.
	layoutDataKey uintptr
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
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		carbon, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		keyAddr, err := purego.Dlsym(carbon, "kTISPropertyUnicodeKeyLayoutData")
		if err != nil {
			loadErr = err
			return
		}
		// keyAddr is the address of the CFStringRef global; read the reference.
		layoutDataKey = **(**uintptr)(unsafe.Pointer(&keyAddr))
		purego.RegisterLibFunc(&cgEventCreateKeyboard, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cfRelease, cg, "CFRelease")
		purego.RegisterLibFunc(&cfDataGetBytePtr, cf, "CFDataGetBytePtr")
		purego.RegisterLibFunc(&axIsProcessTrusted, app, "AXIsProcessTrustedWithOptions")
		purego.RegisterLibFunc(&tisCopyCurrentKeyboardLayout, carbon, "TISCopyCurrentKeyboardLayoutInputSource")
		purego.RegisterLibFunc(&tisGetInputSourceProperty, carbon, "TISGetInputSourceProperty")
		purego.RegisterLibFunc(&lmGetKbdType, carbon, "LMGetKbdType")
		purego.RegisterLibFunc(&ucKeyTranslate, carbon, "UCKeyTranslate")
	})
	return loadErr
}

// Available reports whether pasting can work here. The panel is a
// non-activating window, so the app the user was in still has the keyboard
// after it hides.
func Available(string) (bool, string) { return true, "" }

// Remember returns the key that types "v" in the current keyboard layout. Call
// it on the main thread: the Text Input Services API requires it.
func (System) Remember() Target {
	if load() != nil {
		return Target{}
	}
	return Target{KeyV: vKeyCode()}
}

// vKeyCode returns the virtual key code that types "v" in the current layout,
// or 0 when it can't find one.
func vKeyCode() uint16 {
	src := tisCopyCurrentKeyboardLayout()
	if src == 0 {
		return 0
	}
	defer cfRelease(src)
	// The property is owned by src, so it is not released here.
	data := tisGetInputSourceProperty(src, layoutDataKey)
	if data == 0 {
		return 0
	}
	layout := cfDataGetBytePtr(data)
	if layout == 0 {
		return 0
	}
	kbd := uint32(lmGetKbdType())
	var (
		buf  [4]uint16
		n    uint32
		dead uint32
	)
	for code := range uint16(128) {
		dead = 0 // each key starts with no dead-key state
		status := ucKeyTranslate(layout, code, 0 /* key down */, 0, kbd, 1 /* no dead keys */, unsafe.Pointer(&dead), uint32(len(buf)), unsafe.Pointer(&n), unsafe.Pointer(&buf[0]))
		if status == 0 && n == 1 && buf[0] == 'v' {
			return code
		}
	}
	return 0
}

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

// Paste sends Command+V (with the key Remember found, else kVK_ANSI_V), then
// Left left times.
func (System) Paste(t Target, left int) error {
	if err := load(); err != nil {
		return err
	}
	time.Sleep(settle)
	v := t.KeyV
	if v == 0 {
		v = keyV
	}
	tap(v, flagCommand)
	for range left {
		tap(keyLeft, 0)
	}
	return nil
}
