package native

import (
	"slices"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

// A caller that rotates proxies changes what Config.Proxy returns, and each
// proxy URL has connections of its own (decision 0014). Those of a proxy
// the engine no longer uses are closed before their idle timeout, without
// thrashing proxies used in turn (decision 0040): by default a proxy keeps
// its idle connections while it is one of the keepProxies most recently
// used, or was used in the last proxyUnused. What the engine learned
// through a proxy (TLS and QUIC tickets, HTTP/3 origin state, its 407 or
// refused UDP relay: forgetProxy) costs little memory and saves a returning
// proxy a full handshake, so it outlives the connections: it is forgotten
// once the proxy is not among the rememberProxies most recently used, nor
// used in the last proxyUnused. Config.MaxIdleProxies replaces both with
// one strict count: the N most recently used keep their connections and
// their state, no time.
const (
	// keepProxies is how many of the most recently used proxies keep
	// their idle connections however long ago they were used: round-robin
	// over that many never closes a connection it will use again.
	keepProxies = 16
	// rememberProxies is how many of the most recently used proxies keep
	// what the engine learned through them however long ago they were
	// used (decision 0040's 2026-10-30 amendment).
	rememberProxies = 1024
	// proxyUnused is how long since its last request a proxy keeps its
	// idle connections, and its state, when it is not among the
	// keepProxies (rememberProxies) most recent: round-robin over more
	// proxies keeps them while each comes round again within it. It is
	// also how soon the sweep looks again at a proxy whose connections
	// were in use.
	proxyUnused = 10 * time.Second
)

// proxyUse is when each proxy was last used and the pool keys its requests
// took. Safe for concurrent use.
type proxyUse struct {
	// keep is how many of the most recently used proxies keep their idle
	// connections, remember how many keep their state, and grace how long
	// since its last use protects one that is not among them:
	// keepProxies, rememberProxies and proxyUnused, or
	// Config.MaxIdleProxies twice and zero. Set once.
	keep     int
	remember int
	grace    time.Duration

	mu sync.Mutex
	// byURL is by proxy URL (route.via).
	byURL map[string]*proxyKeys
	// sweep runs sweepProxies at due; armed while more than keep proxies
	// are known.
	sweep clockwork.Timer
	armed bool
	due   time.Time
}

// proxyKeys is a proxy's last use, the route keys of its requests (the
// HTTP/3 pool's are these with h3Key's suffix) and how many of them have
// not returned yet (usedProxy to doneProxy): one may still dial. closed:
// the sweep has closed its connections and keeps it for its state.
type proxyKeys struct {
	last     time.Time
	keys     map[string]struct{}
	inFlight int
	closed   bool
}

// usedProxy records a request on r through its proxy, if any, until
// doneProxy, and arms the sweep once there are more proxies than keep:
// grace after a proxy first appears (at once with a strict count, so that
// the one it pushes out of the most recent closes its idle connections
// now), else proxyUnused.
func (e *Engine) usedProxy(r route) {
	if r.proxy == nil {
		return
	}

	u := &e.proxies
	now := e.clock.Now()

	u.mu.Lock()
	defer u.mu.Unlock()

	pk := u.byURL[r.via]
	added := pk == nil

	if added {
		pk = &proxyKeys{
			keys: map[string]struct{}{},
		}
		u.byURL[r.via] = pk
	}

	pk.last, pk.closed = now, false
	pk.inFlight++

	if _, ok := pk.keys[r.key]; !ok {
		pk.keys[r.key] = struct{}{}
	}

	if len(u.byURL) > u.keep {
		d := proxyUnused
		if added {
			d = u.grace
		}

		e.armSweep(d)
	}
}

// doneProxy ends the request usedProxy recorded on r.
func (e *Engine) doneProxy(r route) {
	if r.proxy == nil {
		return
	}

	u := &e.proxies

	u.mu.Lock()
	defer u.mu.Unlock()

	// Never nil: a proxy with a request in flight is not forgotten.
	u.byURL[r.via].inFlight--
}

// armSweep schedules sweepProxies in d unless it is scheduled already for
// no later. e.proxies.mu must be held.
func (e *Engine) armSweep(d time.Duration) {
	u := &e.proxies

	due := e.clock.Now().Add(d)
	if u.armed && !due.Before(u.due) {
		return
	}

	u.armed = true
	u.due = due

	if u.sweep == nil {
		u.sweep = e.clock.AfterFunc(d, e.sweepProxies)

		return
	}

	u.sweep.Reset(d)
}

// staleProxy is a proxy sweepProxies expires: its URL, keys and the last
// use it saw, and whether it is forgotten (forget) or only loses its
// connections.
type staleProxy struct {
	via    string
	last   time.Time
	keys   []string
	forget bool
}

// sweepProxies closes the idle connections of every proxy that is neither
// among the keep most recently used nor used in the last grace, and
// forgets one not among the remember most recent either, with all it
// taught the engine (forgetProxy), once it has no connection in use, no
// request in flight and no QUIC attempt running. It runs again when the
// next proxy goes stale, and within proxyUnused while a stale proxy is
// still in use.
func (e *Engine) sweepProxies() {
	u := &e.proxies
	now := e.clock.Now()

	u.mu.Lock()

	u.armed = false

	type use struct {
		via string
		pk  *proxyKeys
	}

	uses := make([]use, 0, len(u.byURL))
	for via, pk := range u.byURL {
		uses = append(uses, use{
			via: via,
			pk:  pk,
		})
	}

	slices.SortFunc(uses, func(a, b use) int {
		return b.pk.last.Compare(a.pk.last)
	})

	var stale []staleProxy

	next, waiting := proxyUnused, false

	for i := min(u.keep, len(uses)); i < len(uses); i++ {
		x := uses[i]
		if left := x.pk.last.Add(u.grace).Sub(now); left > 0 {
			next, waiting = min(next, left), true

			continue
		}

		forget := i >= u.remember
		if x.pk.closed && !forget {
			continue
		}

		keys := make([]string, 0, len(x.pk.keys))
		for k := range x.pk.keys {
			keys = append(keys, k)
		}

		stale = append(stale, staleProxy{
			via:    x.via,
			last:   x.pk.last,
			keys:   keys,
			forget: forget,
		})
	}

	u.mu.Unlock()

	inUse := false

	for _, s := range stale {
		left := false
		for _, k := range s.keys {
			left = e.pool.ExpireKey(k) || left
			if e.quic != nil {
				// The attempt first: runAttempt pools its connection
				// before it ends, so one ended by now has its connection
				// in the pool for ExpireKey to see. The other way round,
				// one ending between the two would be missed.
				dialing := e.h3Dialing(k)

				left = e.quic.pool.ExpireKey(h3PoolKey(k)) || dialing || left
			}
		}

		u.mu.Lock()

		// One used since the sweep looked is no longer stale.
		if pk := u.byURL[s.via]; pk != nil && pk.last.Equal(s.last) {
			left = left || pk.inFlight > 0

			switch {
			case left:
			case s.forget:
				delete(u.byURL, s.via)
				// Under u.mu: a request through it again starts
				// afresh, after this.
				e.forgetProxy(s)

			default:
				pk.closed = true
			}
		}

		u.mu.Unlock()

		inUse = inUse || left
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if inUse || waiting {
		e.armSweep(next)
	}
}

// forgetProxy drops what the engine learned through the swept proxy s: its
// TLS and QUIC session tickets, its origins' HTTP/3 state and what the
// dialer knows of it (a 407, a refused UDP association). A request through
// it later starts as through a new proxy: full handshakes, and the
// browser's wait for the 407 again (Config.BrowserProxyAuth). Advertised
// alternatives are not the proxy's (route.alt) and stay. e.proxies.mu must
// be held.
func (e *Engine) forgetProxy(s staleProxy) {
	e.dialer.Forget(s.via)

	if e.tickets != nil {
		for _, k := range s.keys {
			e.tickets.Drop(k)
		}
	}

	if e.quic == nil {
		return
	}

	q := e.quic

	q.mu.Lock()
	defer q.mu.Unlock()

	for _, k := range s.keys {
		q.tickets.Drop(h3PoolKey(k))
		delete(q.origins, k)
	}
}
