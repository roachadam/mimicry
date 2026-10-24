package pool

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
)

const idleTimeout = 90 * time.Second

// testClock is a fake clock whose timer callbacks report on fired once
// they have run, so a test knows when a reap or an eviction is done.
type testClock struct {
	*clockwork.FakeClock
	fired chan struct{}
}

func (c *testClock) AfterFunc(d time.Duration, f func()) clockwork.Timer {
	return c.FakeClock.AfterFunc(d, func() {
		f()
		c.fired <- struct{}{}
	})
}

// fakeShared is a multiplexed connection.
type fakeShared struct {
	pool    *Pool[*fakeShared, *fakeExclusive]
	t       *testing.T
	canTake atomic.Bool
	idle    atomic.Bool
	closed  atomic.Int32
	done    chan struct{}
}

func (c *fakeShared) CanTakeNewRequest() bool {
	return c.canTake.Load()
}

func (c *fakeShared) Idle() bool {
	return c.idle.Load()
}

func (c *fakeShared) Done() <-chan struct{} {
	return c.done
}

func (c *fakeShared) Close() error {
	c.t.Helper()
	checkUnlocked(c.t, c.pool, "Close")
	c.closed.Add(1)
	return nil
}

// fakeExclusive is an HTTP/1.1 connection.
type fakeExclusive struct {
	pool   *Pool[*fakeShared, *fakeExclusive]
	t      *testing.T
	usable atomic.Bool
	checks atomic.Int32
	closed chan struct{}
}

func (c *fakeExclusive) Usable() bool {
	checkUnlocked(c.t, c.pool, "Usable")
	c.checks.Add(1)
	return c.usable.Load()
}

func (c *fakeExclusive) Close() error {
	checkUnlocked(c.t, c.pool, "Close")
	close(c.closed)
	return nil
}

func (c *fakeExclusive) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// checkUnlocked fails the test if the pool's lock is held while a
// connection does I/O. Tests that use it run the pool from one goroutine.
func checkUnlocked(t *testing.T, p *Pool[*fakeShared, *fakeExclusive], what string) {
	t.Helper()
	if !p.mu.TryLock() {
		t.Errorf("%s called with the pool locked", what)
		return
	}
	p.mu.Unlock()
}

type fixture struct {
	t     *testing.T
	clock *testClock
	pool  *Pool[*fakeShared, *fakeExclusive]
	// waiting receives a key each time a Get starts waiting for a dial.
	waiting chan string
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{
		t: t,
		clock: &testClock{
			FakeClock: clockwork.NewFakeClockAt(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)),
			fired:     make(chan struct{}, 64),
		},
		waiting: make(chan string, 16),
	}
	f.pool = New[*fakeShared, *fakeExclusive](Config{
		Clock:       f.clock,
		IdleTimeout: idleTimeout,
		OnWait: func(key string) {
			f.waiting <- key
		},
	})
	return f
}

func (f *fixture) shared() *fakeShared {
	c := &fakeShared{
		pool: f.pool,
		t:    f.t,
		done: make(chan struct{}),
	}
	c.canTake.Store(true)
	f.t.Cleanup(func() {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	})
	return c
}

func (f *fixture) exclusive() *fakeExclusive {
	c := &fakeExclusive{
		pool:   f.pool,
		t:      f.t,
		closed: make(chan struct{}),
	}
	c.usable.Store(true)
	return c
}

// get calls Get and wants found.
func (f *fixture) get(key string, found Found) Got[*fakeShared, *fakeExclusive] {
	f.t.Helper()
	got, err := f.pool.Get(f.t.Context(), key)
	if err != nil {
		f.t.Fatalf("Get(%q): %v", key, err)
	}
	if got.Found != found {
		f.t.Fatalf("Get(%q) found %d, want %d", key, got.Found, found)
	}
	return got
}

// dialExclusive gets a dial for key and makes it an exclusive connection.
func (f *fixture) dialExclusive(key string) *fakeExclusive {
	f.t.Helper()
	got := f.get(key, FoundDial)
	got.Dial.Exclusive()
	return f.exclusive()
}

// advance moves the clock by d and waits for n timers to have run.
func (f *fixture) advance(d time.Duration, n int) {
	f.t.Helper()
	f.clock.Advance(d)
	for range n {
		select {
		case <-f.clock.fired:
		case <-f.t.Context().Done():
			f.t.Fatal("timer did not fire")
		}
	}
}

// getWaiting runs Get in a goroutine and returns its result channel once
// the Get waits for a dial.
func (f *fixture) getWaiting(ctx context.Context, key string) <-chan result {
	f.t.Helper()
	out := make(chan result, 1)
	go func() {
		got, err := f.pool.Get(ctx, key)
		out <- result{
			got: got,
			err: err,
		}
	}()
	select {
	case k := <-f.waiting:
		if k != key {
			f.t.Fatalf("waiting for %q, want %q", k, key)
		}
	case r := <-out:
		f.t.Fatalf("Get returned %+v without waiting for the dial", r)
	}
	return out
}

type result struct {
	got Got[*fakeShared, *fakeExclusive]
	err error
}

func TestSharedConnectionCarriesLaterRequests(t *testing.T) {
	f := newFixture(t)
	c := f.shared()
	if got := f.get("a", FoundDial).Dial.Shared(c); got != c {
		t.Fatal("Shared did not return the dialed connection")
	}
	if got := f.get("a", FoundShared); got.Shared != c {
		t.Fatal("second request did not get the shared connection")
	}
	f.get("b", FoundDial)
}

func TestWaitersShareTheDialsConnection(t *testing.T) {
	f := newFixture(t)
	d := f.get("a", FoundDial).Dial
	waiter := f.getWaiting(t.Context(), "a")
	c := f.shared()
	d.Shared(c)
	r := <-waiter
	if r.err != nil || r.got.Found != FoundShared || r.got.Shared != c {
		t.Fatalf("waiter got %+v, %v; want the dialed shared connection", r.got, r.err)
	}
}

func TestWaitersGetTheDialsError(t *testing.T) {
	f := newFixture(t)
	d := f.get("a", FoundDial).Dial
	waiter := f.getWaiting(t.Context(), "a")
	dialErr := errors.New("connection refused")
	d.Fail(dialErr)
	if r := <-waiter; !errors.Is(r.err, dialErr) {
		t.Fatalf("waiter got %+v, %v; want the dial's error", r.got, r.err)
	}
	// The error is the failed dial's only: a later request dials again.
	f.get("a", FoundDial)
}

func TestWaitersDialWhenTheDialersContextEnds(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	got, err := f.pool.Get(ctx, "a")
	if err != nil || got.Found != FoundDial {
		t.Fatalf("Get = %+v, %v; want a dial", got, err)
	}
	waiter := f.getWaiting(t.Context(), "a")
	cancel()
	got.Dial.Fail(ctx.Err())
	r := <-waiter
	if r.err != nil || r.got.Found != FoundDial {
		t.Fatalf("waiter got %+v, %v; want a dial of its own", r.got, r.err)
	}
}

func TestWaiterHonoursItsContext(t *testing.T) {
	f := newFixture(t)
	f.get("b", FoundDial)
	ctx, cancel := context.WithCancel(t.Context())
	waiter := f.getWaiting(ctx, "b")
	cancel()
	if r := <-waiter; !errors.Is(r.err, context.Canceled) {
		t.Fatalf("waiter got %+v, %v; want context.Canceled", r.got, r.err)
	}
}

func TestHTTP1ServerDialsInParallel(t *testing.T) {
	f := newFixture(t)
	f.dialExclusive("b")
	// The first connection is still busy: the next request dials its own
	// without waiting, and so does the one after while that dial runs.
	f.get("b", FoundDial)
	f.get("b", FoundDial)
}

func TestIdleConnectionIsReusedMostRecentFirst(t *testing.T) {
	f := newFixture(t)
	c1 := f.dialExclusive("a")
	c2 := f.dialExclusive("a")
	f.pool.Release("a", c1, true)
	f.pool.Release("a", c2, true)
	if got := f.get("a", FoundExclusive); got.Exclusive != c2 {
		t.Fatal("did not get the most recently used idle connection")
	}
	if got := f.get("a", FoundExclusive); got.Exclusive != c1 {
		t.Fatal("did not get the other idle connection")
	}
	if c1.checks.Load() != 1 || c2.checks.Load() != 1 {
		t.Fatal("idle connections were not checked before reuse")
	}
}

func TestDeadIdleConnectionIsClosedNotReused(t *testing.T) {
	f := newFixture(t)
	c := f.dialExclusive("a")
	f.pool.Release("a", c, true)
	c.usable.Store(false)
	f.get("a", FoundDial)
	if !c.isClosed() {
		t.Fatal("the dead connection was not closed")
	}
}

func TestUnreusableConnectionIsClosed(t *testing.T) {
	f := newFixture(t)
	c := f.dialExclusive("a")
	f.pool.Release("a", c, false)
	if !c.isClosed() {
		t.Fatal("not closed")
	}
	f.get("a", FoundDial)
}

func TestIdleConnectionIsReapedAfterTheIdleTimeout(t *testing.T) {
	f := newFixture(t)
	c := f.dialExclusive("a")
	f.pool.Release("a", c, true)
	f.clock.Advance(idleTimeout - time.Second)
	if c.isClosed() {
		t.Fatal("closed before the idle timeout")
	}
	f.advance(time.Second, 1)
	if !c.isClosed() {
		t.Fatal("not closed after the idle timeout")
	}
	f.get("a", FoundDial)
}

func TestReusedConnectionIsNotReaped(t *testing.T) {
	f := newFixture(t)
	c := f.dialExclusive("a")
	f.pool.Release("a", c, true)
	f.get("a", FoundExclusive)
	f.clock.Advance(2 * idleTimeout)
	if c.isClosed() {
		t.Fatal("a connection in use was reaped")
	}
}

func TestKeyIsForgottenOnceItHasNoConnections(t *testing.T) {
	f := newFixture(t)
	c := f.dialExclusive("a")
	f.pool.Release("a", c, true)
	f.advance(idleTimeout, 1) // reaps c
	f.pool.mu.Lock()
	kept := len(f.pool.keys)
	f.pool.mu.Unlock()
	if kept != 1 {
		t.Fatal("the key was forgotten as its last connection closed")
	}
	f.advance(idleTimeout, 1)
	f.pool.mu.Lock()
	kept = len(f.pool.keys)
	f.pool.mu.Unlock()
	if kept != 0 {
		t.Fatalf("%d keys kept after the idle timeout with no connections", kept)
	}
	// What the pool knew (HTTP/1.1 only) is gone: requests wait for one
	// dial again.
	f.get("a", FoundDial)
	f.getWaiting(t.Context(), "a")
}

func TestKeyInUseIsNotForgotten(t *testing.T) {
	f := newFixture(t)
	c := f.dialExclusive("a")
	f.pool.Release("a", c, false)
	f.get("a", FoundDial).Dial.Exclusive()
	f.clock.Advance(2 * idleTimeout)
	f.pool.mu.Lock()
	kept := len(f.pool.keys)
	f.pool.mu.Unlock()
	if kept != 1 {
		t.Fatal("a key with a connection in use was forgotten")
	}
}

func TestGoingAwayConnectionLeavesThePoolUnclosed(t *testing.T) {
	f := newFixture(t)
	c := f.shared()
	f.get("a", FoundDial).Dial.Shared(c)
	c.canTake.Store(false)
	f.get("a", FoundDial)
	if c.closed.Load() != 0 {
		t.Fatal("a connection going away was closed: its requests in flight would fail")
	}
}

func TestRemoveSharedDoesNotClose(t *testing.T) {
	f := newFixture(t)
	c := f.shared()
	f.get("a", FoundDial).Dial.Shared(c)
	f.pool.RemoveShared("a", c)
	if c.closed.Load() != 0 {
		t.Fatal("RemoveShared closed the connection")
	}
	f.get("a", FoundDial)
}

func TestClosedSharedConnectionLeavesThePool(t *testing.T) {
	f := newFixture(t)
	c := f.shared()
	f.get("a", FoundDial).Dial.Shared(c)
	close(c.done)
	// The pool's watcher removes it, which schedules the key's eviction.
	if err := f.clock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	f.get("a", FoundDial)
}

func TestSecondSharedConnectionIsClosed(t *testing.T) {
	f := newFixture(t)
	f.dialExclusive("a")
	d1 := f.get("a", FoundDial).Dial
	d2 := f.get("a", FoundDial).Dial
	c1, c2 := f.shared(), f.shared()
	if got := d1.Shared(c1); got != c1 {
		t.Fatal("first shared connection not used")
	}
	if got := d2.Shared(c2); got != c1 {
		t.Fatal("second dial did not get the first shared connection")
	}
	if c2.closed.Load() != 1 || c1.closed.Load() != 0 {
		t.Fatal("want the second shared connection closed, the first open")
	}
}

func TestCloseIdle(t *testing.T) {
	f := newFixture(t)
	idle := f.dialExclusive("a")
	busy := f.dialExclusive("a")
	f.pool.Release("a", idle, true)
	quiet, active := f.shared(), f.shared()
	quiet.idle.Store(true)
	f.get("b", FoundDial).Dial.Shared(quiet)
	f.get("c", FoundDial).Dial.Shared(active)
	f.pool.CloseIdle()
	if !idle.isClosed() || quiet.closed.Load() != 1 {
		t.Fatal("idle connections were not closed")
	}
	if busy.isClosed() || active.closed.Load() != 0 {
		t.Fatal("connections in use were closed")
	}
	f.get("c", FoundShared)
}
