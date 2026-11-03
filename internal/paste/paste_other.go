//go:build !darwin && !windows && !linux

package paste

import "errors"

// Available reports whether pasting can work here; note explains limits or
// why not.
func Available(waylandMode string) (ok bool, note string) {
	return false, "Not supported on this system."
}

func (System) Remember()         {}
func (System) Trusted(bool) bool { return true }
func (System) Paste(int) error   { return errors.ErrUnsupported }
