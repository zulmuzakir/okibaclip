// Package fsutil holds small file helpers shared across packages.
package fsutil

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"time"
)

// WriteFileAtomic replaces path with data at mode 0600. It writes a temp
// file in the same directory and renames it over path, so a failed write
// keeps the old file and an existing file's mode never survives.
func WriteFileAtomic(path string, data []byte) error {
	return WriteFileAtomicFunc(path, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// WriteFileAtomicFunc is WriteFileAtomic for content produced by write, which
// streams into a buffered temp file so large output never sits in memory.
func WriteFileAtomicFunc(path string, write func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	bw := bufio.NewWriter(f)
	err = write(bw)
	if e := bw.Flush(); err == nil {
		err = e
	}
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

// Quarantine renames path to path+".bad-"+now.Format("20060102-150405") and returns the new path.
func Quarantine(path string, now time.Time) (string, error) {
	bad := path + ".bad-" + now.Format("20060102-150405")
	if err := os.Rename(path, bad); err != nil {
		return "", err
	}
	return bad, nil
}
