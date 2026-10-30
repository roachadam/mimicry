// Package mimicry is an HTTP client that looks like a real browser on the
// network: the same TLS handshake, HTTP/2 and HTTP/3 settings, and headers
// in the same order. Use it to automate user actions on sites that check
// them.
//
//	client, err := mimicry.NewClient(mimicry.Config{
//		Profile: profiles.Chrome(),
//	})
//	if err != nil {
//		return err
//	}
//	resp, err := client.Get("https://shop.example/") // a page load
//
// A request is a page load, as if typed into the address bar, unless you
// say otherwise. To send it as a script on the last page would (fetch, XHR,
// an image), wrap it with As or With:
//
//	req, _ := http.NewRequest("GET", "https://shop.example/api/cart", nil)
//	resp, err = client.Do(mimicry.As(req, mimicry.Fetch))
//
// Defaults match the browser; Config fields that change that say so. A
// profile holds only what was recorded from the real browser: asking for
// anything else fails with ErrNotMeasured instead of guessing. Guides are in
// the repository's docs directory.
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
	// Essentials and network.

	// Profile is the browser to impersonate, such as profiles.Chrome().
	// Required. NewTransport keeps a copy, so later changes to it have no
	// effect.
	Profile *profile.Profile
	// RootCAs verifies servers. Nil, the default, uses the system roots.
	RootCAs *x509.CertPool
	// DialTimeout bounds each TCP connect and TLS handshake. Zero means
	// 30 seconds.
	DialTimeout time.Duration
	// Timeout is the Timeout of the client NewClient returns. It covers the
	// whole request, redirects and body included. Zero means 30 seconds;
	// negative means none. Raise it for large downloads or slow proxies.
	// NewTransport ignores it: set your own http.Client's Timeout instead.
	Timeout time.Duration
	// DialContext opens TCP connections, to the proxy if there is one, as
	// in http.Transport. TLS stays the browser's. Nil uses net.Dialer.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// Resolver looks up host names. Nil uses net.DefaultResolver. Set it
	// alongside DialContext when DialContext sends hosts to addresses DNS
	// does not give, so connection sharing between hosts stays correct.
	Resolver *net.Resolver
	// Proxy picks the proxy for each request, as in http.Transport. Nil,
	// the default, connects directly. Supported schemes are "http",
	// "socks5" and "socks5h"; a failed proxy returns a *ProxyError. See
	// docs/concepts/proxies.md.
	Proxy func(*http.Request) (*url.URL, error)

	// Headers and cookies.

	// Headers are your own headers, such as an API key, added to every
	// request where the browser places headers a page sets. They override
	// the browser's defaults; the request's own headers override them. A
	// key with no values removes a default. Default: none.
	Headers http.Header
	// HeaderPlacement moves headers within the browser's order on every
	// request you send, redirect hops included; CORS preflights, WebSocket
	// handshakes and Exact requests keep the browser's order. Default:
	// none. NewTransport rejects an invalid Place with ErrPlacement.
	HeaderPlacement []Place
	// Cookies is the cookie jar requests use. When nil, NewClient creates
	// one and NewTransport sends no cookies. Set it to share or seed
	// cookies.
	Cookies *cookies.Jar

	// Responses.

	// DisableDecompression returns bodies exactly as the server sent them.
	// By default gzip, deflate, br and zstd bodies are decoded, and
	// Content-Encoding and Content-Length removed. Other or stacked
	// encodings always come back raw.
	DisableDecompression bool

	// HTTP/3 and connections. Each of these defaults to what the browser
	// does, except BrowserQUICFallback and BrowserProxyAuth: there the
	// default is faster, and setting the field restores the browser.

	// DisableHTTP3 keeps every request on TCP. By default a request moves
	// to HTTP/3 when the browser would, falling back to TCP if QUIC fails.
	// Note that QUIC ignores DialContext. See docs/concepts/http3.md.
	DisableHTTP3 bool
	// AltSvc stores the HTTP/3 servers have advertised, shared by every
	// Transport given the same cache, like a browser's tabs. Save and load
	// it to keep it across runs. Nil, the default, keeps one per Transport.
	AltSvc *AltSvcCache
	// BrowserQUICFallback copies the browser's slow fallback when QUIC
	// stalls or fails: requests wait as long as the browser's do. By
	// default they move to TCP after the browser's race delay, or at once
	// on failure. Connections that work are the same either way.
	BrowserQUICFallback bool
	// BrowserProxyAuth sends an http proxy the URL's credentials only after
	// it answers 407, as browsers do. By default the first request sends
	// them, saving a round trip per new proxy. Only the proxy sees the
	// difference.
	BrowserProxyAuth bool
	// DisableCoalescing gives every host its own HTTP/2 connection. By
	// default a host shares another host's connection when the browser
	// would. Sharing is faster on clean networks; set this on lossy ones.
	DisableCoalescing bool
	// MaxConnsPerHost limits open HTTP/1.1 connections to one host; extra
	// requests wait their turn. Zero means the browser's limit, 6. A higher
	// value does not look like a browser. Negative is an error.
	MaxConnsPerHost int
	// IdleConnTimeout is how long an unused connection stays open. Zero
	// means 90 seconds; negative means mimicry never closes idle
	// connections itself. Longer reuses more connections at the cost of
	// memory.
	IdleConnTimeout time.Duration
	// MaxIdleProxies is how many recently used proxies keep their idle
	// connections. Zero means the 16 most recent, plus any used in the last
	// 10 seconds. Set 1 to close a proxy's connections as soon as you
	// switch away from it. Negative is an error.
	MaxIdleProxies int
	// HTTP3OverSOCKS5 lets requests through a SOCKS5 proxy use HTTP/3.
	// Browsers never use HTTP/3 through a proxy, so by default those
	// requests stay on TCP. A proxy that refuses UDP keeps them on TCP.
	HTTP3OverSOCKS5 bool
	// UDPReadBufferSize is the receive buffer, in bytes, each HTTP/3
	// connection asks the OS for. Zero asks for 1 MB, as Chrome does;
	// negative keeps the OS default. The OS may grant less. Raise it for
	// fast, long or lossy paths; nothing changes on the wire.
	UDPReadBufferSize int
	// HTTP3ACKEvery acknowledges every Nth QUIC packet instead of the
	// browser's rate, such as Chrome's every 10th. Zero, the default, keeps
	// the browser's. It must be between the browser's rate and 64. Fewer
	// ACKs help a busy server, but the server can see the difference.
	HTTP3ACKEvery int

	// Certificate checks.

	// Pins pins hosts to public keys: a request to a pinned host fails
	// with a *PinError unless the certificate chain matches. Default: none.
	// FetchPins lists a site's pins.
	Pins []Pin
	// VerifyConnection, when set, checks each connection after certificate
	// verification and Pins, as crypto/tls's VerifyConnection does. An
	// error fails the request before anything is sent. Default: none.
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
// Call CloseIdleConnections when you discard a Transport, or the client
// built on it. Otherwise idle connections close after
// Config.IdleConnTimeout, and those of an unreachable Transport close when
// it is garbage collected. A proxy no longer used loses its idle
// connections early; see Config.MaxIdleProxies.
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
	// raw is Config.DisableDecompression.
	raw bool
	// lastNav is the URL of the last navigation sent: the page other kinds
	// come from by default.
	lastNav atomic.Pointer[url.URL]
	// preflights remembers successful CORS preflights; clock times them.
	preflights cors.Cache
	clock      clockwork.Clock
}

// NewTransport builds a Transport from cfg. Building one is not free: build
// Transports up front, one per proxy, and reuse them rather than building
// one per request.
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
		raw:              cfg.DisableDecompression,
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
			MaxIdleProxies:    cfg.MaxIdleProxies,
			HTTP3OverSOCKS5:   cfg.HTTP3OverSOCKS5,
			UDPReadBufferSize: cfg.UDPReadBufferSize,
			HTTP3ACKEvery:     cfg.HTTP3ACKEvery,
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
// (30 s by default). It costs what NewTransport does: build clients up
// front and reuse them.
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
// A gzip, deflate, br or zstd body is decoded and Content-Encoding and
// Content-Length removed, unless Config.DisableDecompression.
// Response.TLS is the connection's state.
//
// Redirects go out as the browser sends them: 301/302 keep a PUT, DELETE
// or PATCH method and body, and chains stop after the browser's limit. A
// cross-origin CORS request whose redirect does not allow its origin
// returns a *CORSError instead, unless Request.IgnoreCORS.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, exact := madeAs(req)
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

	return t.finish(req, r, exact, resp, err)
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
func (t *Transport) finish(req *http.Request, r Request, exact bool, resp *http.Response, err error) (*http.Response, error) {
	if err == nil && r.Kind == Navigate && !exact {
		t.lastNav.Store(req.URL)
	}

	if err != nil || t.raw || !hasBody(req, resp) {
		return resp, err
	}

	// Several encodings, on one line or several, are not decoded.
	encs := resp.Header.Values("Content-Encoding")
	if len(encs) != 1 {
		return resp, nil
	}

	enc := strings.ToLower(strings.TrimSpace(encs[0]))

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
