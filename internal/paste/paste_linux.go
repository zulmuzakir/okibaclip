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

func (System) Remember() Target  { return Target{} }
func (System) Trusted(bool) bool { return true }

// modifierKeysyms are Shift, Control, Alt and Super. Paste releases any still
// held (the panel hotkey may be one), so they don't turn Ctrl+V into another chord.
var modifierKeysyms = []xproto.Keysym{0xffe1, 0xffe2, 0xffe3, 0xffe4, 0xffe9, 0xffea, 0xffeb, 0xffec}

// heldCodes returns the codes whose bit is set in keys, a QueryKeymap bitmap
// indexed by keycode.
func heldCodes(keys []byte, codes []byte) []byte {
	var held []byte
	for _, c := range codes {
		if int(c/8) < len(keys) && keys[c/8]&(1<<(c%8)) != 0 {
			held = append(held, c)
		}
	}
	return held
}

// Paste releases held modifiers, then sends Ctrl+V and Left left times, through XTEST.
// The target is unused: X11 sends to the focused window.
func (System) Paste(_ Target, left int) error {
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
	var mods []byte
	for _, sym := range modifierKeysyms {
		if code := x11.keys[sym]; code != 0 {
			mods = append(mods, code)
		}
	}
	rep, err := xproto.QueryKeymap(c).Reply()
	if err != nil {
		return err
	}
	if rep == nil {
		return errors.New("X11 QueryKeymap returned no data")
	}
	send := func(code byte, down bool) {
		typ := byte(xproto.KeyRelease)
		if down {
			typ = xproto.KeyPress
		}
		xtest.FakeInput(c, typ, code, 0, x11.root, 0, 0, 0)
	}
	for _, code := range heldCodes(rep.Keys, mods) {
		send(code, false)
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
