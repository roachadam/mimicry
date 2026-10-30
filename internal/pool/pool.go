// Package pool keeps connections by key (scheme, host and port, partition,
// proxy): one shared HTTP/2 or HTTP/3 connection, idle HTTP/1.1 ones, the
// dial in progress and the requests waiting for a slot under
// Config.MaxExclusive. Idle connections, shared ones included, are closed
// after Config.IdleTimeout, unless it is negative.
//
// The pool never dials and never holds its lock across network I/O; see
// decisions 0020, 0023 and 0040.
package pool

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

// Shared is a connection that carries many requests at once (HTTP/2,
// HTTP/3).
type Shared interface {
	comparable
	// CanTakeNewRequest reports whether a new request may start on it.
	CanTakeNewRequest() bool
	// IdleSince reports when its last request ended, and false while it
	// has requests in flight.
	IdleSince() (time.Time, bool)
	// OnClose runs f once the connection has closed, for any reason: at
	// once, on the caller's goroutine, if it already has. f runs with none
	// of the connection's locks held; several may be registered.
	OnClose(f func())
	Close() error
	// Expire closes it, idle, as its browser lets go of a connection it
	// no longer needs: after IdleTimeout, or for ExpireKey.
	Expire()
}

// Exclusive is a connection that carries one request at a time (HTTP/1.1).
type Exclusive interface {
	comparable
	// Usable reports, without blocking, whether an idle connection can
	// carry another request: the peer has not closed it or sent anything.
	Usable() bool
	Close() error
}

// Config configures a Pool.
type Config struct {
	// Clock times idle connections and evictions.
	Clock clockwork.Clock
	// IdleTimeout is how long an idle connection is kept: an exclusive one
	// since its release, a shared one since its last request ended.
	// Negative: idle connections are never closed for their idleness.
	IdleTimeout time.Duration
	// KeyTimeout is how long a key with no connections keeps what the pool
	// learned about it (that its server speaks HTTP/1.1 only).
	KeyTimeout time.Duration
	// MaxExclusive caps a key's exclusive connections (in use, idle or
	// dialing) once its server is known to be HTTP/1.1-only: a browser's
	// per-host limit. Further requests wait in order. Zero: no limit.
	MaxExclusive int
	// OnWait, when set, is called with a key when a Get starts waiting. For
	// tests.
	OnWait func(key string)
}

// Pool keeps connections by key: at most one shared connection, idle
// exclusive ones, the dial in progress and the waiting requests. Closing
// and Usable run with the lock released. Safe for concurrent use.
type Pool[S Shared, E Exclusive] struct {
	cfg Config

	mu   sync.Mutex
	keys map[string]*entry[S, E]
}

type entry[S Shared, E Exclusive] struct {
	shared    S
	hasShared bool
	// idle connections, the most recently used last, so also in the order
	// their idle timeouts end.
	idle []idleConn[E]
	// reap closes idle connections whose timeout ended: armed for the
	// oldest's while there are any. One timer per entry, so that a
	// connection going idle allocates nothing.
	reap clockwork.Timer
	// busy counts exclusive connections handed out and dials in progress.
	busy int
	// dialing is set while a dial others wait for is in progress.
	dialing bool
	// exclusiveOnly is set once a dial for this key gave an exclusive
	// connection: the server speaks HTTP/1.1, so requests dial in parallel
	// (up to MaxExclusive) rather than wait for one another's dial.
	exclusiveOnly bool
	// waiters are the requests waiting, oldest first: for the dial in
	// progress, or for a connection once MaxExclusive are busy.
	waiters []*waiter[E]
	// evict forgets the entry once it has had no connections for
	// KeyTimeout; nil while it has some.
	evict clockwork.Timer
	// expire closes the shared connection once it has been idle for
	// IdleTimeout: armed while there is one, it looks again when the
	// connection's idle time could have reached it.
	expire clockwork.Timer
}

type idleConn[E Exclusive] struct {
	conn E
	// until is when it is closed unless a request takes it first.
	until time.Time
}

// waiter is a Get waiting its turn. What it is handed is set under the
// pool's lock, as it leaves the entry's waiters, before ready is closed.
type waiter[E Exclusive] struct {
	ready  chan struct{}
	handed handed
	// conn is the connection handed (handedConn).
	conn E
	// shares reports a dial others wait for (handedDial).
	shares bool
	// err is the error handed (handedErr).
	err error
}

// handed is what a waiter was handed.
type handed int

const (
	// handedRetry: look again, for the shared connection a dial gave.
	handedRetry handed = iota
	// handedConn: an exclusive connection another request released.
	handedConn
	// handedDial: a dial of its own.
	handedDial
	// handedErr: the error of the dial it waited for.
	handedErr
)

// New returns an empty pool.
func New[S Shared, E Exclusive](cfg Config) *Pool[S, E] {
	return &Pool[S, E]{
		cfg:  cfg,
		keys: map[string]*entry[S, E]{},
	}
}

// Found is what Get found for a request.
type Found int

// Values of Found.
const (
	// FoundShared is the key's shared connection, to send on.
	FoundShared Found = iota
	// FoundExclusive is an idle exclusive connection, now the caller's:
	// it hands it back with Release.
	FoundExclusive
	// FoundDial asks the caller to dial and report with the Dial's methods.
	FoundDial
)

// Got is a Get's result: Shared, Exclusive or Dial, as Found says.
type Got[S Shared, E Exclusive] struct {
	Found     Found
	Shared    S
	Exclusive E
	Dial      *Dial[S, E]
}

// Get finds a connection for key: the shared one if it can take the
// request, else the most recently used usable idle exclusive one, else a
// dial. Until the server is known to be HTTP/1.1-only, concurrent requests
// share one dial and its result, error included; after, they dial in
// parallel up to Config.MaxExclusive, then wait in order. Get returns ctx's
// error if ctx ends while waiting.
func (p *Pool[S, E]) Get(ctx context.Context, key string) (Got[S, E], error) {
	for {
		p.mu.Lock()

		e := p.entry(key)
		if e.hasShared {
			if e.shared.CanTakeNewRequest() {
				got := Got[S, E]{
					Found:  FoundShared,
					Shared: e.shared,
				}
				p.mu.Unlock()

				return got, nil
			}

			// Going away or dead: no new requests. It finishes its
			// streams and closes itself.
			p.dropShared(key, e)
		}

		if c, ok := e.popIdle(); ok {
			e.busy++
			p.mu.Unlock()

			if c.Usable() {
				return Got[S, E]{
					Found:     FoundExclusive,
					Exclusive: c,
				}, nil
			}

			p.Release(key, c, false)

			continue
		}

		if e.dialing || len(e.waiters) > 0 || p.full(e) {
			w := &waiter[E]{
				ready: make(chan struct{}),
			}

			e.waiters = append(e.waiters, w)
			p.mu.Unlock()

			if p.cfg.OnWait != nil {
				p.cfg.OnWait(key)
			}

			got, again, err := p.wait(ctx, key, w)
			if again {
				continue
			}

			return got, err
		}

		shares := !e.exclusiveOnly

		e.dialing = shares
		e.busy++
		p.mu.Unlock()

		return p.dial(ctx, key, shares), nil
	}
}

// full reports whether e has as many exclusive connections as it may.
// p.mu must be held.
func (p *Pool[S, E]) full(e *entry[S, E]) bool {
	return e.exclusiveOnly && p.cfg.MaxExclusive > 0 && e.busy >= p.cfg.MaxExclusive
}

// dial is a Got asking for a dial to key, which others wait for if shares.
func (p *Pool[S, E]) dial(ctx context.Context, key string, shares bool) Got[S, E] {
	return Got[S, E]{
		Found: FoundDial,
		Dial: &Dial[S, E]{
			pool:   p,
			key:    key,
			ctx:    ctx,
			shares: shares,
		},
	}
}

// wait waits for w's turn; again asks Get to look again. If ctx ends
// first, whatever w was handed goes to the next request.
func (p *Pool[S, E]) wait(ctx context.Context, key string, w *waiter[E]) (got Got[S, E], again bool, err error) {
	select {
	case <-w.ready:
	case <-ctx.Done():
		p.mu.Lock()

		if e := p.keys[key]; e != nil {
			if i := slices.Index(e.waiters, w); i >= 0 {
				e.waiters = slices.Delete(e.waiters, i, i+1)
				p.maybeEvict(key, e)
				p.mu.Unlock()

				return got, false, ctx.Err()
			}
		}

		p.mu.Unlock()

		// It was handed something as ctx ended (ready is closed).
		switch w.handed {
		case handedConn:
			p.Release(key, w.conn, true)
		case handedDial:
			p.dial(ctx, key, w.shares).Dial.Fail(ctx.Err())
		case handedRetry, handedErr:
			// Nothing to give back.
		}

		return got, false, ctx.Err()
	}

	switch w.handed {
	case handedConn:
		if w.conn.Usable() {
			return Got[S, E]{
				Found:     FoundExclusive,
				Exclusive: w.conn,
			}, false, nil
		}

		// Dead: dial in its place, keeping the turn.
		_ = w.conn.Close()

		return p.dial(ctx, key, false), false, nil

	case handedDial:
		return p.dial(ctx, key, w.shares), false, nil
	case handedErr:
		return got, false, w.err
	case handedRetry:
		// Look again (below).
	}

	return got, true, nil
}

// entry returns key's entry, creating it if needed, and stops its
// eviction. p.mu must be held.
func (p *Pool[S, E]) entry(key string) *entry[S, E] {
	e := p.keys[key]
	if e == nil {
		e = &entry[S, E]{}
		p.keys[key] = e
	}

	if e.evict != nil {
		e.evict.Stop()

		e.evict = nil
	}

	return e
}

// popIdle takes the most recently used idle connection, if any.
func (e *entry[S, E]) popIdle() (E, bool) {
	n := len(e.idle)
	if n == 0 {
		var zero E

		return zero, false
	}

	c := e.idle[n-1].conn

	e.idle[n-1] = idleConn[E]{}
	e.idle = e.idle[:n-1]

	if n == 1 {
		stop(e.reap)
	}

	return c, true
}

// nextWaiter takes the oldest waiter, or nil.
func (e *entry[S, E]) nextWaiter() *waiter[E] {
	if len(e.waiters) == 0 {
		return nil
	}

	w := e.waiters[0]

	e.waiters[0] = nil
	e.waiters = e.waiters[1:]

	return w
}

// wakeAll hands every waiter h, with err for handedErr.
func (e *entry[S, E]) wakeAll(h handed, err error) {
	for w := e.nextWaiter(); w != nil; w = e.nextWaiter() {
		w.handed, w.err = h, err
		close(w.ready)
	}
}

// serve hands the oldest waiters dials of their own while the key may
// dial. p.mu must be held.
func (p *Pool[S, E]) serve(e *entry[S, E]) {
	for len(e.waiters) > 0 && !e.dialing && !p.full(e) {
		w := e.nextWaiter()

		w.handed, w.shares = handedDial, !e.exclusiveOnly
		e.dialing = w.shares
		e.busy++

		close(w.ready)
	}
}

// Dial is a dial Get asked for. The caller reports its outcome once:
// Shared, Adopt, Exclusive or Fail.
type Dial[S Shared, E Exclusive] struct {
	pool *Pool[S, E]
	key  string
	ctx  context.Context
	// shares is set for a dial others wait for: the key's server is not
	// known to speak HTTP/1.1 only.
	shares bool
}

// MayShare reports whether other requests wait for this dial (the server
// is not known to be HTTP/1.1-only), so its connection may be shared or
// another key's adopted.
func (d *Dial[S, E]) MayShare() bool {
	return d.shares
}

// Shared makes c, which the dial produced, the key's shared connection and
// returns the connection to send on: c, or the shared connection another
// dial installed first, in which case c is closed.
func (d *Dial[S, E]) Shared(c S) S {
	got, installed := d.install(c)
	if !installed {
		_ = c.Close()
	}

	return got
}

// Adopt makes c, another key's shared connection, this key's too
// (coalescing) and returns the connection to send on: c, or one another
// dial installed first. c is never closed.
func (d *Dial[S, E]) Adopt(c S) S {
	got, _ := d.install(c)
	return got
}

// install makes c the key's shared connection unless another usable one
// is installed, which it returns; waiters look again. c leaves the pool
// once it closes.
func (d *Dial[S, E]) install(c S) (S, bool) {
	p := d.pool
	p.mu.Lock()

	e := p.entry(d.key)
	e.busy--

	e.exclusiveOnly = false
	d.finish(e)
	e.wakeAll(handedRetry, nil)

	if e.hasShared && e.shared.CanTakeNewRequest() {
		got := e.shared
		p.mu.Unlock()

		return got, false
	}

	e.shared, e.hasShared = c, true
	p.armExpire(d.key, e, p.cfg.IdleTimeout)
	p.mu.Unlock()

	// Registered unlocked: on a closed c it runs at once.
	key := d.key
	c.OnClose(func() {
		p.RemoveShared(key, c)
	})

	return c, true
}

// Exclusive records that the dial produced an exclusive connection, handed
// back with Release; the key's requests now dial in parallel.
func (d *Dial[S, E]) Exclusive() {
	p := d.pool
	p.mu.Lock()
	defer p.mu.Unlock()

	e := p.entry(d.key)

	e.exclusiveOnly = true
	d.finish(e)
	p.serve(e)
}

// Fail fails the dial's waiters with err, unless the dialing request's own
// context ended, in which case the oldest waiter dials again.
func (d *Dial[S, E]) Fail(err error) {
	p := d.pool
	p.mu.Lock()
	defer p.mu.Unlock()

	e := p.entry(d.key)
	e.busy--

	shared := d.shares
	d.finish(e)

	if shared && d.ctx.Err() == nil {
		e.wakeAll(handedErr, err)
	}

	p.serve(e)
	p.maybeEvict(d.key, e)
}

// finish ends the dial others wait for, if it is one. p.mu must be held.
func (d *Dial[S, E]) finish(e *entry[S, E]) {
	if d.shares {
		e.dialing = false
		d.shares = false
	}
}

// Release hands back an exclusive connection Get or a Dial gave: to the
// oldest waiting request, or idle for the next one, if reusable; else
// closed, and the oldest waiting request may dial in its place.
func (p *Pool[S, E]) Release(key string, c E, reusable bool) {
	p.mu.Lock()

	e := p.entry(key)
	if reusable {
		if w := e.nextWaiter(); w != nil {
			w.handed, w.conn = handedConn, c
			close(w.ready)
			p.mu.Unlock()

			return
		}

		e.busy--

		e.idle = append(e.idle, idleConn[E]{
			conn:  c,
			until: p.cfg.Clock.Now().Add(p.cfg.IdleTimeout),
		})

		if len(e.idle) == 1 {
			p.armReap(key, e, p.cfg.IdleTimeout)
		}

		p.mu.Unlock()

		return
	}

	e.busy--
	p.serve(e)
	p.maybeEvict(key, e)
	p.mu.Unlock()

	_ = c.Close()
}

// armReap schedules e's reap in d, for its oldest idle connection, unless
// idle connections are kept (a negative IdleTimeout). p.mu must be held.
func (p *Pool[S, E]) armReap(key string, e *entry[S, E], d time.Duration) {
	if p.cfg.IdleTimeout < 0 {
		return
	}

	if e.reap == nil {
		e.reap = p.cfg.Clock.AfterFunc(d, func() {
			p.reapIdle(key, e)
		})

		return
	}

	e.reap.Reset(d)
}

// reapIdle closes e's idle connections whose timeout ended and schedules
// the next. It finds nothing to do when it fired as a request took the
// connection it was armed for.
func (p *Pool[S, E]) reapIdle(key string, e *entry[S, E]) {
	p.mu.Lock()

	now := p.cfg.Clock.Now()

	n := 0
	for n < len(e.idle) && !now.Before(e.idle[n].until) {
		n++
	}

	if n == 0 {
		p.mu.Unlock()

		return
	}

	expired := make([]E, n)
	for i, ic := range e.idle[:n] {
		expired[i] = ic.conn
	}

	e.idle = slices.Delete(e.idle, 0, n)
	if len(e.idle) > 0 {
		p.armReap(key, e, e.idle[0].until.Sub(now))
	}

	p.maybeEvict(key, e)
	p.mu.Unlock()

	for _, c := range expired {
		_ = c.Close()
	}
}

// SharedConns returns the shared connections that can take a new request,
// each once, for a caller looking for one to adopt.
func (p *Pool[S, E]) SharedConns() []S {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []S
	for _, e := range p.keys {
		if e.hasShared && e.shared.CanTakeNewRequest() && !slices.Contains(out, e.shared) {
			out = append(out, e.shared)
		}
	}

	return out
}

// Peek reports what Get would find for key now (FoundShared,
// FoundExclusive or FoundDial) without taking, waiting, dialing or
// changing anything.
func (p *Pool[S, E]) Peek(key string) Found {
	p.mu.Lock()
	defer p.mu.Unlock()

	e := p.keys[key]
	switch {
	case e == nil:
		return FoundDial
	case e.hasShared && e.shared.CanTakeNewRequest():
		return FoundShared
	case len(e.idle) > 0 || e.exclusiveOnly:
		return FoundExclusive
	}

	return FoundDial
}

// Shared returns key's shared connection if it can take a new request,
// without waiting or dialing (for WebSockets, which otherwise dial
// outside the pool).
func (p *Pool[S, E]) Shared(key string) (S, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if e := p.keys[key]; e != nil && e.hasShared && e.shared.CanTakeNewRequest() {
		return e.shared, true
	}

	var zero S

	return zero, false
}

// RemoveShared unpools c if it is key's shared connection, without
// closing it: in-flight requests go on, and c closes itself after them.
func (p *Pool[S, E]) RemoveShared(key string, c S) {
	p.mu.Lock()
	defer p.mu.Unlock()

	e := p.keys[key]
	if e == nil || !e.hasShared || e.shared != c {
		return
	}

	p.dropShared(key, e)
}

// dropShared forgets e's shared connection. p.mu must be held.
func (p *Pool[S, E]) dropShared(key string, e *entry[S, E]) {
	var zero S

	e.shared, e.hasShared = zero, false
	stop(e.expire)
	p.maybeEvict(key, e)
}

// armExpire schedules e's look at its shared connection's idle time in d,
// unless idle connections are kept (a negative IdleTimeout). p.mu must be
// held.
func (p *Pool[S, E]) armExpire(key string, e *entry[S, E], d time.Duration) {
	if p.cfg.IdleTimeout < 0 {
		return
	}

	if e.expire == nil {
		e.expire = p.cfg.Clock.AfterFunc(d, func() {
			p.expireShared(key, e)
		})

		return
	}

	e.expire.Reset(d)
}

// expireShared expires e's shared connection if it has been idle for
// IdleTimeout, else looks again when it could have been: IdleTimeout
// after its last request ended, or IdleTimeout from now while it has
// requests in flight. A connection several keys share (coalescing) is
// idle only when no key's request is on it, whichever key looks.
func (p *Pool[S, E]) expireShared(key string, e *entry[S, E]) {
	p.mu.Lock()

	if p.keys[key] != e || !e.hasShared {
		p.mu.Unlock()

		return
	}

	c := e.shared

	since, idle := c.IdleSince()
	if left := since.Add(p.cfg.IdleTimeout).Sub(p.cfg.Clock.Now()); !idle || left > 0 {
		if !idle {
			left = p.cfg.IdleTimeout
		}

		p.armExpire(key, e, left)
		p.mu.Unlock()

		return
	}

	p.dropShared(key, e)
	p.mu.Unlock()

	c.Expire()
}

// ExpireKey closes key's idle connections now, as their idle timeout
// would (Close for exclusive ones, Expire for the shared one), and
// reports whether key still has connections or dials in use, which a
// later ExpireKey may close once idle.
func (p *Pool[S, E]) ExpireKey(key string) bool {
	p.mu.Lock()

	e := p.keys[key]
	if e == nil {
		p.mu.Unlock()

		return false
	}

	idle := make([]E, len(e.idle))
	for i, ic := range e.idle {
		idle[i] = ic.conn
	}

	if len(e.idle) > 0 {
		stop(e.reap)
	}

	e.idle = nil

	var (
		shared S
		expire bool
	)

	if e.hasShared {
		if _, expire = e.shared.IdleSince(); expire {
			shared = e.shared
			p.dropShared(key, e)
		}
	}

	p.maybeEvict(key, e)

	left := !e.empty()
	p.mu.Unlock()

	for _, c := range idle {
		_ = c.Close()
	}

	if expire {
		shared.Expire()
	}

	return left
}

// maybeEvict schedules e's removal if it has no connections or waiters
// left. p.mu must be held.
func (p *Pool[S, E]) maybeEvict(key string, e *entry[S, E]) {
	if !e.empty() || e.evict != nil {
		return
	}

	e.evict = p.cfg.Clock.AfterFunc(p.cfg.KeyTimeout, func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		if p.keys[key] == e && e.empty() {
			delete(p.keys, key)
		}
	})
}

// empty reports whether e has no connections, dials or waiters.
func (e *entry[S, E]) empty() bool {
	return !e.hasShared && len(e.idle) == 0 && e.busy == 0 && !e.dialing && len(e.waiters) == 0
}

// CloseIdle closes idle exclusive connections and shared ones with no
// requests in flight, each once.
func (p *Pool[S, E]) CloseIdle() {
	var (
		shared    []S
		exclusive []E
	)

	p.mu.Lock()

	for key, e := range p.keys {
		if len(e.idle) > 0 {
			stop(e.reap)
		}

		for _, ic := range e.idle {
			exclusive = append(exclusive, ic.conn)
		}

		e.idle = nil
		if e.hasShared {
			if _, idle := e.shared.IdleSince(); idle {
				if !slices.Contains(shared, e.shared) {
					shared = append(shared, e.shared)
				}

				p.dropShared(key, e)
			}
		}

		p.maybeEvict(key, e)
	}

	p.mu.Unlock()

	for _, c := range exclusive {
		_ = c.Close()
	}

	for _, c := range shared {
		_ = c.Close()
	}
}

// stop stops t, which is nil if it was never armed.
func stop(t clockwork.Timer) {
	if t != nil {
		t.Stop()
	}
}
