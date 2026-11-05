//go:build linux

package capture

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
)

// selWatch counts CLIPBOARD owner changes reported by XFixes.
type selWatch struct {
	conn  *xgb.Conn
	n     atomic.Uint64
	owner atomic.Pointer[Owner] // app that took the selection at the last change
	alive atomic.Bool
}

// watchSelection connects to display ("" means $DISPLAY) and counts CLIPBOARD
// owner changes. The connection is not reopened when it dies; the token then
// reports unavailable and the monitor falls back to fingerprints.
func watchSelection(display string) (*selWatch, error) {
	c, err := xgb.NewConnDisplay(display)
	if err != nil {
		return nil, err
	}
	if err := xfixes.Init(c); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := xfixes.QueryVersion(c, 5, 0).Reply(); err != nil {
		c.Close()
		return nil, err
	}
	atom, err := xproto.InternAtom(c, false, uint16(len("CLIPBOARD")), "CLIPBOARD").Reply()
	if err != nil {
		c.Close()
		return nil, err
	}
	root := xproto.Setup(c).DefaultScreen(c).Root
	mask := uint32(xfixes.SelectionEventMaskSetSelectionOwner |
		xfixes.SelectionEventMaskSelectionWindowDestroy |
		xfixes.SelectionEventMaskSelectionClientClose)
	if err := xfixes.SelectSelectionInputChecked(c, root, atom.Atom, mask).Check(); err != nil {
		c.Close()
		return nil, err
	}
	w := &selWatch{conn: c}
	w.alive.Store(true)
	go func() {
		for {
			ev, xerr := w.conn.WaitForEvent()
			if ev == nil && xerr == nil {
				w.alive.Store(false)
				return
			}
			if sn, ok := ev.(xfixes.SelectionNotifyEvent); ok {
				o := Owner{}
				if sn.Subtype == xfixes.SelectionEventSetSelectionOwner {
					o = selectionOwner(sn.Owner)
				}
				w.owner.Store(&o)
				w.n.Add(1)
			}
		}
	}()
	return w, nil
}

// token returns the change count while the connection is alive.
func (w *selWatch) token() (uint64, bool) {
	if !w.alive.Load() {
		return 0, false
	}
	return w.n.Load(), true
}

func (w *selWatch) close() { w.conn.Close() }

// useX11Token reports whether the session talks to X11: a DISPLAY is set and
// either no Wayland display is, or GTK is forced to X11.
func useX11Token() bool {
	return os.Getenv("DISPLAY") != "" && (os.Getenv("WAYLAND_DISPLAY") == "" || os.Getenv("GDK_BACKEND") == "x11")
}

var xsel struct {
	once sync.Once
	w    *selWatch
}

// selectionWatch starts the XFixes watch on first use. It is nil when the
// session is not X11 or the watch could not start.
func selectionWatch() *selWatch {
	xsel.once.Do(func() {
		if !useX11Token() {
			return
		}
		w, err := watchSelection("")
		if err != nil {
			slog.Info("x11 selection watch unavailable", "err", err)
			return
		}
		xsel.w = w
	})
	return xsel.w
}

// changeToken is the XFixes CLIPBOARD owner-change count on X11. It reports
// false when the session is not X11 or the watch could not start.
func changeToken() (uint64, bool) {
	w := selectionWatch()
	if w == nil {
		return 0, false
	}
	return w.token()
}
