//go:build linux

package paste

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

const (
	keysymControlL = 0xffe3
	keysymV        = 0x76
	keysymLeft     = 0xff51
)

var x11 struct {
	mu   sync.Mutex
	conn *xgb.Conn
	root xproto.Window
	keys map[xproto.Keysym]byte
}

// connect returns the shared X11 connection with the keycodes Paste needs.
// Call with x11.mu held.
func connect() (*xgb.Conn, error) {
	if x11.conn != nil {
		return x11.conn, nil
	}
	c, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	if err := xtest.Init(c); err != nil {
		c.Close()
		return nil, fmt.Errorf("XTEST: %w", err)
	}
	setup := xproto.Setup(c)
	count := byte(setup.MaxKeycode - setup.MinKeycode + 1)
	m, err := xproto.GetKeyboardMapping(c, setup.MinKeycode, count).Reply()
	if err != nil {
		c.Close()
		return nil, err
	}
	keys := map[xproto.Keysym]byte{}
	per := int(m.KeysymsPerKeycode)
	for i, sym := range m.Keysyms {
		if _, ok := keys[sym]; !ok && per > 0 {
			keys[sym] = byte(int(setup.MinKeycode) + i/per)
		}
	}
	x11.conn, x11.root, x11.keys = c, setup.DefaultScreen(c).Root, keys
	return c, nil
}

// Available reports whether pasting can work here; note explains limits or
// why not.
func Available(waylandMode string) (bool, string) {
	switch waylandMode {
	case "wayland":
		return false, "Native Wayland does not let apps send keystrokes."
	case "xwayland":
		return true, "Under Wayland only X11 apps receive the paste."
	}
	return true, ""
}

func (System) Remember()         {}
func (System) Trusted(bool) bool { return true }

// Paste sends Ctrl+V, then Left left times, through XTEST.
func (System) Paste(left int) error {
	time.Sleep(settle)
	x11.mu.Lock()
	defer x11.mu.Unlock()
	c, err := connect()
	if err != nil {
		return err
	}
	ctrl, v, lk := x11.keys[keysymControlL], x11.keys[keysymV], x11.keys[keysymLeft]
	if ctrl == 0 || v == 0 || (left > 0 && lk == 0) {
		return errors.New("keyboard layout has no Control, V or Left key")
	}
	send := func(code byte, down bool) {
		typ := byte(xproto.KeyRelease)
		if down {
			typ = xproto.KeyPress
		}
		xtest.FakeInput(c, typ, code, 0, x11.root, 0, 0, 0)
	}
	send(ctrl, true)
	send(v, true)
	send(v, false)
	send(ctrl, false)
	for range left {
		send(lk, true)
		send(lk, false)
	}
	_, err = xproto.GetInputFocus(c).Reply()
	return err
}
