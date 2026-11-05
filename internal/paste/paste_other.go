//go:build !darwin && !windows && !linux

package paste

import "errors"

// Available reports whether pasting can work here; note explains limits or
// why not.
func Available(waylandMode string) (ok bool, note string) {
	return false, "Not supported on this system."
}

func (System) Remember() Target            { return Target{} }
func (System) Trusted(bool) bool           { return true }
func (System) Paste(_ Target, _ int) error { return errors.ErrUnsupported }
