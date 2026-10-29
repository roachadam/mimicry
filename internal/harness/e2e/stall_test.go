package e2e

import (
	"fmt"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
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

// raceStall reports the first QUIC attempt on page whose race against TCP
// this process's stalls decided, not mimicry: the server answered it within
// the profile's race delay net of the stalls before its answer, but that
// answer, with the stalls after it while the delay ran, came at or past the
// delay. A request racing it may then have gone on TCP (and Safari dropped
// the attempt) where, unstalled, it would have waited for QUIC. A capture
// read from a file (no Start) has none.
func raceStall(p *profile.Profile, page *capture.Page) (string, bool) {
	lo, hi := raceDelay(p)
	if lo == 0 || page.Start.IsZero() {
		return "", false
	}

	for i, c := range page.QUIC {
		if len(c.Server) == 0 {
			continue
		}

		answer := c.Server[0].Micros / 1000
		before := int64(stalledIn(page, c.Millis, answer))
		after := int64(stalledIn(page, c.Millis+answer, hi-answer))

		if answer-before < lo && answer+after >= lo {
			return fmt.Sprintf("QUIC connection %d (%s): its server answered after %d ms, %d of them with the process stalled, and the process stalled %d ms more within the race delay (%d..%d ms)", i+1, c.Host, answer, before, after, lo, hi), true
		}
	}

	return "", false
}

// raceDelay is how long, in ms, a request waits for an unanswered QUIC
// attempt before it goes on TCP: the profile's race delay, else its first
// probe timeout (how long fast fallback waits on a resumed attempt); zero
// where nothing waits.
func raceDelay(p *profile.Profile) (lo, hi int64) {
	if r := p.HTTP3.Discovery.Fallback.TCPDelayMillis; r != nil {
		return int64(r.Min), int64(r.Max)
	}

	ms := int64(p.QUIC.Loss.InitialPTOMillis)

	return ms, ms
}
