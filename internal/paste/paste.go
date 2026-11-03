// Package paste sends the system paste shortcut to the focused app, for the
// opt-in "Paste after choosing" setting.
package paste

import "time"

// settle is how long Paste waits so the panel has handed focus back.
const settle = 150 * time.Millisecond

// System sends real key events. The zero value is ready to use.
type System struct{}
