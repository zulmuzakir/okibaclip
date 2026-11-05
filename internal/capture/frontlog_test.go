package capture

import (
	"slices"
	"testing"
	"time"
)

func TestFrontLogKeepsRecentApps(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a, b := Owner{App: "a"}, Owner{App: "b"}
	var l frontLog
	l.note(Owner{}, t0)
	l.note(a, t0)
	l.note(b, t0.Add(time.Second))
	if got := l.owners(t0.Add(time.Second)); !slices.Equal(got, []Owner{b, a}) {
		t.Errorf("owners = %v; want [b a]", got)
	}
	if got := l.owners(t0.Add(5 * time.Second)); !slices.Equal(got, []Owner{b}) {
		t.Errorf("owners after the window = %v; want [b]", got)
	}
}
