package pool

import (
	"context"
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

// busy returns key's count of exclusive connections in use and dials.
func (f *fixture) busy(key string) int {
	f.pool.mu.Lock()
	defer f.pool.mu.Unlock()
	return f.pool.keys[key].busy
}

// want reads r and wants found, without an error.
func want(t *testing.T, r result, found Found) Got[*fakeShared, *fakeExclusive] {
	t.Helper()
	if r.err != nil || r.got.Found != found {
		t.Fatalf("waiter got %+v, %v; want found %d", r.got, r.err, found)
	}
	return r.got
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

func TestHTTP1ConnectionsAreCappedPerKey(t *testing.T) {
	f := newLimitedFixture(t, 2)
	c1 := f.dialExclusive("a")
	f.get("a", FoundDial)
	// Two connections busy: the third request waits, and another key does
	// not.
	waiter := f.getWaiting(t.Context(), "a")
	f.get("b", FoundDial)
	pending(t, waiter)
	f.pool.Release("a", c1, true)
	if got := want(t, <-waiter, FoundExclusive); got.Exclusive != c1 {
		t.Fatal("the waiter did not get the released connection")
	}
	if n := f.busy("a"); n != 2 {
		t.Fatalf("busy = %d, want 2", n)
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

func TestNewRequestDoesNotOvertakeWaiters(t *testing.T) {
	f := newLimitedFixture(t, 1)
	c := f.dialExclusive("a")
	first := f.getWaiting(t.Context(), "a")
	f.pool.Release("a", c, true)
	// The released connection went to the waiter, not to idle, where a
	// request made before the waiter runs could take it.
	f.pool.mu.Lock()
	idle, busy := len(f.pool.keys["a"].idle), f.pool.keys["a"].busy
	f.pool.mu.Unlock()
	if idle != 0 || busy != 1 {
		t.Fatalf("%d idle, %d busy; want the connection handed over: 0 idle, 1 busy", idle, busy)
	}
	want(t, <-first, FoundExclusive)
}

func TestClosedConnectionLetsAWaiterDial(t *testing.T) {
	f := newLimitedFixture(t, 1)
	c := f.dialExclusive("a")
	waiter := f.getWaiting(t.Context(), "a")
	f.pool.Release("a", c, false)
	got := want(t, <-waiter, FoundDial)
	if got.Dial.MayShare() {
		t.Fatal("a dial to an HTTP/1.1-only key is waited for")
	}
	f.getWaiting(t.Context(), "a")
}

func TestFailedDialLetsAWaiterDial(t *testing.T) {
	f := newLimitedFixture(t, 1)
	held := f.dialExclusive("a")
	waiter := f.getWaiting(t.Context(), "a")
	f.pool.Release("a", held, false)
	d := want(t, <-waiter, FoundDial).Dial
	next := f.getWaiting(t.Context(), "a")
	d.Fail(context.DeadlineExceeded)
	// A dial nobody waited for fails alone: the next request dials.
	want(t, <-next, FoundDial)
}

func TestWaitersForTheFirstDialDialUpToTheLimit(t *testing.T) {
	f := newLimitedFixture(t, 3)
	d := f.get("a", FoundDial).Dial
	if !d.MayShare() {
		t.Fatal("the first dial to a key is not waited for")
	}
	waiters := []<-chan result{
		f.getWaiting(t.Context(), "a"),
		f.getWaiting(t.Context(), "a"),
		f.getWaiting(t.Context(), "a"),
	}
	d.Exclusive()
	// The dialer holds one connection; the two oldest waiters dial.
	want(t, <-waiters[0], FoundDial)
	want(t, <-waiters[1], FoundDial)
	pending(t, waiters[2])
	c := f.exclusive()
	f.pool.Release("a", c, true)
	if got := want(t, <-waiters[2], FoundExclusive); got.Exclusive != c {
		t.Fatal("the last waiter did not get the released connection")
	}
}

func TestDeadHandedConnectionKeepsTheTurn(t *testing.T) {
	f := newLimitedFixture(t, 1)
	c := f.dialExclusive("a")
	waiter := f.getWaiting(t.Context(), "a")
	c.usable.Store(false)
	f.pool.Release("a", c, true)
	want(t, <-waiter, FoundDial)
	if !c.isClosed() {
		t.Fatal("the dead connection was not closed")
	}
	if n := f.busy("a"); n != 1 {
		t.Fatalf("busy = %d, want 1: the waiter dials in the dead connection's place", n)
	}
}

func TestCanceledWaiterPassesItsConnectionOn(t *testing.T) {
	f := newLimitedFixture(t, 1)
	c := f.dialExclusive("a")
	ctx, cancel := context.WithCancel(t.Context())
	first := f.getWaiting(ctx, "a")
	second := f.getWaiting(t.Context(), "a")
	cancel()
	if r := <-first; r.err == nil {
		t.Fatalf("canceled waiter got %+v", r.got)
	}
	f.pool.Release("a", c, true)
	if got := want(t, <-second, FoundExclusive); got.Exclusive != c {
		t.Fatal("the second waiter did not get the connection")
	}
}

func TestAdoptedConnectionCarriesTheKeysRequests(t *testing.T) {
	f := newFixture(t)
	c := f.shared()
	f.get("a", FoundDial).Dial.Shared(c)
	d := f.get("b", FoundDial).Dial
	waiter := f.getWaiting(t.Context(), "b")
	if got := d.Adopt(c); got != c {
		t.Fatal("Adopt did not return the adopted connection")
	}
	if got := want(t, <-waiter, FoundShared); got.Shared != c {
		t.Fatal("the waiter did not get the adopted connection")
	}
	if got := f.get("b", FoundShared); got.Shared != c {
		t.Fatal("a later request did not get the adopted connection")
	}
	if conns := f.pool.SharedConns(); len(conns) != 1 || conns[0] != c {
		t.Fatalf("SharedConns() = %v, want the one connection once", conns)
	}
	c.idle.Store(true)
	f.pool.CloseIdle()
	if n := c.closed.Load(); n != 1 {
		t.Fatalf("closed %d times, want once", n)
	}
}

func TestAdoptKeepsAnInstalledConnection(t *testing.T) {
	f := newFixture(t)
	f.dialExclusive("b")
	d1 := f.get("b", FoundDial).Dial
	d2 := f.get("b", FoundDial).Dial
	own, other := f.shared(), f.shared()
	d1.Shared(own)
	if got := d2.Adopt(other); got != own {
		t.Fatal("Adopt replaced the key's shared connection")
	}
	if other.closed.Load() != 0 {
		t.Fatal("Adopt closed another key's connection")
	}
}

func TestSharedConnsLeavesOutGoingAway(t *testing.T) {
	f := newFixture(t)
	c := f.shared()
	f.get("a", FoundDial).Dial.Shared(c)
	c.canTake.Store(false)
	if conns := f.pool.SharedConns(); len(conns) != 0 {
		t.Fatalf("SharedConns() = %v, want none", conns)
	}
}
