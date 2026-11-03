//go:build darwin

package capture

import (
	"sync"

	"github.com/ebitengine/purego/objc"
)

var (
	pbOnce sync.Once
	pbID   objc.ID
)

// changeToken is NSPasteboard.changeCount. Call only after the app is
// ready, so AppKit is loaded.
func changeToken() (uint64, bool) {
	pbOnce.Do(func() {
		if cls := objc.GetClass("NSPasteboard"); cls != 0 {
			pbID = objc.ID(cls).Send(objc.RegisterName("generalPasteboard"))
		}
	})
	if pbID == 0 {
		return 0, false
	}
	return uint64(objc.Send[int](pbID, objc.RegisterName("changeCount"))), true
}
