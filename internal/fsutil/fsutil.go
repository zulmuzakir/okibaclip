// Package fsutil holds small file helpers shared across packages.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data at mode 0600. It writes a temp
// file in the same directory and renames it over path, so a failed write
// keeps the old file and an existing file's mode never survives.
func WriteFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if e := f.Sync(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = Restrict(tmp)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
