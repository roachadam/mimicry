package pool

import (
	"context"
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
	// OnWait, when set, is called with a key when a Get starts waiting for
	// another request's dial to it. For tests.
	OnWait func(key string)
}

// Pool keeps connections by key: per key, at most one shared connection, the
// idle exclusive connections, and the dial in progress, which concurrent
// requests share. It never holds its lock across network I/O: closing
// connections and Usable run with it released. Safe for concurrent use.
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
	// dial is the dial in progress that others wait for; nil if none.
	dial *dialCall
	// exclusiveOnly is set once a dial for this key gave an exclusive
	// connection: the server speaks HTTP/1.1, so requests dial in parallel
	// rather than wait for one another's dial.
	exclusiveOnly bool
	// evict forgets the entry once it has had no connections for
	// IdleTimeout; nil while it has some.
	evict clockwork.Timer
}

type idleConn[E Exclusive] struct {
	conn  E
	reap  clockwork.Timer
	taken bool
}

// dialCall is a dial other requests wait for.
type dialCall struct {
	done chan struct{}
	// err is the dial's error, given to the waiters; nil if it succeeded or
	// if the dialing request was canceled (the waiters then try again).
	err error
}

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
// and share its result: its connection, or its error. Get returns ctx's
// error if ctx ends while it waits.
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
		if call := e.dial; call != nil {
			p.mu.Unlock()
			if p.cfg.OnWait != nil {
				p.cfg.OnWait(key)
			}
			select {
			case <-call.done:
			case <-ctx.Done():
				return Got[S, E]{}, ctx.Err()
			}
			if call.err != nil {
				return Got[S, E]{}, call.err
			}
			continue
		}
		d := &Dial[S, E]{
			pool: p,
			key:  key,
			ctx:  ctx,
		}
		if !e.exclusiveOnly {
			d.call = &dialCall{
				done: make(chan struct{}),
			}
			e.dial = d.call
		}
		e.busy++
		p.mu.Unlock()
		return Got[S, E]{
			Found: FoundDial,
			Dial:  d,
		}, nil
	}
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

// Dial is a dial Get asked for. The caller reports its outcome once:
// Shared, Exclusive or Fail.
type Dial[S Shared, E Exclusive] struct {
	pool *Pool[S, E]
	key  string
	ctx  context.Context
	// call is the dial others wait for; nil for a key that dials in
	// parallel.
	call *dialCall
}

// Shared makes c, which the dial produced, the key's shared connection and
// returns the connection to send on: c, or the shared connection another
// dial installed first, in which case c is closed.
func (d *Dial[S, E]) Shared(c S) S {
	p := d.pool
	p.mu.Lock()
	e := p.entry(d.key)
	e.busy--
	e.exclusiveOnly = false
	d.finish(e, nil)
	if e.hasShared && e.shared.CanTakeNewRequest() {
		existing := e.shared
		p.mu.Unlock()
		_ = c.Close()
		return existing
	}
	e.shared, e.hasShared = c, true
	p.mu.Unlock()
	go p.watch(d.key, c)
	return c
}

// Exclusive records that the dial produced an exclusive connection, which
// the caller holds and hands back with Release: from now on, requests to
// the key dial in parallel.
func (d *Dial[S, E]) Exclusive() {
	p := d.pool
	p.mu.Lock()
	e := p.entry(d.key)
	e.exclusiveOnly = true
	d.finish(e, nil)
	p.mu.Unlock()
}

// Fail records that the dial failed with err: the requests waiting for it
// fail with err too, unless the dialing request's own context ended, in
// which case they dial again themselves.
func (d *Dial[S, E]) Fail(err error) {
	p := d.pool
	p.mu.Lock()
	e := p.entry(d.key)
	e.busy--
	if d.ctx.Err() != nil {
		err = nil
	}
	d.finish(e, err)
	p.maybeEvict(d.key, e)
	p.mu.Unlock()
}

// finish wakes the dial's waiters. p.mu must be held.
func (d *Dial[S, E]) finish(e *entry[S, E], err error) {
	if d.call == nil {
		return
	}
	if e.dial == d.call {
		e.dial = nil
	}
	d.call.err = err
	close(d.call.done)
	d.call = nil
}

// Release hands back an exclusive connection Get or a Dial gave: idle for
// the next request if reusable, else closed.
func (p *Pool[S, E]) Release(key string, c E, reusable bool) {
	p.mu.Lock()
	e := p.entry(key)
	e.busy--
	if reusable {
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

// maybeEvict schedules e's removal if it has no connections left. p.mu
// must be held.
func (p *Pool[S, E]) maybeEvict(key string, e *entry[S, E]) {
	if e.hasShared || len(e.idle) > 0 || e.busy > 0 || e.dial != nil || e.evict != nil {
		return
	}
	e.evict = p.cfg.Clock.AfterFunc(p.cfg.IdleTimeout, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.keys[key] == e && !e.hasShared && len(e.idle) == 0 && e.busy == 0 && e.dial == nil {
			delete(p.keys, key)
		}
	})
}

// CloseIdle closes the idle exclusive connections and the shared ones with
// no requests in flight.
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
			shared = append(shared, e.shared)
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
