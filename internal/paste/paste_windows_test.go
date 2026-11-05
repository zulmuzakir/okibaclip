//go:build windows

package paste

import "testing"

func TestReleaseHeld(t *testing.T) {
	got := releaseHeld(func(vk int32) bool { return vk == 0xA0 })
	if len(got) != 1 {
		t.Fatalf("releaseHeld returned %d events, want 1", len(got))
	}
	if got[0].ki.vk != 0xA0 || got[0].ki.flags != keyEventKeyUp {
		t.Fatalf("releaseHeld = vk %#x flags %#x, want vk 0xA0 flags %#x", got[0].ki.vk, got[0].ki.flags, keyEventKeyUp)
	}
}
