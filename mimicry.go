// Package mimicry is an HTTP client that reproduces real browsers' network
// fingerprints: TLS ClientHello, HTTP/2 frames and header order, HTTP/1.1
// header lines, and HTTP/3 where the browser uses it. Transport is an
// http.RoundTripper; browser profiles live in the profile and profiles
// packages.
//
// A request is a top-level navigation unless As or With says otherwise; the
// profile supplies the kind's headers, order and priority, and the page
// decides sec-fetch-site, Origin and Referer. Defaults reproduce the browser
// exactly; every deviation is the caller's, and asking for what the profile
// never measured is an error wrapping ErrNotMeasured, not a guess.
//
//	page, _ := http.NewRequest("GET", "https://example.com/", nil)
//	client.Do(page) // a navigation, as typed into the address bar
//	api, _ := http.NewRequest("GET", "https://api.example.com/v1", nil)
//	client.Do(mimicry.As(api, mimicry.Fetch)) // fetch() from the last page
package mimicry

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/cors"
	"github.com/roachadam/mimicry/internal/engine/native"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/pins"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/profile"
)

// engine sends requests: the pure-Go engine, or a test's fake.
type engine interface {
	RoundTrip(req *http.Request) (*http.Response, error)
	CloseIdleConnections()
}

// Config configures a Transport.
type Config struct {
	// Profile is the browser to impersonate. Required. NewTransport
	// validates it and keeps a copy, so later changes to it have no effect.
	Profile *profile.Profile
	// RootCAs verifies servers; nil uses the system roots.
	RootCAs *x509.CertPool
	// DialTimeout bounds TCP connects and TLS handshakes; zero means 30
	// seconds.
	DialTimeout time.Duration
	// Timeout is the http.Client.Timeout NewClient sets: zero means 30
	// seconds (as tls-client, httpcloak and azuretls default to), negative
	// means none. It covers the whole request, redirects and reading the
	// body included, so raise it for large downloads or slow proxies; with
	// it, a request whose server or connection stops mid-response fails
	// after it instead of waiting forever. The returned client's Timeout
	// may be changed afterwards. NewTransport ignores it: a caller
	// building its own http.Client sets that client's Timeout (or a
	// context deadline), as with net/http.
	Timeout time.Duration
	// DialContext opens TCP connections (to the proxy, if any), as
	// http.Transport's does; TLS and above stay the profile's. Nil uses
	// net.Dialer.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// Resolver looks host names up, for dialing when DialContext is nil and,
	// for profiles that coalesce on lookup (Chrome, Safari), to reuse
	// another host's HTTP/2 connection to the same address when its
	// certificate covers the host (unless DisableCoalescing). Nil uses
	// net.DefaultResolver. Set it with DialContext when DialContext maps
	// hosts to addresses DNS does not give.
	Resolver *net.Resolver
	// Proxy returns the proxy for a request, as http.Transport's does; nil,
	// or a nil URL, connects directly. Schemes: "http" (CONNECT for https,
	// whole requests for http), "socks5" and "socks5h" (the proxy resolves
	// names either way). The proxy sees what the profile's proxy section
	// says the browser sends (a profile without one cannot use proxies);
	// URL credentials go with the first request (see BrowserProxyAuth).
	// Each proxy URL has connections of its own; those of a proxy no
	// longer used close early (see Transport). A failed proxy returns a
	// *ProxyError.
	Proxy func(*http.Request) (*url.URL, error)
	// Headers are the caller's own headers (an API key, say), added to every
	// request where its kind places page-set headers. They override the
	// profile's defaults and computed headers and yield to the request's
	// own; a key with no values suppresses a default. CORS preflights get
	// only those their kind's list names. Authorization and Cookie are
	// dropped once a redirect chain leaves the origin.
	Headers http.Header
	// HeaderPlacement moves headers within the profile's order on every
	// caller request, redirect hops included, before Request.HeaderPlacement.
	// It skips CORS preflights, WebSocket handshakes, and requests with
	// their own order (Exact, HeaderOrderKey). NewTransport rejects an
	// invalid Place (ErrPlacement); a missing header or anchor fails the
	// request.
	HeaderPlacement []Place
	// Cookies is the jar requests send from and responses store to, under
	// the profile's cookie rules. When nil, NewClient creates one and
	// NewTransport leaves cookies to the caller. A request's own Cookie
	// header (or an empty one) overrides the jar.
	Cookies *cookies.Jar
	// DisableHTTP3 keeps every request on TCP. Otherwise a profile with an
	// http3 section moves to HTTP/3 as its browser does (Alt-Svc, profile
	// http3.discovery), never through a proxy unless HTTP3OverSOCKS5 says
	// so, falling back to TCP when QUIC fails. QUIC dials the Resolver's
	// address directly, not DialContext.
	DisableHTTP3 bool
	// AltSvc, when set, keeps the HTTP/3 alternatives servers advertise
	// (Alt-Svc), shared with every Transport given the same cache, as a
	// browser's tabs share them; AltSvcCache saves and loads them, as
	// browsers keep them across restarts. A request to an origin found in
	// it goes as the profile's browser sends one to an origin whose
	// alternative it knows (profile http3.discovery): Chrome and Safari
	// start on QUIC, racing TCP after their delay (300 and 100 ms) as
	// for any new connection; Firefox validates the alternative in the
	// background beside the request, which goes on TCP, and moves the
	// requests after to HTTP/3 once it is up. Nil keeps them per
	// Transport, learnt from its own responses.
	AltSvc *AltSvcCache
	// BrowserQUICFallback keeps the browser's timing where QUIC fails,
	// stalls included: Chrome holds every request behind an unanswered
	// 0-RTT connection until it gives up (4 s), Firefox those made while
	// it re-validates after the server's close (5 s), and a failed attempt
	// holds its request until the race delay. By default those requests go
	// on TCP once the profile's race delay (Chrome 300 ms; Firefox, which
	// has none, its 300 ms first probe timeout) has passed with no answer,
	// and at once when the attempt failed; the QUIC connection itself
	// carries on as the browser's, and a connection whose server answers
	// goes exactly as with this set (decision 0036).
	BrowserQUICFallback bool
	// BrowserProxyAuth gives an http proxy the URL's credentials as the
	// profile's browser does: only once the proxy has answered 407 (Chrome
	// and Firefox then send them unasked to that proxy URL; Safari waits
	// for a 407 on every tunnel), one round trip more for each new proxy
	// URL. By default the first CONNECT, and the first request sent to the
	// proxy whole, carry them, placed where the browser places them when
	// it sends them unasked. Only the proxy sees the difference: what
	// reaches the server through it is the same either way (decision
	// 0039).
	BrowserProxyAuth bool
	// DisableCoalescing gives every host its own HTTP/2 connection. By
	// default a request goes, as the profile's browser sends it, on another
	// host's connection whose certificate covers it at the same address
	// (profile connections.coalesce: Chrome and Safari after a DNS lookup;
	// Firefox after opening a connection of its own and closing it with a
	// GOAWAY). One connection for a site's hosts shares one TCP congestion
	// window: faster on a clean network, where it saves the other hosts'
	// handshakes, much slower on a lossy one (warm page loads about twice
	// as slow at 1% loss). With it set, a server hosting several of a
	// site's hosts sees one connection per host where the browser opens
	// one (decision 0037). Requests through a proxy never coalesce.
	DisableCoalescing bool
	// MaxConnsPerHost is the most HTTP/1.1 connections open at once to one
	// host (and port, partition and proxy); requests beyond it wait, first
	// come, first served. Zero means the profile's connections.http1PerHost,
	// 6 in every browser. A browser never has more than 6 open to one host:
	// a server that counts a client's concurrent HTTP/1.1 connections sees
	// a value above 6 as not a browser. HTTP/2 and HTTP/3 use one
	// connection per host whatever it says. Negative is an error.
	MaxConnsPerHost int
	// IdleConnTimeout is how long an idle connection is kept before
	// mimicry closes it: HTTP/1.1 since its last response, HTTP/2 and
	// HTTP/3 since their last request ended. Zero means 90 seconds
	// (decision 0040); negative means mimicry never closes an idle
	// connection itself, leaving it to the server, the proxy, QUIC's idle
	// timeout (the profile's, 30 s in Chrome and Firefox) or
	// CloseIdleConnections. A longer timeout reuses more connections
	// (no new handshake after a pause) at the cost of memory, a goroutine
	// per HTTP/2 or HTTP/3 connection, and, through a proxy, the proxy's
	// connection slots; servers and proxies often close idle connections
	// after 60 to 120 seconds anyway. Whatever it says, the connections of
	// a proxy no longer used close 10 to 20 seconds after its last use
	// once more than 16 proxies are known (see Transport), and those of a
	// dropped Transport when it is collected.
	IdleConnTimeout time.Duration
	// HTTP3OverSOCKS5 lets requests through a socks5 or socks5h proxy use
	// HTTP/3 as they would without it (Alt-Svc, 0-RTT, fallback to TCP),
	// over a SOCKS5 UDP association with the proxy (RFC 1928, section 7):
	// one per QUIC connection, naming the host for the proxy to resolve,
	// as its TCP connections do, with the URL's credentials. The server
	// receives the same datagrams as without the proxy. A proxy that
	// refuses UDP ASSOCIATE is asked once: its requests stay on TCP from
	// then on. One that drops an association (closes its control
	// connection) ends that QUIC connection, and its requests go on TCP
	// as on any failed connection. Browsers never use HTTP/3 through a
	// proxy, so by default, and through an http proxy always, requests
	// stay on TCP. Either way no datagram goes to the server directly
	// (decision 0038).
	HTTP3OverSOCKS5 bool
	// UDPReadBufferSize is the receive buffer, in bytes, each HTTP/3
	// connection's UDP socket asks the OS for (SO_RCVBUF), a SOCKS5
	// association's included. Zero keeps the OS's default (about 200 KB on
	// Linux, 768 KB on macOS). A larger buffer trades drops for a queue: in
	// the HTTP/3 benchmark (one host, nginx) a 7 MB buffer, quic-go's,
	// removed the drops and ran 10-15% faster for 100 KB bodies at 64 at
	// once, but half as fast at 256 at once, and 20% slower for 10 MB
	// bodies from the Go server: what it holds delays everything behind it,
	// the server's acknowledgements of the client's own packets included.
	// Raise it (2 to 8 MB) for high-bandwidth, lossy or long paths, where a
	// burst can overflow the default: each datagram dropped there is lost as
	// if on the network, costing a retransmission and congestion window. It
	// is a request: the OS may grant less, and the connection never fails for
	// it. Linux grants at most net.core.rmem_max, often 208 KB, without an
	// error, unless the host raises it (sysctl -w
	// net.core.rmem_max=8388608); mimicry never uses SO_RCVBUFFORCE, which
	// needs privileges. The wire is the same either way. Negative is an
	// error.
	UDPReadBufferSize int
	// Pins pins hosts to public keys: a request to a covered host goes only
	// on a connection whose verified chain matches one of the Pin's values,
	// else it fails with a *PinError before anything is sent. NewTransport
	// rejects a malformed Pin; FetchPins lists a site's pins.
	Pins []Pin
	// VerifyConnection, when set, checks each connection after certificate
	// verification and Pins; an error fails the request before anything is
	// sent. It runs on every TLS and QUIC handshake, resumed ones included
	// (crypto/tls VerifyConnection semantics, not VerifyPeerCertificate's),
	// and before coalescing another host onto an HTTP/2 connection, with
	// ServerName set to that host; an error there opens a new connection.
	// The state has no ExportKeyingMaterial.
	VerifyConnection func(tls.ConnectionState) error

	// engine replaces the pure-Go engine (tests only).
	engine engine
}

// Transport is an http.RoundTripper that sends requests as the profile's
// browser. It is safe for concurrent use.
//
// A Transport is one browsing entity, like a browser tab: non-navigation
// requests come from its last navigation unless they set Request.From.
// Parallel tasks should each use their own Transport, or set Request.From.
//
// Call CloseIdleConnections when you discard a Transport (or the client
// NewClient built on it), as when a new client takes over with a new
// proxy identity: its connections close at once. Otherwise an idle
// connection closes after Config.IdleConnTimeout, 90 s by default (HTTP/3
// ones at QUIC's idle timeout if that is sooner), and a Transport no longer reachable closes its idle
// connections once the garbage collector finds it. Within a Transport, a
// proxy no longer used loses its idle connections early: once it is not
// among the 16 proxies used most recently and has not been used for 10 s
// (decision 0040).
type Transport struct {
	engine  engine
	profile *profile.Profile
	// headers are Config.Headers, keys canonical; preflightHeaders those
	// of them CORS preflights get (cors.PreflightHeaders).
	headers          http.Header
	preflightHeaders http.Header
	jar              *cookies.Jar
	// placements are Config.HeaderPlacement, lowercase.
	placements []headerorder.Placement
	// pins are Config.Pins by host, in an allocation of their own: the
	// engine's verifier reads them without holding t.
	pins *pins.Set
	// decode is true when the profile sends Accept-Encoding by default.
	decode bool
	// lastNav is the URL of the last navigation sent: the page other kinds
	// come from by default.
	lastNav atomic.Pointer[url.URL]
	// preflights remembers successful CORS preflights; clock times them.
	preflights cors.Cache
	clock      clockwork.Clock
}

// NewTransport builds a Transport from cfg.
func NewTransport(cfg Config) (*Transport, error) {
	if cfg.Profile == nil {
		return nil, errors.New("mimicry: Config.Profile is required")
	}

	// A validated copy, shared with the engine and with other Transports
	// built from an equal profile, so later changes to the caller's
	// profile have no effect.
	p, err := snapshot(cfg.Profile)
	if err != nil {
		return nil, err
	}

	headers := make(http.Header, len(cfg.Headers))
	for k, v := range cfg.Headers {
		k = http.CanonicalHeaderKey(k)
		// A copy, and a key with no values stays present: it suppresses.
		headers[k] = append(headers[k], v...)
	}

	places, err := placements("Config.HeaderPlacement", cfg.HeaderPlacement)
	if err != nil {
		return nil, err
	}

	pinned, err := parsePins(cfg.Pins)
	if err != nil {
		return nil, err
	}

	t := &Transport{
		engine:           cfg.engine,
		profile:          p,
		headers:          headers,
		preflightHeaders: cors.PreflightHeaders(p, headers),
		jar:              cfg.Cookies,
		placements:       places,
		pins:             &pinned,
		clock:            clockwork.NewRealClock(),
	}
	if t.engine == nil {
		ncfg := native.Config{
			Profile:           p,
			RootCAs:           cfg.RootCAs,
			DialTimeout:       cfg.DialTimeout,
			DialContext:       cfg.DialContext,
			Resolver:          cfg.Resolver,
			Proxy:             cfg.Proxy,
			DisableHTTP3:      cfg.DisableHTTP3,
			BrowserFallback:   cfg.BrowserQUICFallback,
			Coalesce:          !cfg.DisableCoalescing,
			MaxConnsPerHost:   cfg.MaxConnsPerHost,
			IdleConnTimeout:   cfg.IdleConnTimeout,
			HTTP3OverSOCKS5:   cfg.HTTP3OverSOCKS5,
			UDPReadBufferSize: cfg.UDPReadBufferSize,
			BrowserProxyAuth:  cfg.BrowserProxyAuth,
			VerifyConnection:  t.verifier(cfg.VerifyConnection),
		}
		if cfg.AltSvc != nil {
			ncfg.AltSvc = cfg.AltSvc.c
		}

		e, err := native.New(ncfg)
		if err != nil {
			return nil, err
		}

		t.engine = e
	}

	// Nothing the engine holds refers to t (its connections' goroutines
	// keep the engine reachable, never t), so t can become unreachable
	// while connections are open: they close then.
	runtime.AddCleanup(t, closeIdle, t.engine)

	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, "accept-encoding") {
			t.decode = true
		}
	}

	return t, nil
}

// closeIdle closes a collected Transport's idle connections. A cleanup
// must not block the others: the closes run on their own goroutine.
func closeIdle(e engine) {
	go e.CloseIdleConnections()
}

// NewClient returns an *http.Client using a new Transport and, unless
// cfg.Cookies is set, a new jar with the profile's cookie rules (see
// Transport.Cookies). The jar is the transport's, not the client's Jar,
// because it needs the page, kind and redirects. The client follows
// redirects as far as the browser does (20, or the profile's
// redirects.navigationLimit for navigations); a custom CheckRedirect
// sees hops as http.Client built them. Its Timeout is Config.Timeout's
// (30 s by default).
func NewClient(cfg Config) (*http.Client, error) {
	t, err := NewTransport(cfg)
	if err != nil {
		return nil, err
	}

	if t.jar == nil {
		jar, err := cookies.New(cookies.Config{
			Rules: t.profile.Cookies,
		})
		if err != nil {
			return nil, err
		}

		t.jar = jar
	}

	timeout := cfg.Timeout
	switch {
	case timeout == 0:
		timeout = defaultClientTimeout
	case timeout < 0:
		timeout = 0
	}

	return &http.Client{
		Transport:     t,
		CheckRedirect: followRedirects,
		Timeout:       timeout,
	}, nil
}

// defaultClientTimeout is NewClient's http.Client.Timeout when
// Config.Timeout is zero.
const defaultClientTimeout = 30 * time.Second

// followRedirects is NewClient's CheckRedirect: the Transport stops a chain
// where the browser does (browserHop), not http.Client's default of 10.
func followRedirects(*http.Request, []*http.Request) error {
	return nil
}

// RoundTrip implements http.RoundTripper. It sends req as its kind (As,
// With; a navigation by default) from its page, with Config.Headers added.
// If the profile sends Accept-Encoding and the caller did not set it, the
// body is decoded and Content-Encoding and Content-Length are removed.
// Response.TLS is the connection's state.
//
// Redirects go out as the browser sends them: 301/302 keep a PUT, DELETE
// or PATCH method and body, and chains stop after the browser's limit. A
// cross-origin CORS request whose redirect does not allow its origin
// returns a *CORSError instead, unless Request.IgnoreCORS.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, exact := madeAs(req)
	callerSet := hasHeader(req.Header, "Accept-Encoding") || hasHeader(t.headers, "Accept-Encoding")
	req, o, err := t.outgoing(req, r, exact, toSend)

	if err == nil && o.preflight != nil {
		err = t.sendPreflight(o.preflight, r.IgnoreCORS)
	}

	if err != nil {
		// The RoundTripper contract: the body is closed, even on errors.
		if req.Body != nil && req.Body != http.NoBody {
			_ = req.Body.Close()
		}

		return nil, err
	}

	req = t.ready(req, o, toSend)

	resp, err := t.engine.RoundTrip(req)

	err = publicError(err)

	s := o.s

	if err == nil && t.jar != nil && s.credentialed {
		t.jar.SetCookies(s.cookies, resp.Header.Values("Set-Cookie"))
	}

	if err == nil && !r.IgnoreCORS {
		if cerr := refusedRedirect(req, resp, s); cerr != nil {
			return nil, cerr
		}
	}

	return t.finish(req, r, exact, callerSet, resp, err)
}

// out is how a request goes out: how it was resolved, its header
// placements, and the CORS preflight to send before it (nil when it
// needs none or a cached answer allows it).
type out struct {
	s         *sending
	places    []headerorder.Placement
	preflight *preflightSend
}

// outgoing works out how req goes out, up to its preflight: the browser's
// hop for a redirect (returned in req's place, even with an error), its
// placements, and how it is resolved. It sends nothing.
func (t *Transport) outgoing(req *http.Request, r Request, exact bool, u use) (*http.Request, out, error) {
	if req.Response != nil {
		next, err := browserHop(t.profile, req, exact || r.Kind == Navigate, u)
		if err != nil {
			return req, out{}, err
		}

		req = next
	}

	places, err := t.placementsFor(req, r, exact)
	if err != nil {
		return req, out{}, err
	}

	s, pf, err := t.prepare(req, r, exact)
	if err != nil {
		return req, out{}, err
	}

	return req, out{
		s:         s,
		places:    places,
		preflight: pf,
	}, nil
}

// placementsFor returns Config.HeaderPlacement then the request's own. A
// request that sets its own order takes none, and is an error if it has
// placements of its own.
func (t *Transport) placementsFor(req *http.Request, r Request, exact bool) ([]headerorder.Placement, error) {
	if exact || setsOrder(req.Header) || setsOrder(t.headers) {
		if len(r.HeaderPlacement) > 0 {
			return nil, fmt.Errorf("%w: Request.HeaderPlacement on a request that sets its own order (Exact or HeaderOrderKey)", ErrPlacement)
		}

		return nil, nil
	}

	if len(r.HeaderPlacement) == 0 {
		return t.placements, nil
	}

	own, err := placements("Request.HeaderPlacement", r.HeaderPlacement)
	if err != nil {
		return nil, err
	}

	return append(slices.Clip(t.placements), own...), nil
}

// ready returns req as the engine sends it, made as o says: on a context
// carrying its partition, plan, placements and whether it was
// preflighted, with its headers and the jar's cookies (read without
// changing the jar when only planning).
func (t *Transport) ready(req *http.Request, o out, u use) *http.Request {
	s := o.s
	h := s.header
	ctx := pool.WithPartition(req.Context(), s.partition)

	if s.plan != nil {
		ctx = headerorder.WithPlan(ctx, s.plan)
	}

	if s.preflighted {
		// The next hop's preflight may depend on it (hopPreflight): it
		// sees this request as its Response's.
		ctx = context.WithValue(ctx, preflightedKey{}, true)
	}

	if len(o.places) > 0 {
		// The caller's request only: its preflight goes without.
		ctx = headerorder.WithPlacements(ctx, o.places)
	}

	if line := t.cookieLine(s, u); line != "" {
		h.Set("Cookie", line)
	}

	// A clone without the caller's headers, which h replaces.
	shallow := *req

	shallow.Header = nil
	req = shallow.Clone(ctx)
	req.Header = h

	return req
}

// preflightedKey marks a request sent after a CORS preflight it needed, in
// its context, which a redirect's next hop sees as req.Response.Request.
type preflightedKey struct{}

// cookieLine is the Cookie line the jar adds to a request resolved as s:
// "" without a jar, credentials, or cookies, or when the request sets
// its own. Only to send does reading it update the jar (Cookies; Peek
// otherwise).
func (t *Transport) cookieLine(s *sending, u use) string {
	if t.jar == nil || !s.credentialed || hasHeader(s.header, "Cookie") {
		return ""
	}

	if u == toPlan {
		return cookies.Header(t.jar.Peek(s.cookies))
	}

	return cookies.Header(t.jar.Cookies(s.cookies))
}

// prepare returns how to send req, made as r describes (or Exact), and
// its CORS preflight when it needs one that no cached answer spares.
func (t *Transport) prepare(req *http.Request, r Request, exact bool) (*sending, *preflightSend, error) {
	if exact {
		return exactSending(req, r.From, t.headers), nil, nil
	}

	if r.Kind == kindPreflight || r.Kind == kindWebSocket {
		// The transport's own: callers never make one.
		return nil, nil, fmt.Errorf("mimicry: unknown request kind %d", int(r.Kind))
	}

	from := r.From
	if from == nil && r.Kind != Navigate {
		from = t.lastNav.Load()
		if from == nil {
			from = &url.URL{
				Scheme: req.URL.Scheme,
				Host:   req.URL.Host,
				Path:   "/",
			}
		}
	}

	s, err := resolve(t.profile, req, r, from, t.headers)
	if err != nil {
		return nil, nil, err
	}

	needed, pf, err := t.preflightFor(req, r, from, s.credentialed, s.header)
	if err != nil {
		return nil, nil, err
	}

	s.preflighted = needed

	return s, pf, nil
}

// preflightSend is a CORS preflight to send before a request: what it
// asks for, the preflight request ready for the engine, how it was
// resolved, and the profile's cache rule.
type preflightSend struct {
	need *cors.Need
	req  *http.Request
	s    *sending
	rule string
}

// preflightFor returns req's CORS preflight unless none is needed or a
// cached answer allows req, and whether one was needed at all. It changes
// nothing; RoundTrip sends pf, Plan only plans it.
func (t *Transport) preflightFor(req *http.Request, r Request, from *url.URL, credentialed bool, h http.Header) (needed bool, pf *preflightSend, err error) {
	origin := preflightOrigin(req, r, from)

	need := cors.Preflight(req, origin, credentialed, h, false)
	if need == nil {
		forced := cors.Preflight(req, origin, credentialed, h, true)
		inherit, err := t.hopPreflight(req, forced != nil)

		if err != nil || !inherit {
			return false, nil, err
		}

		need = forced
	}

	var rule string
	if t.profile.Context != nil {
		rule = t.profile.Context.PreflightCache
	}

	if rule == "" {
		return true, nil, fmt.Errorf("%w: profile %s has no CORS preflight cache rule", ErrNotMeasured, t.profile.Name)
	}

	if t.preflights.Allows(need, t.clock.Now()) {
		return true, nil, nil
	}

	ctx := req.Context()
	// The same URL and redirect chain (Response, which decides the
	// Origin), with nothing of the request's own.
	pre := req.Clone(ctx)

	pre.Method = http.MethodOptions
	pre.Header = http.Header{}
	pre.Body, pre.GetBody, pre.ContentLength = http.NoBody, nil, 0
	pre.TransferEncoding, pre.Trailer = nil, nil
	pre.Header.Set("Access-Control-Request-Method", need.Method)

	if len(need.Headers) > 0 {
		pre.Header.Set("Access-Control-Request-Headers", strings.Join(need.Headers, ","))
	}

	s, err := resolve(t.profile, pre, Request{
		Kind: kindPreflight,
	}, from, t.preflightHeaders)
	if err != nil {
		return true, nil, err
	}

	pctx := pool.WithPartition(ctx, s.partition)
	if s.plan != nil {
		pctx = headerorder.WithPlan(pctx, s.plan)
	}

	pre = pre.WithContext(pctx)
	pre.Header = s.header

	return true, &preflightSend{
		need: need,
		req:  pre,
		s:    s,
		rule: rule,
	}, nil
}

// sendPreflight sends pf and checks its answer: a *CORSError when it
// does not allow the request, unless ignore (Request.IgnoreCORS); a
// successful answer is cached.
func (t *Transport) sendPreflight(pf *preflightSend, ignore bool) error {
	resp, err := t.engine.RoundTrip(pf.req)
	if err != nil {
		return publicError(err)
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()

	if reason := cors.CheckPreflight(resp, pf.need); reason != "" {
		if ignore {
			return nil
		}

		return &CORSError{
			URL:    pf.req.URL,
			Reason: reason,
			Status: resp.StatusCode,
			Header: resp.Header,
		}
	}

	t.preflights.Store(pf.need, resp.Header, pf.rule == profile.PreflightCacheReplace, t.clock.Now())

	return nil
}

// hopPreflight reports whether a redirect hop that could be preflighted
// is, because the request before it was (redirects.hopPreflight inherit:
// Firefox and Safari, not Chrome). Without the rule it is not measured.
func (t *Transport) hopPreflight(req *http.Request, could bool) (bool, error) {
	if !could || req.Response == nil || req.Response.Request == nil {
		return false, nil
	}

	if before, _ := req.Response.Request.Context().Value(preflightedKey{}).(bool); !before {
		return false, nil
	}

	if t.profile.Redirects == nil {
		return false, fmt.Errorf("%w: profile %s has no redirects rules, for a hop after a CORS preflight", ErrNotMeasured, t.profile.Name)
	}

	return t.profile.Redirects.HopPreflight == profile.RedirectPreflightInherit, nil
}

// finish records the last navigation and decodes the body, as RoundTrip
// describes.
func (t *Transport) finish(req *http.Request, r Request, exact, callerSet bool, resp *http.Response, err error) (*http.Response, error) {
	if err == nil && r.Kind == Navigate && !exact {
		t.lastNav.Store(req.URL)
	}

	if err != nil || !t.decode || callerSet || !hasBody(req, resp) {
		return resp, err
	}

	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))

	var open func(io.Reader) (io.ReadCloser, error)

	switch enc {
	case "gzip", "x-gzip":
		open = func(r io.Reader) (io.ReadCloser, error) {
			zr, err := gzip.NewReader(r)
			if err != nil {
				// Not zr: a nil *gzip.Reader in a non-nil interface.
				return nil, err
			}

			return zr, nil
		}

	case "deflate":
		open = zlib.NewReader
	case "br":
		open = func(r io.Reader) (io.ReadCloser, error) {
			return brrr.NewReader(r), nil
		}

	case "zstd":
		open = func(r io.Reader) (io.ReadCloser, error) {
			d, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
			if err != nil {
				return nil, err
			}

			return d.IOReadCloser(), nil
		}

	default:
		return resp, nil
	}

	resp.Body = &decoder{
		src:  resp.Body,
		open: open,
	}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")

	resp.ContentLength = -1
	resp.Uncompressed = true

	return resp, nil
}

// hasBody reports whether resp to req can have a body to decode: not a
// HEAD request, not a 204 or 304, not a declared empty one.
func hasBody(req *http.Request, resp *http.Response) bool {
	switch {
	case req.Method == http.MethodHead:
		return false
	case resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified:
		return false
	}

	return resp.ContentLength != 0
}

// CloseIdleConnections closes connections with no requests in flight.
func (t *Transport) CloseIdleConnections() {
	t.engine.CloseIdleConnections()
}

// Cookies returns the transport's cookie jar: Config.Cookies, or the one
// NewClient made; nil when it has none.
func (t *Transport) Cookies() *cookies.Jar {
	return t.jar
}

// decoder opens its decompressor on first Read, so RoundTrip never blocks
// on the body.
type decoder struct {
	src  io.ReadCloser
	open func(io.Reader) (io.ReadCloser, error)
	dec  io.ReadCloser
	err  error
}

func (d *decoder) Read(p []byte) (int, error) {
	if d.dec == nil && d.err == nil {
		dec, err := d.open(d.src)
		if err != nil {
			d.err = err
		} else {
			d.dec = dec
		}
	}

	if d.err != nil {
		return 0, d.err
	}

	return d.dec.Read(p)
}

func (d *decoder) Close() error {
	if d.dec != nil {
		_ = d.dec.Close()
	}

	return d.src.Close()
}
