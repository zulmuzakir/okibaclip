//go:build linux

package capture

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSelectionWatchCountsOwnerChanges(t *testing.T) {
	for _, bin := range []string{"Xvfb", "xclip"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	xvfb := exec.Command("Xvfb", "-displayfd", "3", "-nolisten", "tcp")
	xvfb.ExtraFiles = []*os.File{pw}
	if err := xvfb.Start(); err != nil {
		pw.Close()
		t.Fatal(err)
	}
	pw.Close()
	t.Cleanup(func() {
		xvfb.Process.Kill()
		xvfb.Wait()
	})
	num, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("Xvfb did not report a display: %v", err)
	}
	display := ":" + strings.TrimSpace(num)

	w, err := watchSelection(display)
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	before, ok := w.token()
	if !ok {
		t.Fatal("token unavailable right after watch start")
	}

	// xclip forks a background owner and exits; Run returns once it has.
	cmd := exec.Command("xclip", "-selection", "clipboard", "-i")
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	cmd.Stdin = strings.NewReader("hi")
	if err := cmd.Run(); err != nil {
		t.Fatalf("xclip: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, _ := w.token(); n != before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("selection owner change not counted within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
