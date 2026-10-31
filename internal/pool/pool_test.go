package pool

import (
	"context"
	"runtime"
	"sync"
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
	// idleSince is when its last request ended; zero while one is in
	// flight.
	idleSince atomic.Pointer[time.Time]
	closed    atomic.Int32
	expired   atomic.Int32
	// onClose holds what OnClose registered until end runs it.
	mu      sync.Mutex
	onClose []func()
	ended   bool
}

func (c *fakeShared) CanTakeNewRequest() bool {
	return c.canTake.Load()
}

func (c *fakeShared) IdleSince() (time.Time, bool) {
	if since := c.idleSince.Load(); since != nil {
		return *since, true
	}

	return time.Time{}, false
}

func (c *fakeShared) OnClose(f func()) {
	c.mu.Lock()

	if !c.ended {
		c.onClose = append(c.onClose, f)
		c.mu.Unlock()

		return
	}

	c.mu.Unlock()
	f()
}

// end closes c as the server would.
func (c *fakeShared) end() {
	c.mu.Lock()

	fs := c.onClose

	c.onClose, c.ended = nil, true
	c.mu.Unlock()

	for _, f := range fs {
		f()
	}
}

func (c *fakeShared) Close() error {
	c.t.Helper()
	checkUnlocked(c.t, c.pool, "Close")
	c.closed.Add(1)

	return nil
}

func (c *fakeShared) Expire() {
	c.t.Helper()
	checkUnlocked(c.t, c.pool, "Expire")
	c.expired.Add(1)
}

// fakeExclusive is an HTTP/1.1 connection.
type fakeExclusive struct {
	pool   *Pool[*fakeShared, *fakeExclusive]
	t      *testing.T
	usable atomic.Bool
	checks atomic.Int32
	closed chan struct{}
	// handedOff, when set, holds Usable until the test sends on it once
	// Release has returned: Release wakes the waiting Get it hands the
	// connection to with the pool's lock still held, and that Get's
	// goroutine checks the connection.
	handedOff chan struct{}
}

func (c *fakeExclusive) Usable() bool {
	if c.handedOff != nil {
		<-c.handedOff
	}

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
// connection does I/O. Tests that use it run the pool from one goroutine
// at a time, so a lock it finds held is the caller's: a Get that Release
// wakes checks only once Release has returned (fakeExclusive.handedOff).
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
	t.Helper()

	return newFixtureIdle(t, idleTimeout)
}

// newFixtureIdle is newFixture with the pool's IdleTimeout set to idle.
func newFixtureIdle(t *testing.T, idle time.Duration) *fixture {
	t.Helper()

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
		IdleTimeout: idle,
		KeyTimeout:  idleTimeout,
		OnWait: func(key string) {
			f.waiting <- key
		},
	})

	return f
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

// advance moves the clock by d and waits for a timer to have run.
func (f *fixture) advance(d time.Duration) {
	f.t.Helper()
	f.clock.Advance(d)

	select {
	case <-f.clock.fired:
	case <-f.t.Context().Done():
		f.t.Fatal("timer did not fire")
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

func TestIdleConnectionIsReapedAfterTheIdleTimeout(t *testing.T) {
	f := newFixture(t)
	c := f.dialExclusive("a")
	f.pool.Release("a", c, true)
	f.clock.Advance(idleTimeout - time.Second)

	if c.isClosed() {
		t.Fatal("closed before the idle timeout")
	}

	f.advance(time.Second)

	if !c.isClosed() {
		t.Fatal("not closed after the idle timeout")
	}

	f.get("a", FoundDial)
}

// Each idle connection is closed IdleTimeout after its last release, not
// at an earlier release's timeout, though one timer per key serves them
// all.
func TestIdleConnectionsAreReapedEachAtItsTimeout(t *testing.T) {
	f := newFixture(t)
	a := f.dialExclusive("b")
	b := f.dialExclusive("b")

	f.pool.Release("b", a, true)
	f.clock.Advance(idleTimeout / 2)
	f.pool.Release("b", b, true)
	// Both taken (b, the most recent, first) and released again.
	f.get("b", FoundExclusive)
	f.get("b", FoundExclusive)
	f.pool.Release("b", a, true)
	f.clock.Advance(idleTimeout / 2)
	f.pool.Release("b", b, true)
	f.clock.Advance(idleTimeout/2 - time.Second)

	if a.isClosed() || b.isClosed() {
		t.Fatal("closed before the idle timeout of its last release")
	}

	f.advance(time.Second)

	if !a.isClosed() || b.isClosed() {
		t.Fatalf("at a's timeout: a closed %v, b closed %v; want only a", a.isClosed(), b.isClosed())
	}

	f.clock.Advance(idleTimeout/2 - time.Second)

	if b.isClosed() {
		t.Fatal("b closed before its idle timeout")
	}

	f.advance(time.Second)

	if !b.isClosed() {
		t.Fatal("b not closed after its idle timeout")
	}
}

// heldConn is an exclusive connection that fails the test if it is
// closed while a request holds it.
type heldConn struct {
	t      *testing.T
	held   atomic.Bool
	closed atomic.Bool
}

func (c *heldConn) Usable() bool {
	return !c.closed.Load()
}

func (c *heldConn) Close() error {
	if c.held.Load() {
		c.t.Error("connection closed while a request holds it")
	}

	c.closed.Store(true)

	return nil
}

// The reap timer is re-armed as connections go idle and are taken, and
// may fire as a request takes the connection it was armed for: it must
// never close a connection a request holds.
func TestReapNeverClosesAHeldConnection(t *testing.T) {
	clock := clockwork.NewFakeClock()
	p := New[*fakeShared, *heldConn](Config{
		Clock:       clock,
		IdleTimeout: idleTimeout,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		for ctx.Err() == nil {
			clock.Advance(idleTimeout / 4)
			runtime.Gosched()
		}
	}()

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for range 500 {
				got, err := p.Get(ctx, "a")
				if err != nil {
					t.Error(err)

					return
				}

				c := got.Exclusive
				if got.Found == FoundDial {
					got.Dial.Exclusive()

					c = &heldConn{
						t: t,
					}
				}

				c.held.Store(true)
				runtime.Gosched()
				c.held.Store(false)
				p.Release("a", c, true)
			}
		})
	}

	wg.Wait()
}

// shared installs a new shared connection for key, idle since now.
func (f *fixture) shared(key string) *fakeShared {
	f.t.Helper()

	c := &fakeShared{
		pool: f.pool,
		t:    f.t,
	}
	c.canTake.Store(true)
	f.idle(c)

	if got := f.get(key, FoundDial).Dial.Shared(c); got != c {
		f.t.Fatalf("Shared installed %p, want %p", got, c)
	}

	return c
}

// idle marks c idle since now.
func (f *fixture) idle(c *fakeShared) {
	now := f.clock.Now()
	c.idleSince.Store(&now)
}

// A shared connection expires (never Close) once it has been idle for the
// idle timeout, counted from its last request's end, never while one is
// in flight; the pool forgets it then.
func TestSharedConnectionExpiresWhenIdle(t *testing.T) {
	f := newFixture(t)
	c := f.shared("a")

	f.clock.Advance(idleTimeout - time.Second)

	if c.expired.Load() != 0 {
		t.Fatal("expired before the idle timeout")
	}

	f.advance(time.Second)

	if c.expired.Load() != 1 || c.closed.Load() != 0 {
		t.Fatalf("after the idle timeout: expired %d times, closed %d, want 1 and 0", c.expired.Load(), c.closed.Load())
	}

	f.get("a", FoundDial)

	// A request in flight at the timeout: the pool looks again later, and
	// the timeout runs from the request's end.
	busy := f.shared("b")
	busy.idleSince.Store(nil)
	f.advance(idleTimeout)

	if busy.expired.Load() != 0 {
		t.Fatal("expired with a request in flight")
	}

	f.get("b", FoundShared)
	f.clock.Advance(30 * time.Second)
	f.idle(busy)
	f.advance(idleTimeout - 30*time.Second)

	if busy.expired.Load() != 0 {
		t.Fatal("expired before the idle timeout since its request ended")
	}

	f.advance(30 * time.Second)

	if busy.expired.Load() != 1 {
		t.Fatal("not expired at the idle timeout since its request ended")
	}
}

// ExpireKey closes a key's idle connections at once (Close for exclusive
// ones, Expire for the shared one) and reports the key still in use while
// its shared connection carries a request.
func TestExpireKey(t *testing.T) {
	f := newFixture(t)
	x := f.dialExclusive("a")
	f.pool.Release("a", x, true)

	s := f.shared("b")
	busy := f.shared("c")
	busy.idleSince.Store(nil)

	if inUse := f.pool.ExpireKey("a"); inUse || !x.isClosed() {
		t.Errorf("idle exclusive: still in use %t, closed %t; want false, true", inUse, x.isClosed())
	}

	if f.pool.ExpireKey("b") || s.expired.Load() != 1 || s.closed.Load() != 0 {
		t.Errorf("idle shared: expired %d, closed %d; want 1, 0 and not in use", s.expired.Load(), s.closed.Load())
	}

	f.get("b", FoundDial)

	if !f.pool.ExpireKey("c") || busy.expired.Load() != 0 {
		t.Errorf("shared with a request in flight: expired %d, want 0 and still in use", busy.expired.Load())
	}

	f.get("c", FoundShared)

	if f.pool.ExpireKey("unknown") {
		t.Error("a key never used is in use")
	}
}

// A shared connection leaves the pool once it closes, however that races
// with its install: closed before, during or after. The pool learns of it
// from the connection (OnClose), on no goroutine of its own.
func TestSharedConnectionLeavesThePoolWhenItCloses(t *testing.T) {
	f := newFixture(t)

	c := f.shared("a")
	c.end()

	if _, ok := f.pool.Shared("a"); ok {
		t.Fatal("a closed connection is still pooled")
	}

	f.get("a", FoundDial).Dial.Fail(context.Canceled)

	for i := range 200 {
		key := string(rune('b' + i))
		c := &fakeShared{
			pool: f.pool,
			t:    t,
		}
		c.canTake.Store(true)

		// Closed before it is installed, or as it is.
		if i%2 == 0 {
			c.end()
		}

		ended := make(chan struct{})

		go func() {
			c.end()
			close(ended)
		}()

		f.get(key, FoundDial).Dial.Shared(c)
		<-ended

		if _, ok := f.pool.Shared(key); ok {
			t.Fatalf("connection %d closed, still pooled", i)
		}
	}
}

// With a negative IdleTimeout no idle connection is closed for its
// idleness, exclusive or shared; CloseIdle still closes them.
func TestNegativeIdleTimeoutKeepsIdleConnections(t *testing.T) {
	f := newFixtureIdle(t, -1)

	e := f.dialExclusive("a")
	f.pool.Release("a", e, true)

	s := f.shared("b")

	f.clock.Advance(100 * idleTimeout)

	if e.isClosed() || s.expired.Load() != 0 || s.closed.Load() != 0 {
		t.Fatal("an idle connection closed with a negative IdleTimeout")
	}

	if got := f.get("a", FoundExclusive); got.Exclusive != e {
		t.Fatal("the idle HTTP/1.1 connection is not handed out again")
	}

	f.pool.Release("a", e, true)
	f.get("b", FoundShared)
	f.pool.CloseIdle()

	if !e.isClosed() || s.closed.Load() != 1 {
		t.Fatal("CloseIdle left an idle connection open")
	}
}
