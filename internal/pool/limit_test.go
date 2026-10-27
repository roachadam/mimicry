package pool

import (
	"testing"
)

// newLimitedFixture is newFixture with at most max exclusive connections
// per key.
func newLimitedFixture(t *testing.T, limit int) *fixture {
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
	first := f.getWaiting(t.Context(), "a")
	second := f.getWaiting(t.Context(), "a")
	f.pool.Release("a", c, true)
	want(t, <-first, FoundExclusive)
	pending(t, second)
	f.pool.Release("a", c, true)
	want(t, <-second, FoundExclusive)
}
