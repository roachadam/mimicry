package e2e

import (
	"sync"
	"time"
)

// A loaded test machine (the whole suite under -race) can hold every
// goroutine of this process, mimicry's QUIC loops and the capture
// server's alike, for tens of milliseconds. A check of when mimicry acted
// on a timer of its own then sees the timer late by the stall, not by the
// rule it holds. stalls measures those stalls directly, from the process's
// side: a goroutine sleeping stallTick at a time records how much later
// than due it woke. A timing check subtracts the stall time inside the
// interval it measures (stalled), so the rule holds on a loaded machine
// while a wrong timer still fails on an idle one, and on a loaded one
// wherever the process was not held as long.
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
