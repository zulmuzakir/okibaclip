//go:build linux

package paste

import (
	"slices"
	"testing"
)

func TestHeldCodes(t *testing.T) {
	keys := make([]byte, 32)
	for _, c := range []byte{50, 105} {
		keys[c/8] |= 1 << (c % 8)
	}
	got := heldCodes(keys, []byte{50, 62, 37, 105})
	if want := []byte{50, 105}; !slices.Equal(got, want) {
		t.Fatalf("heldCodes = %v, want %v", got, want)
	}
}
