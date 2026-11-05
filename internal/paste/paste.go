// Package paste sends the system paste shortcut to the focused app, for the
// opt-in "Paste after choosing" setting.
package paste

import (
	"errors"
	"time"
)

// settle is how long Paste waits so the panel has handed focus back.
const settle = 150 * time.Millisecond

// Target is where Paste sends its keys, captured by Remember on the main thread.
type Target struct {
	Window uintptr // Windows: HWND to refocus; 0 = none
	KeyV   uint16  // macOS: virtual key code of "v" in the current layout; 0 = kVK_ANSI_V
}

// ErrNoTarget means there is no window to paste into.
var ErrNoTarget = errors.New("no window to paste into")

// System sends real key events. The zero value is ready to use.
type System struct{}
