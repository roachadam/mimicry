// Package httpsrr looks up the DNS HTTPS records (RFC 9460) of the origins
// mimicry connects to, for the ECH configurations (RFC 9849) and ALPN they
// carry, as Chrome and Firefox do. net.Resolver cannot ask for type 65, so
// this is a small DNS client of its own: one query to the system's
// nameservers, through the caller's net.Resolver Dial when it has one, over
// UDP and again over TCP when the answer is truncated.
//
// Answers are cached by host and port: a record until the smallest TTL of
// the records it came from (CNAMEs included); no record (NXDOMAIN, NODATA)
// for the authority section's SOA negative TTL (RFC 2308), not at all
// without a SOA; a failed lookup for 30 s, so that a resolver dropping type
// 65 stalls one connection, not each; nothing for longer than a day.
// Concurrent lookups of one host and port share one query.
package httpsrr

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

const (
	// defaultTimeout bounds a lookup when Config.Timeout is zero.
	defaultTimeout = 5 * time.Second
	// failedTTL is how long a failed lookup is cached.
	failedTTL = 30 * time.Second
	// maxTTL caps every cached answer's lifetime.
	maxTTL = 24 * time.Hour
	// minSweep is the cache size below which expired entries are left
	// until their key is looked up again.
	minSweep = 64
)

// Config is what a Resolver needs.
type Config struct {
	// Resolver is the caller's resolver (mimicry.Config.Resolver); nil uses
	// the system's nameservers. When its Dial is set, every query goes
	// through Dial (network "udp" or "tcp", address the nameserver the
	// system config names), as Go's own resolver calls it.
	Resolver *net.Resolver
	// Clock times cache expiry; nil: the real clock.
	Clock clockwork.Clock
	// Timeout bounds one lookup (all nameservers and attempts); zero: 5 s.
	Timeout time.Duration
}

// Record is what mimicry uses of a host's HTTPS record. Its slices are
// shared by every caller given the same cached record: read-only.
type Record struct {
	// ECH is the ech SvcParam (an ECHConfigList, raw, not validated), nil
	// without one.
	ECH []byte
	// ALPN are the alpn SvcParam's protocols.
	ALPN []string
	// NoDefaultALPN is the no-default-alpn SvcParam's flag.
	NoDefaultALPN bool
}

// Resolver looks up and caches HTTPS records. It is safe for concurrent
// use.
type Resolver struct {
	resolver *net.Resolver
	clock    clockwork.Clock
	timeout  time.Duration
	// nameservers returns the addresses (host:port) to query, in order.
	nameservers func() []string

	mu      sync.Mutex
	cache   map[key]entry
	flights map[key]*flight
	// sweepAt is the cache size at which the next store drops expired
	// entries.
	sweepAt int
}

// key is a lookup's cache key: the host, lowercased and without a
// trailing dot, and the port.
type key struct {
	host string
	port int
}

// result is a lookup's outcome, as Lookup returns it.
type result struct {
	rec Record
	ok  bool
	err error
}

// entry is a cached result.
type entry struct {
	res     result
	expires time.Time
}

// flight is a query in progress; res is set when done is closed.
type flight struct {
	done chan struct{}
	res  result
}

// New returns a Resolver with an empty cache.
func New(cfg Config) *Resolver {
	clock := cfg.Clock
	if clock == nil {
		clock = clockwork.NewRealClock()
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	return &Resolver{
		resolver:    cfg.Resolver,
		clock:       clock,
		timeout:     timeout,
		nameservers: systemNameservers,
		cache:       map[key]entry{},
		flights:     map[key]*flight{},
		sweepAt:     minSweep,
	}
}

// Lookup returns the HTTPS record of host for an https:// origin on port:
// the RRset of host for port 443, of _<port>._https.<host> otherwise
// (RFC 9460, section 9.1). ok is false when there is none (NXDOMAIN,
// NODATA, or no usable ServiceMode record) and for an IP literal. An error
// is a failed lookup (timeout, SERVFAIL, REFUSED, malformed answer);
// callers proceed as without a record.
//
// Lookup returns when ctx is done, but the query it started or joined
// goes on (for at most Config.Timeout) for the others waiting for it and
// the cache.
func (r *Resolver) Lookup(ctx context.Context, host string, port int) (rec Record, ok bool, err error) {
	k := key{
		host: strings.ToLower(strings.TrimSuffix(host, ".")),
		port: port,
	}

	if _, err := netip.ParseAddr(k.host); err == nil {
		return Record{}, false, nil
	}

	now := r.clock.Now()

	r.mu.Lock()

	if e, hit := r.cache[k]; hit && now.Before(e.expires) {
		r.mu.Unlock()

		return e.res.rec, e.res.ok, e.res.err
	}

	f, shared := r.flights[k]
	if !shared {
		f = &flight{
			done: make(chan struct{}),
		}
		r.flights[k] = f

		go r.fly(context.WithoutCancel(ctx), k, f)
	}

	r.mu.Unlock()

	select {
	case <-f.done:
		return f.res.rec, f.res.ok, f.res.err
	case <-ctx.Done():
		return Record{}, false, ctx.Err()
	}
}

// fly runs f's query for k within the timeout, caches its result and
// hands it to f's waiters.
func (r *Resolver) fly(ctx context.Context, k key, f *flight) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	res, ttl := r.lookup(ctx, k)
	now := r.clock.Now()

	r.mu.Lock()
	delete(r.flights, k)
	r.store(now, k, res, min(ttl, maxTTL))
	r.mu.Unlock()

	f.res = res
	close(f.done)
}

// store caches res under k for ttl (nothing when ttl is not positive),
// first dropping expired entries when the cache has doubled since it last
// did. r.mu must be held.
func (r *Resolver) store(now time.Time, k key, res result, ttl time.Duration) {
	if ttl <= 0 {
		return
	}

	if len(r.cache) >= r.sweepAt {
		for old, e := range r.cache {
			if !now.Before(e.expires) {
				delete(r.cache, old)
			}
		}

		r.sweepAt = max(2*len(r.cache), minSweep)
	}

	r.cache[k] = entry{
		res:     res,
		expires: now.Add(ttl),
	}
}
