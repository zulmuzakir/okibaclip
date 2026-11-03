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
	win := xproto.Window(xgb.Get32(v))
	if win == 0 {
		return Owner{}
	}
	if x11.pid != 0 {
		if p := x11Prop(win, x11.pid, xproto.GetPropertyTypeAny, 1); len(p) >= 4 && int(xgb.Get32(p)) == os.Getpid() {
			return Owner{}
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
		return Owner{}
	}
	return Owner{App: strings.ToLower(class), Name: class}
}
