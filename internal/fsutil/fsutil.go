// Package fsutil holds small file helpers shared across packages.
package fsutil

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	if err == nil {
		syncDir(filepath.Dir(path)) // best effort: the new file is in place either way
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Quarantine renames path to path+".bad-"+now.Format("20060102-150405") and
// returns the new path. If that name is taken it appends "-1" to "-99", so an
// earlier quarantined file is never overwritten.
func Quarantine(path string, now time.Time) (string, error) {
	base := path + ".bad-" + now.Format("20060102-150405")
	bad := base
	for i := 1; ; i++ {
		_, err := os.Lstat(bad)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if i > 99 {
			return "", fmt.Errorf("quarantine %s: no free name", path)
		}
		bad = fmt.Sprintf("%s-%d", base, i)
	}
	if err := os.Rename(path, bad); err != nil {
		return "", err
	}
	return bad, nil
}

// RemoveQuarantined deletes every file Quarantine made from path
// (path+".bad-*", including their "-journal" companions).
func RemoveQuarantined(path string) error {
	dir := filepath.Dir(path)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	prefix := filepath.Base(path) + ".bad-"
	var errs []error
	for _, de := range ents {
		if strings.HasPrefix(de.Name(), prefix) {
			if err := os.Remove(filepath.Join(dir, de.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
