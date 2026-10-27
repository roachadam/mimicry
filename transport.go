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
	"strings"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/engine/native"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/profile"
)

// HeaderOrderKey is a pseudo request header whose values are header names,
// in the order to send them; it is never sent itself. It replaces the
// profile's order, so profile defaults it does not name are not sent; the
// request's own unnamed headers follow the named ones. It overrides
// Config.HeaderPlacement; use a Place to move one header instead.
const HeaderOrderKey = headerorder.Key

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
	// DialContext opens TCP connections (to the proxy, if any), as
	// http.Transport's does; TLS and above stay the profile's. Nil uses
	// net.Dialer.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// Resolver looks host names up, for dialing when DialContext is nil and,
	// for profiles that coalesce on lookup (Chrome, Safari), to reuse
	// another host's HTTP/2 connection to the same address when its
	// certificate covers the host. Nil uses net.DefaultResolver. Set it with
	// DialContext when DialContext maps hosts to addresses DNS does not give.
	Resolver *net.Resolver
	// Proxy returns the proxy for a request, as http.Transport's does; nil,
	// or a nil URL, connects directly. Schemes: "http" (CONNECT for https,
	// whole requests for http), "socks5" and "socks5h" (the proxy resolves
	// names either way). The proxy sees what the profile's proxy section
	// says the browser sends (a profile without one cannot use proxies); URL
	// credentials go once the proxy answers 407. A failed proxy returns a
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
	// http3.discovery), never through a proxy, falling back to TCP when QUIC
	// fails. QUIC dials the Resolver's address directly, not DialContext.
	DisableHTTP3 bool
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
type Transport struct {
	engine  engine
	profile *profile.Profile
	// headers are Config.Headers, keys canonical; preflightHeaders those
	// of them CORS preflights get (preflightConfig).
	headers          http.Header
	preflightHeaders http.Header
	jar              *cookies.Jar
	// placements are Config.HeaderPlacement, lowercase.
	placements []headerorder.Placement
	// pins are Config.Pins by host.
	pins pinSet
	// decode is true when the profile sends Accept-Encoding by default.
	decode bool
	// lastNav is the URL of the last navigation sent: the page other kinds
	// come from by default.
	lastNav atomic.Pointer[url.URL]
	// preflights remembers successful CORS preflights; clock times them.
	preflights preflightCache
	clock      clockwork.Clock
}

// NewTransport builds a Transport from cfg.
func NewTransport(cfg Config) (*Transport, error) {
	if cfg.Profile == nil {
		return nil, errors.New("mimicry: Config.Profile is required")
	}
	// A private copy, validated once and shared with the engine, so later
	// changes to the caller's profile have no effect.
	data, err := profile.Encode(cfg.Profile)
	if err != nil {
		return nil, err
	}
	p, err := profile.Parse(data)
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
	pins, err := parsePins(cfg.Pins)
	if err != nil {
		return nil, err
	}
	t := &Transport{
		engine:           cfg.engine,
		profile:          p,
		headers:          headers,
		preflightHeaders: preflightConfig(p, headers),
		jar:              cfg.Cookies,
		placements:       places,
		pins:             pins,
		clock:            clockwork.NewRealClock(),
	}
	if t.engine == nil {
		e, err := native.New(native.Config{
			Profile:          p,
			RootCAs:          cfg.RootCAs,
			DialTimeout:      cfg.DialTimeout,
			DialContext:      cfg.DialContext,
			Resolver:         cfg.Resolver,
			Proxy:            cfg.Proxy,
			DisableHTTP3:     cfg.DisableHTTP3,
			VerifyConnection: t.verifier(cfg.VerifyConnection),
		})
		if err != nil {
			return nil, err
		}
		t.engine = e
	}
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, "accept-encoding") {
			t.decode = true
		}
	}

	return t, nil
}

// NewClient returns an *http.Client using a new Transport and, unless
// cfg.Cookies is set, a new jar with the profile's cookie rules (see
// Transport.Cookies). The jar is the transport's, not the client's Jar,
// because it needs the page, kind and redirects. The client follows
// redirects as far as the browser does (20, or the profile's
// redirects.navigation_limit for navigations); a custom CheckRedirect
// sees hops as http.Client built them.
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

	return &http.Client{
		Transport:     t,
		CheckRedirect: followRedirects,
	}, nil
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

// madeAs returns how req is made: as With's Request describes (a
// navigation by default), or Exact.
func madeAs(req *http.Request) (r Request, exact bool) {
	ctx := req.Context()
	r, _ = ctx.Value(requestKey{}).(Request)
	exact, _ = ctx.Value(exactKey{}).(bool)

	return r, exact
}

// use is why a request is worked out: to send it (RoundTrip) or only to
// plan it (Plan). They differ only where sending has effects: a hop's body
// and the cookie jar's access times.
type use int

const (
	toSend use = iota
	toPlan
)

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
	req = req.Clone(ctx)
	req.Header = h

	return req
}

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
		h := req.Header.Clone()
		if h == nil {
			h = http.Header{}
		}
		chain := append(redirectsBefore(req), req.URL)
		addHeaders(h, t.headers, chain)

		return &sending{
			plan:   &headerorder.Plan{},
			header: h,
			// Exact still keeps cookies, as a navigation from its page.
			credentialed: true,
			cookies: cookies.Request{
				URL:        req.URL,
				Method:     req.Method,
				Navigation: true,
				Page:       r.From,
				Redirects:  chain[:len(chain)-1],
			},
			variant: -1,
		}, nil, nil
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
