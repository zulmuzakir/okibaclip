package capture

import (
	"sync"
	"time"
)

const frontWindow = 3 * time.Second // longer than the slowest poll (1000 ms) plus slack

type seenOwner struct {
	o     Owner
	until time.Time
}

// frontLog remembers the apps that were frontmost recently, so a copy
// followed by a quick app switch is still matched against the ignore list.
type frontLog struct {
	mu     sync.Mutex
	cur    Owner
	recent []seenOwner // oldest first
}

// note records o as the frontmost app at now.
func (l *frontLog) note(o Owner, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if o.App != l.cur.App {
		if l.cur.App != "" {
			l.recent = append(l.recent, seenOwner{l.cur, now.Add(frontWindow)})
		}
		l.cur = o
	}
	l.expire(now)
}

// owners lists the current frontmost app, then the unexpired earlier ones, newest first.
func (l *frontLog) owners(now time.Time) []Owner {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	out := appendOwner(nil, l.cur)
	for i := len(l.recent) - 1; i >= 0; i-- {
		out = appendOwner(out, l.recent[i].o)
	}
	return out
}

func (l *frontLog) expire(now time.Time) {
	keep := l.recent[:0]
	for _, r := range l.recent {
		if now.Before(r.until) {
			keep = append(keep, r)
		}
	}
	l.recent = keep
}
