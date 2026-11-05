//go:build !windows

package fsutil

import "os"

// Restrict makes path accessible to its owner only: mode 0700 for a
// directory, 0600 for a file.
func Restrict(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}

// syncDir flushes dir's entries so a rename into it survives a crash.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
