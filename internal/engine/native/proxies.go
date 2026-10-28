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
// thrashing proxies used in turn (decision 0040): a proxy keeps its idle
// connections while it is one of the keepProxies most recently used, or
// was used in the last proxyUnused.
const (
	// keepProxies is how many of the most recently used proxies keep
	// their idle connections however long ago they were used: round-robin
	// over that many never closes a connection it will use again.
	keepProxies = 16
	// proxyUnused is how long since its last request a proxy keeps its
	// idle connections when it is not among the keepProxies most recent:
	// round-robin over more proxies keeps them while each comes round
	// again within it.
	proxyUnused = 10 * time.Second
)

// proxyUse is when each proxy was last used and the pool keys its requests
// took. Safe for concurrent use.
type proxyUse struct {
	mu sync.Mutex
	// byURL is by proxy URL (route.via).
	byURL map[string]*proxyKeys
	// sweep runs sweepProxies; armed while more than keepProxies proxies
	// are known.
	sweep clockwork.Timer
	armed bool
}

// proxyKeys is a proxy's last use and the route keys of its requests (the
// HTTP/3 pool's are these with h3Key's suffix).
type proxyKeys struct {
	last time.Time
	keys map[string]struct{}
}

// usedProxy records a request on r through its proxy, if any, and arms the
// sweep once there are more proxies than keepProxies.
func (e *Engine) usedProxy(r route) {
	if r.proxy == nil {
		return
	}

	u := &e.proxies
	now := e.clock.Now()

	u.mu.Lock()
	defer u.mu.Unlock()

	pk := u.byURL[r.via]
	if pk == nil {
		pk = &proxyKeys{
			keys: map[string]struct{}{},
		}
		u.byURL[r.via] = pk
	}

	pk.last = now
	if _, ok := pk.keys[r.key]; !ok {
		pk.keys[r.key] = struct{}{}
	}

	if len(u.byURL) > keepProxies {
		e.armSweep(proxyUnused)
	}
}

// armSweep schedules sweepProxies in d unless it is scheduled already.
// e.proxies.mu must be held.
func (e *Engine) armSweep(d time.Duration) {
	u := &e.proxies
	if u.armed {
		return
	}

	u.armed = true
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
// among the keepProxies most recently used nor used in the last
// proxyUnused, and forgets it once it has none in use. It runs again when
// the next proxy goes stale, and within proxyUnused while a stale proxy's
// connections are still in use or more than keepProxies are known.
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

	for _, x := range uses[min(keepProxies, len(uses)):] {
		if left := x.pk.last.Add(proxyUnused).Sub(now); left > 0 {
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

	if inUse || len(u.byURL) > keepProxies {
		e.armSweep(next)
	}
}
