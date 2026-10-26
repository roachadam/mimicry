package pool

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

// Shared is a connection that carries many requests at once (HTTP/2).
type Shared interface {
	comparable
	// CanTakeNewRequest reports whether a new request may start on it.
	CanTakeNewRequest() bool
	// Idle reports whether it has no requests in flight.
	Idle() bool
	// Done is closed once the connection is closed, for any reason.
	Done() <-chan struct{}
	Close() error
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
	// IdleTimeout is how long an idle exclusive connection is kept, and how
	// long a key with no connections keeps what the pool learned about it
	// (that its server speaks HTTP/1.1 only).
	IdleTimeout time.Duration
	// MaxExclusive is the most exclusive connections a key has at once
	// (in use, idle, or being dialed) once its server is known to speak
	// HTTP/1.1 only: a browser's per-host limit. More requests wait for
	// one, first come first served. Zero: no limit.
	MaxExclusive int
	// OnWait, when set, is called with a key when a Get starts waiting:
	// for another request's dial to it, or for one of its connections. For
	// tests.
	OnWait func(key string)
}

// Pool keeps connections by key: per key, at most one shared connection, the
// idle exclusive connections, the dial in progress, which concurrent
// requests share, and the requests waiting their turn. It never holds its
// lock across network I/O: closing connections and Usable run with it
// released. Safe for concurrent use.
type Pool[S Shared, E Exclusive] struct {
	cfg Config

	mu   sync.Mutex
	keys map[string]*entry[S, E]
}

type entry[S Shared, E Exclusive] struct {
	shared    S
	hasShared bool
	// idle connections, the most recently used last.
	idle []*idleConn[E]
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
	// IdleTimeout; nil while it has some.
	evict clockwork.Timer
}

type idleConn[E Exclusive] struct {
	conn  E
	reap  clockwork.Timer
	taken bool
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

// Get finds a connection for a request to key: the shared connection if it
// can take the request, else the most recently used idle exclusive
// connection that is still usable, else a dial. Requests to a key whose
// server is not known to speak HTTP/1.1 only wait for a dial in progress
// and share its result: its connection, or its error. Once it is known to,
// requests dial in parallel up to Config.MaxExclusive connections, then
// wait, in the order they came, for a connection another request releases
// or a dial one gives up. Get returns ctx's error if ctx ends while it
// waits.
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
		if ic := e.popIdle(); ic != nil {
			e.busy++
			p.mu.Unlock()
			if ic.conn.Usable() {
				return Got[S, E]{
					Found:     FoundExclusive,
					Exclusive: ic.conn,
				}, nil
			}
			p.Release(key, ic.conn, false)
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

// wait waits for w's turn and returns what it was handed; again asks Get
// to look again. If ctx ends first, what w was handed meanwhile goes to
// the next request.
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

// popIdle takes the most recently used idle connection, or nil.
func (e *entry[S, E]) popIdle() *idleConn[E] {
	n := len(e.idle)
	if n == 0 {
		return nil
	}
	ic := e.idle[n-1]
	e.idle[n-1] = nil
	e.idle = e.idle[:n-1]
	ic.taken = true
	ic.reap.Stop()
	return ic
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
// dial: no dial others wait for in progress (the first waiter's becomes
// one if the server is not known to speak HTTP/1.1 only), and fewer than
// MaxExclusive connections. p.mu must be held.
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

// MayShare reports whether the dial is one other requests to the key wait
// for, because its server is not known to speak HTTP/1.1 only: its
// connection may be shared, or the request may take another key's (Adopt).
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

// Adopt makes c, another key's shared connection, this key's too, instead
// of dialing (connection coalescing), and returns the connection to send
// on: c, or the shared connection another dial installed first. c is
// never closed.
func (d *Dial[S, E]) Adopt(c S) S {
	got, _ := d.install(c)
	return got
}

// install makes c the key's shared connection, unless another dial
// installed one that can still take requests: then it returns that one.
// The requests waiting look again.
func (d *Dial[S, E]) install(c S) (S, bool) {
	p := d.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(d.key)
	e.busy--
	e.exclusiveOnly = false
	d.finish(e)
	e.wakeAll(handedRetry, nil)
	if e.hasShared && e.shared.CanTakeNewRequest() {
		return e.shared, false
	}
	e.shared, e.hasShared = c, true
	go p.watch(d.key, c)
	return c, true
}

// Exclusive records that the dial produced an exclusive connection, which
// the caller holds and hands back with Release: from now on, requests to
// the key dial in parallel, up to MaxExclusive.
func (d *Dial[S, E]) Exclusive() {
	p := d.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(d.key)
	e.exclusiveOnly = true
	d.finish(e)
	p.serve(e)
}

// Fail records that the dial failed with err: the requests waiting for it
// fail with err too, unless the dialing request's own context ended, in
// which case the oldest dials again. A dial that others did not wait for
// gives its turn to the oldest waiting request.
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
		ic := &idleConn[E]{
			conn: c,
		}
		ic.reap = p.cfg.Clock.AfterFunc(p.cfg.IdleTimeout, func() {
			p.reap(key, ic)
		})
		e.idle = append(e.idle, ic)
		p.mu.Unlock()
		return
	}
	e.busy--
	p.serve(e)
	p.maybeEvict(key, e)
	p.mu.Unlock()
	_ = c.Close()
}

// reap closes ic once it has idled for IdleTimeout, unless a request took
// it first.
func (p *Pool[S, E]) reap(key string, ic *idleConn[E]) {
	p.mu.Lock()
	e := p.keys[key]
	if e == nil || ic.taken {
		p.mu.Unlock()
		return
	}
	for i, other := range e.idle {
		if other == ic {
			e.idle = append(e.idle[:i], e.idle[i+1:]...)
			break
		}
	}
	ic.taken = true
	p.maybeEvict(key, e)
	p.mu.Unlock()
	_ = ic.conn.Close()
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

// Peek reports what Get would find for key now, without taking it,
// waiting or dialling: FoundShared for a shared connection that can take
// a new request, FoundExclusive for an idle exclusive connection or a key
// whose server is known to give only exclusive ones (it waits for one
// then, or dials another), FoundDial otherwise. It changes nothing.
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

// Shared returns key's shared connection if it has one that can take a
// new request, without waiting or dialing: for a caller that opens a
// connection of its own otherwise, outside the pool (a WebSocket's).
func (p *Pool[S, E]) Shared(key string) (S, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.keys[key]; e != nil && e.hasShared && e.shared.CanTakeNewRequest() {
		return e.shared, true
	}
	var zero S
	return zero, false
}

// RemoveShared takes c out of the pool, if it is key's shared connection,
// without closing it: requests in flight on it go on, and it closes itself
// once they end (a server's GOAWAY) or it fails.
func (p *Pool[S, E]) RemoveShared(key string, c S) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.keys[key]
	if e == nil || !e.hasShared || e.shared != c {
		return
	}
	p.dropShared(key, e)
}

// watch removes c from the pool once it closes.
func (p *Pool[S, E]) watch(key string, c S) {
	<-c.Done()
	p.RemoveShared(key, c)
}

// dropShared forgets e's shared connection. p.mu must be held.
func (p *Pool[S, E]) dropShared(key string, e *entry[S, E]) {
	var zero S
	e.shared, e.hasShared = zero, false
	p.maybeEvict(key, e)
}

// maybeEvict schedules e's removal if it has no connections or waiters
// left. p.mu must be held.
func (p *Pool[S, E]) maybeEvict(key string, e *entry[S, E]) {
	if !e.empty() || e.evict != nil {
		return
	}
	e.evict = p.cfg.Clock.AfterFunc(p.cfg.IdleTimeout, func() {
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

// CloseIdle closes the idle exclusive connections and the shared ones with
// no requests in flight, each once (an adopted connection is several
// keys').
func (p *Pool[S, E]) CloseIdle() {
	var shared []S
	var exclusive []E
	p.mu.Lock()
	for key, e := range p.keys {
		for _, ic := range e.idle {
			ic.taken = true
			ic.reap.Stop()
			exclusive = append(exclusive, ic.conn)
		}
		e.idle = nil
		if e.hasShared && e.shared.Idle() {
			if !slices.Contains(shared, e.shared) {
				shared = append(shared, e.shared)
			}
			var zero S
			e.shared, e.hasShared = zero, false
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
