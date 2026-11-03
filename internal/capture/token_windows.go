//go:build windows

package capture

import "syscall"

var procSeq = syscall.NewLazyDLL("user32.dll").NewProc("GetClipboardSequenceNumber")

func changeToken() (uint64, bool) {
	r, _, _ := procSeq.Call()
	return uint64(r), r != 0
}
