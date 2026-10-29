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
// used, or was used in the last proxyUnused. Config.MaxIdleProxies replaces
// that with a strict count: the N most recently used, no time.
const (
	// keepProxies is how many of the most recently used proxies keep
	// their idle connections however long ago they were used: round-robin
	// over that many never closes a connection it will use again.
	keepProxies = 16
	// proxyUnused is how long since its last request a proxy keeps its
	// idle connections when it is not among the keepProxies most recent:
	// round-robin over more proxies keeps them while each comes round
	// again within it. It is also how soon the sweep looks again at a
	// proxy whose connections were in use.
	proxyUnused = 10 * time.Second
)

// proxyUse is when each proxy was last used and the pool keys its requests
// took. Safe for concurrent use.
type proxyUse struct {
	// keep is how many of the most recently used proxies keep their idle
	// connections, and grace how long since its last use protects one
	// that is not among them: keepProxies and proxyUnused, or
	// Config.MaxIdleProxies and zero. Set once.
	keep  int
	grace time.Duration

	mu sync.Mutex
	// byURL is by proxy URL (route.via).
	byURL map[string]*proxyKeys
	// sweep runs sweepProxies at due; armed while more than keep proxies
	// are known.
	sweep clockwork.Timer
	armed bool
	due   time.Time
}

// proxyKeys is a proxy's last use and the route keys of its requests (the
// HTTP/3 pool's are these with h3Key's suffix).
type proxyKeys struct {
	last time.Time
	keys map[string]struct{}
}

// usedProxy records a request on r through its proxy, if any, and arms the
// sweep once there are more proxies than keep: grace after a proxy first
// appears (at once with a strict count, so that the one it pushes out of
// the most recent closes its idle connections now), else proxyUnused.
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

	pk.last = now
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
// use it saw.
type staleProxy struct {
	via  string
	last time.Time
	keys []string
}

// sweepProxies closes the idle connections of every proxy that is neither
// among the keep most recently used nor used in the last grace, and forgets
// it once it has none in use. It runs again when the next proxy goes
// stale, and within proxyUnused while a stale proxy's connections are
// still in use or more than keep are known.
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

	next := proxyUnused

	for _, x := range uses[min(u.keep, len(uses)):] {
		if left := x.pk.last.Add(u.grace).Sub(now); left > 0 {
			next = min(next, left)

			continue
		}

		keys := make([]string, 0, len(x.pk.keys))
		for k := range x.pk.keys {
			keys = append(keys, k)
		}

		stale = append(stale, staleProxy{
			via:  x.via,
			last: x.pk.last,
			keys: keys,
		})
	}

	u.mu.Unlock()

	inUse := false

	for _, s := range stale {
		left := false
		for _, k := range s.keys {
			left = e.pool.ExpireKey(k) || left
			if e.quic != nil {
				left = e.quic.pool.ExpireKey(h3PoolKey(k)) || left
			}
		}

		u.mu.Lock()

		if pk := u.byURL[s.via]; !left && pk != nil && pk.last.Equal(s.last) {
			delete(u.byURL, s.via)
		}

		u.mu.Unlock()

		inUse = inUse || left
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if inUse || len(u.byURL) > u.keep {
		e.armSweep(next)
	}
}
