package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
)

var (
	isDarwin = runtime.GOOS == "darwin"
	isLinux  = runtime.GOOS == "linux"
)

// dataBase is the per-user, non-roaming folder that holds dataDirName.
func dataBase() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return d, nil
		}
		return "", errors.New("LOCALAPPDATA is not set")
	case "linux":
		if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
			return d, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share"), nil
	}
	return mygo.App.Path(mygo.PathAppData) // macOS: ~/Library/Application Support
}
