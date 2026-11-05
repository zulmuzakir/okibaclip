//go:build linux

package capture

import (
	"bytes"
	"os"
	"strings"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

var x11 struct {
	once sync.Once
	conn *xgb.Conn
	root xproto.Window

	active, pid xproto.Atom
}

func x11Init() {
	c, err := xgb.NewConn()
	if err != nil {
		return
	}
	atom := func(name string) xproto.Atom {
		r, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply()
		if err != nil {
			return 0
		}
		return r.Atom
	}
	x11.root = xproto.Setup(c).DefaultScreen(c).Root
	x11.active, x11.pid = atom("_NET_ACTIVE_WINDOW"), atom("_NET_WM_PID")
	x11.conn = c
}

func x11Prop(win xproto.Window, atom xproto.Atom, typ xproto.Atom, words uint32) []byte {
	r, err := xproto.GetProperty(x11.conn, false, win, atom, typ, 0, words).Reply()
	if err != nil || r == nil {
		return nil
	}
	return r.Value
}

// windowOwner is the app that owns win, from its WM_CLASS. self reports that
// the window belongs to this process.
func windowOwner(win xproto.Window) (o Owner, self bool) {
	x11.once.Do(x11Init)
	if x11.conn == nil || win == 0 {
		return Owner{}, false
	}
	if x11.pid != 0 {
		if p := x11Prop(win, x11.pid, xproto.GetPropertyTypeAny, 1); len(p) >= 4 && int(xgb.Get32(p)) == os.Getpid() {
			return Owner{}, true
		}
	}
	// WM_CLASS is "instance\0class\0".
	parts := bytes.Split(bytes.TrimRight(x11Prop(win, xproto.AtomWmClass, xproto.AtomString, 256), "\x00"), []byte{0})
	class := ""
	if len(parts) >= 2 {
		class = string(parts[1])
	} else if len(parts) == 1 {
		class = string(parts[0])
	}
	if class == "" {
		return Owner{}, false
	}
	return Owner{App: strings.ToLower(class), Name: class}, false
}

// frontOwner is the window class of the X11 active window. Wayland-native
// apps have none.
func frontOwner() Owner {
	x11.once.Do(x11Init)
	if x11.conn == nil || x11.active == 0 {
		return Owner{}
	}
	v := x11Prop(x11.root, x11.active, xproto.GetPropertyTypeAny, 1)
	if len(v) < 4 {
		return Owner{}
	}
	o, self := windowOwner(xproto.Window(xgb.Get32(v)))
	if self {
		return Owner{}
	}
	return o
}

// selectionOwner is the app owning the CLIPBOARD selection window win. Toolkit
// helper windows often lack WM_CLASS; then it is the active window now.
func selectionOwner(win xproto.Window) Owner {
	o, self := windowOwner(win)
	if self {
		return Owner{}
	}
	if o.App != "" {
		return o
	}
	return frontOwner()
}

func noteFront() {}

// clipOwners lists the CLIPBOARD owner seen by the selection watch, then the
// active window.
func clipOwners() []Owner {
	var out []Owner
	if w := selectionWatch(); w != nil && w.alive.Load() {
		if p := w.owner.Load(); p != nil {
			out = appendOwner(out, *p)
		}
	}
	return appendOwner(out, frontOwner())
}
