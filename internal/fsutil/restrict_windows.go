//go:build windows

package fsutil

import (
	"os"

	"golang.org/x/sys/windows"
)

// Restrict replaces path's ACL with one granting full control to the current
// user and SYSTEM only. Inherited entries are removed; for a directory the
// new entries are inherited by its children.
func Restrict(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return err
	}
	sid := u.User.Sid.String()
	inh := ""
	if fi.IsDir() {
		inh = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inh + ";FA;;;" + sid + ")(A;" + inh + ";FA;;;SY)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
