package pool

import (
	"testing"
)

// newLimitedFixture is newFixture with at most max exclusive connections
// per key.
func newLimitedFixture(t *testing.T, limit int) *fixture {
	t.Helper()

	f := newFixture(t)

	f.pool = New[*fakeShared, *fakeExclusive](Config{
		Clock:        f.clock,
		IdleTimeout:  idleTimeout,
		MaxExclusive: limit,
		OnWait: func(key string) {
			f.waiting <- key
		},
	})

	return f
}

// want reads r and wants found, without an error.
func want(t *testing.T, r result, found Found) {
	t.Helper()

	if r.err != nil || r.got.Found != found {
		t.Fatalf("waiter got %+v, %v; want found %d", r.got, r.err, found)
	}
}

// pending fails the test if a waiter has been handed anything.
func pending(t *testing.T, waiters ...<-chan result) {
	t.Helper()

	for i, w := range waiters {
		select {
		case r := <-w:
			t.Fatalf("waiter %d got %+v, %v; want it still waiting", i, r.got, r.err)
		default:
		}
	}
}

func TestWaitersAreServedInOrder(t *testing.T) {
	f := newLimitedFixture(t, 1)
	c := f.dialExclusive("a")

	c.handedOff = make(chan struct{}, 1)

	first := f.getWaiting(t.Context(), "a")
	second := f.getWaiting(t.Context(), "a")
	// release hands c to the oldest waiter, which checks it once Release
	// has let the pool's lock go.
	release := func() {
		f.pool.Release("a", c, true)

		c.handedOff <- struct{}{}
	}

	release()
	want(t, <-first, FoundExclusive)
	pending(t, second)
	release()
	want(t, <-second, FoundExclusive)
}
