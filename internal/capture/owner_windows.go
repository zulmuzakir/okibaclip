//go:build windows

package capture

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetClipboardOwner     = user32.NewProc("GetClipboardOwner")
	procGetForegroundWindow   = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcID = user32.NewProc("GetWindowThreadProcessId")
	procOpenProcess           = kernel32.NewProc("OpenProcess")
	procQueryImageName        = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle           = kernel32.NewProc("CloseHandle")
)

const processQueryLimitedInformation = 0x1000

// frontOwner is the clipboard owner's executable, or the foreground window's
// when the clipboard has no owner window.
func frontOwner() Owner {
	hwnd, _, _ := procGetClipboardOwner.Call()
	if hwnd == 0 {
		hwnd, _, _ = procGetForegroundWindow.Call()
	}
	if hwnd == 0 {
		return Owner{}
	}
	var pid uint32
	procGetWindowThreadProcID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 || int(pid) == os.Getpid() {
		return Owner{}
	}
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return Owner{}
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if r, _, _ := procQueryImageName.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return Owner{}
	}
	path := syscall.UTF16ToString(buf[:n])
	base := filepath.Base(path)
	return Owner{App: strings.ToLower(base), Name: strings.TrimSuffix(base, filepath.Ext(base)), Path: path}
}

func noteFront() {}

func clipOwners() []Owner { return appendOwner(nil, frontOwner()) }
