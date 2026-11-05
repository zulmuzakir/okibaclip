//go:build darwin

package capture

import (
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

// goString copies a NUL-terminated C string.
func goString(p uintptr) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p)); ; ptr = unsafe.Add(ptr, 1) {
		c := *(*byte)(ptr)
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

func nsString(id objc.ID) string {
	if id == 0 {
		return ""
	}
	return goString(objc.Send[uintptr](id, objc.RegisterName("UTF8String")))
}

// frontOwner is the frontmost application, which is what the user copied
// from.
func frontOwner() Owner {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ws := objc.GetClass("NSWorkspace")
	pool := objc.GetClass("NSAutoreleasePool")
	if ws == 0 || pool == 0 {
		return Owner{}
	}
	p := objc.ID(pool).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	defer p.Send(objc.RegisterName("drain"))
	front := objc.ID(ws).Send(objc.RegisterName("sharedWorkspace")).Send(objc.RegisterName("frontmostApplication"))
	if front == 0 {
		return Owner{}
	}
	if objc.Send[int32](front, objc.RegisterName("processIdentifier")) == int32(os.Getpid()) {
		return Owner{}
	}
	name := nsString(front.Send(objc.RegisterName("localizedName")))
	app := nsString(front.Send(objc.RegisterName("bundleIdentifier")))
	if app == "" {
		app = name
	}
	path := ""
	if url := front.Send(objc.RegisterName("bundleURL")); url != 0 {
		path = nsString(url.Send(objc.RegisterName("path")))
	}
	if app == "" {
		return Owner{}
	}
	return Owner{App: strings.ToLower(app), Name: name, Path: path}
}

// frontPID is the frontmost application's process ID, or 0.
func frontPID() int32 {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ws := objc.GetClass("NSWorkspace")
	pool := objc.GetClass("NSAutoreleasePool")
	if ws == 0 || pool == 0 {
		return 0
	}
	p := objc.ID(pool).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	defer p.Send(objc.RegisterName("drain"))
	front := objc.ID(ws).Send(objc.RegisterName("sharedWorkspace")).Send(objc.RegisterName("frontmostApplication"))
	if front == 0 {
		return 0
	}
	return objc.Send[int32](front, objc.RegisterName("processIdentifier"))
}

var (
	darwinFront  frontLog
	lastFrontPID atomic.Int32
)

// noteFront records the frontmost app whenever it changes.
func noteFront() {
	if pid := frontPID(); pid != lastFrontPID.Swap(pid) {
		darwinFront.note(frontOwner(), time.Now())
	}
}

// clipOwners are the apps that were frontmost within the last few seconds.
func clipOwners() []Owner {
	noteFront()
	return darwinFront.owners(time.Now())
}
