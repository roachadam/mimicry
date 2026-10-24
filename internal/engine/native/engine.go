package native

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/dial"
	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/h1"
	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/profile"
)

// idleTimeout is how long an idle HTTP/1.1 connection is kept.
const idleTimeout = 90 * time.Second

// defaultDialTimeout applies when Config.DialTimeout is zero.
const defaultDialTimeout = 30 * time.Second

// maxAttempts bounds how many connections one request tries.
const maxAttempts = 5

// Config configures the engine.
type Config struct {
	// Profile is the browser to imitate. New takes ownership: it must be
	// valid (profile.Parse or Validate) and never changed afterwards.
	Profile *profile.Profile
	RootCAs *x509.CertPool
	// DialTimeout bounds the connect (through a proxy: to the proxy, and the
	// proxy's handshake) and, separately, the TLS handshake, on top of the
	// request context's deadline. Zero means 30 seconds.
	DialTimeout time.Duration
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// Resolver looks host names up: for dials without DialContext, and to
	// find another host's HTTP/2 connection a request may take
	// (profile.CoalesceOnLookup). Nil uses net.DefaultResolver.
	Resolver *net.Resolver
	// Proxy picks each request's proxy, as http.Transport's does; nil, or
	// a nil URL, connects directly.
	Proxy func(*http.Request) (*url.URL, error)
}

// Engine is the pure-Go engine: pooled connections, one dial path, uTLS
// handshakes from the profile's plan, and HTTP/2 or HTTP/1.1 by ALPN.
// It is safe for concurrent use.
type Engine struct {
	plan    *fingerprint.Plan
	profile *profile.Profile
	clock   clockwork.Clock
	rootCAs *x509.CertPool
	dialer  *dial.Dialer
	proxy   func(*http.Request) (*url.URL, error)
	// lookup returns a host's addresses, for coalescing on lookup.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	// tickets keeps TLS session tickets when the profile resumes.
	tickets *handshake.Tickets
	pool    *pool.Pool[*shared, *h1.Conn]
}

// New builds an engine for cfg.Profile, which it takes ownership of.
func New(cfg Config) (*Engine, error) {
	return newEngine(cfg, clockwork.NewRealClock(), nil)
}

// newEngine is New with the clock and a hook for tests (pool.Config.OnWait).
func newEngine(cfg Config, clock clockwork.Clock, onWait func(string)) (*Engine, error) {
	p := cfg.Profile
	if p == nil {
		return nil, errors.New("mimicry: Config.Profile is required")
	}
	if p.Connections == nil {
		return nil, errors.New("mimicry: the profile has no connections section")
	}
	timeout := cfg.DialTimeout
	if timeout == 0 {
		timeout = defaultDialTimeout
	}
	plan := fingerprint.Compile(p)
	e := &Engine{
		plan:    plan,
		profile: p,
		clock:   clock,
		rootCAs: cfg.RootCAs,
		dialer: &dial.Dialer{
			Timeout:     timeout,
			DialContext: cfg.DialContext,
			Resolver:    cfg.Resolver,
			Proxy:       p.Proxy,
			UserAgent:   userAgent(p),
		},
		proxy:  cfg.Proxy,
		lookup: lookupFunc(cfg.Resolver),
		pool: pool.New[*shared, *h1.Conn](pool.Config{
			Clock:        clock,
			IdleTimeout:  idleTimeout,
			MaxExclusive: p.Connections.HTTP1PerHost,
			OnWait:       onWait,
		}),
	}
	if plan.Resumes() {
		e.tickets = handshake.NewTickets(p.TLS.Resumption.TLS12, clock)
	}
	return e, nil
}

// Errors of an attempt that may be tried again on another connection.
var (
	// errNotSent: nothing of the request was sent, so it goes again as it
	// is, whatever its method and body.
	errNotSent = errors.New("request not sent")
	// errRetry: the request may have been sent in part on a connection the
	// server had closed; it goes again only with a fresh body.
	errRetry = errors.New("request failed on a reused connection")
)

// RoundTrip sends req on a pooled or new connection. It closes req.Body,
// on errors too.
func (e *Engine) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	// Every copy of req sent below keeps its context.
	ctx := req.Context()
	defer func() {
		// req is the request last sent, or about to be: its body is the
		// one not closed yet.
		if err != nil && req.Body != nil {
			_ = req.Body.Close()
		}
	}()
	if req.URL == nil || req.URL.Host == "" {
		return nil, errors.New("mimicry: request has no host")
	}
	if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
		return nil, fmt.Errorf("mimicry: unsupported scheme %q", req.URL.Scheme)
	}
	if !httpguts.ValidHostHeader(req.URL.Host) {
		return nil, fmt.Errorf("mimicry: invalid host %q", req.URL.Host)
	}
	r, err := e.route(req)
	if err != nil {
		return nil, err
	}
	unasked := ""
	if r.forward {
		if unasked = e.dialer.ForwardAuthorization(r.proxy); unasked != "" {
			req = withProxyAuth(ctx, req, unasked)
		}
	}
	answered := false
	for attempt := 1; ; attempt++ {
		resp, err := e.try(ctx, req, r)
		if err == nil && r.forward && !answered && unasked == "" && resp.StatusCode == http.StatusProxyAuthRequired && e.dialer.Challenged(r.proxy, resp.Header) {
			// As browsers do, answer the proxy's challenge once, on the
			// same connection: read to the end, it goes back to the pool
			// for the resent request to take.
			answered = true
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			if req, err = fresh(req); err != nil {
				return nil, err
			}
			req = withProxyAuth(context.WithValue(ctx, h1.ChallengeAnswered{}, true), req, dial.Credentials(r.proxy))
			continue
		}
		notSent := errors.Is(err, errNotSent)
		if !notSent && !errors.Is(err, errRetry) {
			return resp, err
		}
		if attempt == maxAttempts {
			return nil, fmt.Errorf("mimicry: no usable connection after %d attempts: %w", attempt, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if notSent {
			continue
		}
		// The failed attempt may have consumed the body: send a fresh copy.
		next, err := fresh(req)
		if err != nil {
			return nil, err
		}
		req = next
	}
}

// fresh returns req ready to send again: with a new copy of its body, on a
// clone so the caller's request is not modified.
func fresh(req *http.Request) (*http.Request, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, nil
	}
	if req.GetBody == nil {
		return nil, errors.New("mimicry: the request must be sent again and its body cannot be resent")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.Body = body
	return req, nil
}

// withProxyAuth returns a clone of req, on ctx, carrying
// Proxy-Authorization, which the HTTP/1.1 writer places as the profile
// says.
func withProxyAuth(ctx context.Context, req *http.Request, auth string) *http.Request {
	req = req.Clone(ctx)
	req.Header.Set("Proxy-Authorization", auth)
	return req
}

// route is how a request reaches its server: the connection pool it
// belongs to, and the proxy it goes through.
type route struct {
	// key is the pool: scheme, host and port, partition, and proxy.
	key string
	// addr is the server's host and port; host is the host alone.
	addr string
	host string
	// group is what an HTTP/2 connection to another host must share for
	// the request to go on it (coalescing): scheme, port and partition.
	// Empty for requests that never take another host's connection:
	// plain HTTP, or through a proxy.
	group string
	// proxy is nil for a direct connection.
	proxy *url.URL
	// forward sends plain-HTTP requests to an HTTP proxy whole, instead of
	// through a tunnel.
	forward bool
}

// route picks req's proxy and pool. Connections and TLS sessions are
// never shared between proxies, or with direct connections.
func (e *Engine) route(req *http.Request) (route, error) {
	addr := hostPort(req.URL)
	r := route{
		key:  req.URL.Scheme + "://" + addr,
		addr: addr,
		host: req.URL.Hostname(),
	}
	partition := pool.Partition(req.Context())
	if partition != "" {
		r.key += " " + partition
	}
	if req.URL.Scheme == "https" {
		_, port, _ := net.SplitHostPort(addr)
		r.group = "https :" + port + " " + partition
	}
	if e.proxy == nil {
		return r, nil
	}
	p, err := e.proxy(req)
	if err != nil || p == nil {
		return r, err
	}
	if err := e.dialer.CheckProxy(p); err != nil {
		return r, err
	}
	r.proxy = p
	r.forward = p.Scheme == dial.SchemeHTTP && req.URL.Scheme == "http"
	r.key += " via " + p.String()
	// Coalescing through a proxy was never captured; the proxy resolves
	// names, so the address condition cannot be checked either.
	r.group = ""
	return r, nil
}

// userAgent is the profile's User-Agent value.
func userAgent(p *profile.Profile) string {
	for _, h := range p.Headers {
		if h.Name == "user-agent" {
			return h.Value
		}
	}
	return ""
}

// try sends req once: on the pool's connection for the route, another
// host's the profile's coalescing rule allows, or a new one.
func (e *Engine) try(ctx context.Context, req *http.Request, r route) (*http.Response, error) {
	got, err := e.pool.Get(ctx, r.key)
	if err != nil {
		return nil, err
	}
	switch got.Found {
	case pool.FoundShared:
		return e.roundTripH2(r.key, got.Shared, req)
	case pool.FoundExclusive:
		resp, err := e.roundTripH1(r.key, got.Exclusive, req)
		if err != nil && replayable(req) && ctx.Err() == nil {
			// The server may have closed the idle connection as the
			// request went out: try a new one.
			return nil, fmt.Errorf("%w: %w", errRetry, err)
		}
		return resp, err
	}
	d := got.Dial
	if d.MayShare() && e.profile.Connections.Coalesce == profile.CoalesceOnLookup {
		if c := e.coalesceOnLookup(ctx, r); c != nil {
			return e.roundTripH2(r.key, d.Adopt(c), req)
		}
	}
	conn, proto, err := e.dial(ctx, req, r)
	if err != nil {
		d.Fail(err)
		return nil, err
	}
	if proto != "h2" {
		d.Exclusive()
		return e.roundTripH1(r.key, e.newH1(conn, r), req)
	}
	if e.profile.Connections.Coalesce == profile.CoalesceOnConnect {
		if c := e.coalesceOnConnect(r, conn); c != nil {
			// The browser opens the connection, then lets it go.
			_ = h2.Decline(conn, e.profile)
			return e.roundTripH2(r.key, d.Adopt(c), req)
		}
	}
	hc, err := h2.New(conn, e.profile, e.clock)
	if err != nil {
		_ = conn.Close()
		d.Fail(err)
		return nil, err
	}
	return e.roundTripH2(r.key, d.Shared(newShared(hc, conn, r)), req)
}

// roundTripH2 sends req on c. A request c did not send (it is going away
// or failed) goes to another connection, and c leaves the pool, finishing
// what it has in flight.
func (e *Engine) roundTripH2(key string, c *shared, req *http.Request) (*http.Response, error) {
	resp, err := c.RoundTrip(req)
	if errors.Is(err, h2.ErrConnUnusable) {
		e.pool.RemoveShared(key, c)
		return nil, fmt.Errorf("%w: %w", errNotSent, err)
	}
	return resp, err
}

// newH1 wraps conn, which carries HTTP/1.1, for the route's requests.
func (e *Engine) newH1(conn net.Conn, r route) *h1.Conn {
	c := h1.New(conn, e.profile)
	if r.forward {
		c.Forward(e.profile.Proxy)
	}
	return c
}

// dial connects, through the route's proxy if it has one, and, for https,
// performs the profile's TLS handshake, bounded by the dial timeout. It
// returns the negotiated protocol ("h2", or HTTP/1.1 otherwise).
func (e *Engine) dial(ctx context.Context, req *http.Request, r route) (net.Conn, string, error) {
	var conn net.Conn
	var err error
	switch {
	case r.proxy == nil:
		conn, err = e.dialer.Dial(ctx, r.addr)
	case r.forward:
		conn, err = e.dialer.DialProxy(ctx, r.proxy)
	default:
		conn, err = e.dialer.DialVia(ctx, r.proxy, r.addr)
	}
	if err != nil {
		return nil, "", err
	}
	if req.URL.Scheme == "http" {
		return conn, "http/1.1", nil
	}
	cfg := handshake.Config{
		ServerName: req.URL.Hostname(),
		RootCAs:    e.rootCAs,
	}
	hello := e.plan.Hello()
	if e.tickets != nil {
		// Tickets are kept per connection pool, partition and proxy
		// included: a browser's credential-less connections do not resume
		// its others'.
		cfg.Tickets, cfg.TicketKey = e.tickets, r.key
		if cfg.Ticket = e.tickets.Take(cfg.TicketKey); cfg.Ticket != nil && cfg.Ticket.TLS13() {
			// A TLS 1.2 ticket goes in the full hello's session_ticket.
			hello = e.plan.ResumedHello()
		}
	}
	hctx, cancel := context.WithTimeout(ctx, e.dialer.Timeout)
	defer cancel()
	return handshake.Client(hctx, conn, hello, cfg)
}

// roundTripH1 sends req on c, which the pool gave or a dial made, and
// gives c back to the pool when the response ends.
func (e *Engine) roundTripH1(key string, c *h1.Conn, req *http.Request) (*http.Response, error) {
	return c.RoundTrip(req, func(reusable bool) {
		e.pool.Release(key, c, reusable)
	})
}

// CloseIdleConnections closes idle HTTP/1.1 connections and HTTP/2
// connections with no requests in flight.
func (e *Engine) CloseIdleConnections() {
	e.pool.CloseIdle()
}

// replayable reports whether req can be sent again on a new connection:
// idempotent, with no body or one that can be recreated.
func replayable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
	default:
		return false
	}
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// hostPort is u's host and port; without a port, or with an empty one, the
// scheme's default.
func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}
