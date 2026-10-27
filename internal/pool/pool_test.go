package pool

import (
	"context"
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
