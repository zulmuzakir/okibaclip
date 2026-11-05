package accel

import (
	"runtime"
	"testing"
)

func TestAccelKey(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"shift+ctrl+k":     "Ctrl+Shift+K",
		"Control+Shift+K":  "Ctrl+Shift+K",
		"Shift+Ctrl+k":     "Ctrl+Shift+K",
		"cmd+option+enter": "Super+Alt+enter",
	}
	if runtime.GOOS != "darwin" {
		cases["cmdorctrl+1"] = "Ctrl+1"
	} else {
		cases["cmdorctrl+1"] = "Super+1"
	}
	for in, want := range cases {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}
