package native

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

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

// Config configures the engine.
type Config struct {
	Profile     *profile.Profile
	RootCAs     *x509.CertPool
	DialTimeout time.Duration
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Engine is the pure-Go engine: pooled connections, one dial path, uTLS
// handshakes from the profile's plan, and HTTP/2 or HTTP/1.1 by ALPN.
// It is safe for concurrent use.
type Engine struct {
	plan    *fingerprint.Plan
	profile *profile.Profile
	clock   clockwork.Clock
	rootCAs *x509.CertPool
	dialer  dial.Dialer
	// tickets keeps TLS session tickets when the profile resumes.
	tickets *handshake.Tickets

	// mu guards the pool. It is never held across network I/O: dials,
	// handshakes and HTTP/2 setup happen with it released.
	mu    sync.Mutex
	h2    map[string]*h2.Conn
	idle  map[string][]idleConn
	dials map[string]*dialCall
	// h1Hosts are pools whose server negotiated HTTP/1.1: new requests
	// dial in parallel instead of waiting for a dial in progress.
	h1Hosts map[string]bool
}

type idleConn struct {
	conn  *h1.Conn
	since time.Time
}

// dialCall lets concurrent requests to one host share a dial.
type dialCall struct {
	done chan struct{}
	h2   *h2.Conn // set if the dial produced an HTTP/2 connection
	err  error
}

// New builds an engine for cfg.Profile.
func New(cfg Config) (*Engine, error) {
	if cfg.Profile == nil {
		return nil, errors.New("mimicry: Config.Profile is required")
	}
	plan, err := fingerprint.Compile(cfg.Profile)
	if err != nil {
		return nil, err
	}
	// An own copy, so later changes to the caller's profile have no effect.
	data, err := profile.Encode(cfg.Profile)
	if err != nil {
		return nil, err
	}
	p, err := profile.Parse(data)
	if err != nil {
		return nil, err
	}
	timeout := cfg.DialTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	e := &Engine{
		plan:    plan,
		profile: p,
		clock:   clockwork.NewRealClock(),
		rootCAs: cfg.RootCAs,
		dialer: dial.Dialer{
			Timeout:     timeout,
			DialContext: cfg.DialContext,
		},
		h2:      map[string]*h2.Conn{},
		idle:    map[string][]idleConn{},
		dials:   map[string]*dialCall{},
		h1Hosts: map[string]bool{},
	}
	if plan.Resumes() {
		e.tickets = handshake.NewTickets(p.TLS.Resumption.TLS12)
	}
	return e, nil
}

// errRetry means the request did not start and should be tried again.
var errRetry = errors.New("retry")

// RoundTrip sends req on a pooled or new connection.
func (e *Engine) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Host == "" {
		return nil, errors.New("mimicry: request has no host")
	}
	if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
		return nil, fmt.Errorf("mimicry: unsupported scheme %q", req.URL.Scheme)
	}
	key := poolKey(req)
	for attempt := 0; ; attempt++ {
		resp, err := e.try(req, key)
		if !errors.Is(err, errRetry) {
			return resp, err
		}
		if attempt == 4 {
			return nil, errors.New("mimicry: no usable connection after several attempts")
		}
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		// The failed attempt may have consumed the body: send a fresh copy,
		// on a clone so the caller's request is not modified.
		if req.Body != nil && req.Body != http.NoBody {
			if req.GetBody == nil {
				return nil, errors.New("mimicry: connection failed and the request body cannot be resent")
			}
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}

// poolKey is the pool a request's connection belongs to: scheme, host and
// port, and its partition.
func poolKey(req *http.Request) string {
	key := req.URL.Scheme + "://" + hostPort(req.URL.Scheme, req.URL.Host)
	if p := pool.Partition(req.Context()); p != "" {
		key += " " + p
	}
	return key
}

func (e *Engine) try(req *http.Request, key string) (*http.Response, error) {
	e.mu.Lock()
	if c := e.h2[key]; c != nil {
		if c.CanTakeNewRequest() {
			e.mu.Unlock()
			resp, err := c.RoundTrip(req)
			if errors.Is(err, h2.ErrConnUnusable) {
				e.dropH2(key, c)
				return nil, errRetry
			}
			return resp, err
		}
		delete(e.h2, key)
	}
	if c, ok := e.takeIdle(key); ok {
		e.mu.Unlock()
		resp, err := e.roundTripH1(key, c, req)
		if err != nil && replayable(req) && req.Context().Err() == nil {
			// The server may have closed the idle connection: try a new one.
			return nil, errRetry
		}
		return resp, err
	}
	if e.h1Hosts[key] {
		// The server speaks HTTP/1.1 only: like a browser, open another
		// connection at once instead of waiting for one in progress.
		e.mu.Unlock()
		conn, proto, err := e.dial(req)
		if err != nil {
			return nil, err
		}
		if proto == "h2" {
			// It changed its mind; use this one and share it from now on.
			return e.adoptH2(key, conn, req)
		}
		return e.roundTripH1(key, h1.New(conn, e.profile), req)
	}
	if call := e.dials[key]; call != nil {
		e.mu.Unlock()
		select {
		case <-call.done:
			// HTTP/2: the next attempt finds the shared connection. HTTP/1.1
			// or a failed dial: the next attempt dials its own.
			return nil, errRetry
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	call := &dialCall{
		done: make(chan struct{}),
	}
	e.dials[key] = call
	e.mu.Unlock()

	conn, proto, err := e.dial(req)
	var hc *h2.Conn
	if err == nil && proto == "h2" {
		hc, err = h2.New(conn, e.profile, e.clock)
		if err != nil {
			_ = conn.Close()
		}
	}
	e.mu.Lock()
	delete(e.dials, key)
	call.err = err
	if hc != nil {
		e.h2[key] = hc
		call.h2 = hc
		delete(e.h1Hosts, key)
	} else if err == nil {
		e.h1Hosts[key] = true
	}
	close(call.done)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if hc != nil {
		resp, err := hc.RoundTrip(req)
		if errors.Is(err, h2.ErrConnUnusable) {
			e.dropH2(key, hc)
		}
		return resp, err
	}
	return e.roundTripH1(key, h1.New(conn, e.profile), req)
}

// adoptH2 makes conn, which negotiated h2, the pool's shared connection for
// key (closing it if one already took that place) and sends req on it.
func (e *Engine) adoptH2(key string, conn net.Conn, req *http.Request) (*http.Response, error) {
	hc, err := h2.New(conn, e.profile, e.clock)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	e.mu.Lock()
	delete(e.h1Hosts, key)
	if existing := e.h2[key]; existing != nil && existing.CanTakeNewRequest() {
		e.mu.Unlock()
		_ = hc.Close()
		return nil, errRetry
	}
	e.h2[key] = hc
	e.mu.Unlock()
	resp, err := hc.RoundTrip(req)
	if errors.Is(err, h2.ErrConnUnusable) {
		e.dropH2(key, hc)
	}
	return resp, err
}

// dial connects and, for https, performs the profile's TLS handshake. It
// returns the negotiated protocol ("h2", or HTTP/1.1 otherwise).
func (e *Engine) dial(req *http.Request) (net.Conn, string, error) {
	ctx := req.Context()
	addr := hostPort(req.URL.Scheme, req.URL.Host)
	conn, err := e.dialer.Dial(ctx, addr)
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
		// Tickets are kept per connection pool, partition included: a
		// browser's credential-less connections do not resume its others'.
		cfg.Tickets, cfg.TicketKey = e.tickets, poolKey(req)
		if cfg.Ticket = e.tickets.Take(cfg.TicketKey); cfg.Ticket != nil && cfg.Ticket.TLS13() {
			// A TLS 1.2 ticket goes in the full hello's session_ticket.
			hello = e.plan.ResumedHello()
		}
	}
	return handshake.Client(ctx, conn, hello, cfg)
}

func (e *Engine) roundTripH1(key string, c *h1.Conn, req *http.Request) (*http.Response, error) {
	return c.RoundTrip(req, func(reusable bool) {
		if !reusable {
			_ = c.Close()
			return
		}
		e.mu.Lock()
		e.idle[key] = append(e.idle[key], idleConn{
			conn:  c,
			since: e.clock.Now(),
		})
		e.mu.Unlock()
	})
}

// takeIdle pops the most recently used idle connection, closing any that
// idled too long. e.mu must be held.
func (e *Engine) takeIdle(key string) (*h1.Conn, bool) {
	conns := e.idle[key]
	for len(conns) > 0 {
		ic := conns[len(conns)-1]
		conns = conns[:len(conns)-1]
		if e.clock.Since(ic.since) < idleTimeout {
			e.idle[key] = conns
			return ic.conn, true
		}
		_ = ic.conn.Close()
	}
	delete(e.idle, key)
	return nil, false
}

func (e *Engine) dropH2(key string, c *h2.Conn) {
	e.mu.Lock()
	if e.h2[key] == c {
		delete(e.h2, key)
	}
	e.mu.Unlock()
	_ = c.Close()
}

// CloseIdleConnections closes idle HTTP/1.1 connections and HTTP/2
// connections with no requests in flight.
func (e *Engine) CloseIdleConnections() {
	e.mu.Lock()
	var closing []interface{ Close() error }
	for key, conns := range e.idle {
		for _, ic := range conns {
			closing = append(closing, ic.conn)
		}
		delete(e.idle, key)
	}
	for key, c := range e.h2 {
		if c.Idle() {
			closing = append(closing, c)
			delete(e.h2, key)
		}
	}
	e.mu.Unlock()
	for _, c := range closing {
		_ = c.Close()
	}
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

// hostPort adds the scheme's default port when host has none.
func hostPort(scheme, host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if scheme == "http" {
		return net.JoinHostPort(host, "80")
	}
	return net.JoinHostPort(host, "443")
}
