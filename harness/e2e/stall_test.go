package e2e

import (
	"sync"
	"time"
)

// stalls measures how long a loaded machine held this whole process: a
// goroutine sleeping stallTick records how late it woke. Timing checks
// subtract the stall inside their interval, so they hold under -race while
// a wrong timer still fails.
var stalls stallWatch

// stallTick is how long the watcher sleeps at a time; stallFloor the
// least lateness it records, above a sleep's ordinary overshoot.
const (
	stallTick  = time.Millisecond
	stallFloor = time.Millisecond
)

type stallWatch struct {
	start sync.Once
	mu    sync.Mutex
	// spans are the times the watcher woke late, in order: from when its
	// sleep was due to when it woke.
	spans []stallSpan
}

type stallSpan struct {
	due, woke time.Time
}

// watch starts the watcher, once per test process; it runs until the
// process exits.
func (w *stallWatch) watch() {
	w.start.Do(func() {
		go func() {
			for {
				due := time.Now().Add(stallTick)
				time.Sleep(stallTick)
				if woke := time.Now(); woke.Sub(due) >= stallFloor {
					w.mu.Lock()
					w.spans = append(w.spans, stallSpan{
						due:  due,
						woke: woke,
					})
					w.mu.Unlock()
				}
			}
		}()
	})
}

// stalled is how long the process was held within [from, to]: the
// watcher's late wake-ups clipped to it. Zero before watch.
func (w *stallWatch) stalled(from, to time.Time) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	var d time.Duration
	for _, s := range w.spans {
		lo, hi := s.due, s.woke
		if lo.Before(from) {
			lo = from
		}
		if hi.After(to) {
			hi = to
		}
		if hi.After(lo) {
			d += hi.Sub(lo)
		}
	}
	return d
}
