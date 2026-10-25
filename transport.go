package mimicry

import (
	"compress/gzip"
	"compress/zlib"
	"context"
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
// in the order to send them. It replaces the profile's order for the
// request, so a profile default it does not name is not sent; headers the
// request sets that it does not name are still sent, after the named ones.
// It is never sent itself.
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
	// DialContext opens TCP connections, as http.Transport's does (to pin a
	// host to an address, or dial through your own network code); TLS and
	// everything above stay the profile's. Through a proxy it reaches the
	// proxy. Nil uses net.Dialer.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// Resolver looks host names up: for connections when DialContext is
	// nil, and, for a profile that coalesces on lookup (Chrome, Safari),
	// to find whether another host's HTTP/2 connection reaches an address
	// the request's host resolves to, which the request then takes, as the
	// browser does, if that connection's certificate covers the host. A
	// failed lookup only means a new connection. Nil uses
	// net.DefaultResolver. Set it with DialContext when DialContext maps
	// hosts to addresses DNS does not give.
	Resolver *net.Resolver
	// Proxy returns the proxy for a request, as http.Transport's does
	// (http.ProxyURL, http.ProxyFromEnvironment); nil, or a nil URL,
	// connects directly. An "http" proxy tunnels https requests with
	// CONNECT and takes http requests whole; "socks5" and "socks5h" tunnel
	// both, the proxy resolving host names either way. The proxy sees what
	// the profile's browser sends it (its proxy section; a profile without
	// one cannot use proxies); the server sees what it would without the
	// proxy. Credentials in the URL go as the browser sends them: once the
	// proxy asks (407), then unasked. Each proxy has its own connections and
	// TLS sessions.
	Proxy func(*http.Request) (*url.URL, error)
	// Headers are added to every request: the caller's own, such as an API
	// key, not the profile's. They take the place the request's kind gives
	// them (page-set headers), override the profile's defaults and computed
	// headers, and yield to the request's own; a key with no values
	// suppresses the profile's default. Names match in any case. CORS
	// preflights get those their kind's header list names (User-Agent,
	// Accept-Language). Authorization and Cookie stop once a redirect chain
	// has left an origin, as a browser drops Authorization.
	Headers http.Header
	// Cookies is the cookie jar requests send from and responses store to,
	// under the profile's cookie rules. NewClient creates one when nil;
	// NewTransport leaves cookies to the caller (an http.Client Jar, or
	// Cookie headers) when nil. A request that sets its own Cookie header
	// sends that instead, and one set with no values sends none.
	Cookies *cookies.Jar

	// engine replaces the pure-Go engine (tests only).
	engine engine
}

// Transport is an http.RoundTripper that sends requests as the profile's
// browser. It is safe for concurrent use.
//
// A Transport is one browsing entity, like one browser tab: requests of
// other kinds than navigations come from its last navigation unless they
// name their page (Request.From). Tasks browsing in parallel should each
// use a Transport (or NewClient) of their own, or set Request.From.
type Transport struct {
	engine  engine
	profile *profile.Profile
	// headers are Config.Headers, keys canonical; preflightHeaders those
	// of them CORS preflights get (preflightConfig).
	headers          http.Header
	preflightHeaders http.Header
	jar              *cookies.Jar
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
	t := &Transport{
		engine:           cfg.engine,
		profile:          p,
		headers:          headers,
		preflightHeaders: preflightConfig(p, headers),
		jar:              cfg.Cookies,
		clock:            clockwork.NewRealClock(),
	}
	if t.engine == nil {
		e, err := native.New(native.Config{
			Profile:     p,
			RootCAs:     cfg.RootCAs,
			DialTimeout: cfg.DialTimeout,
			DialContext: cfg.DialContext,
			Resolver:    cfg.Resolver,
			Proxy:       cfg.Proxy,
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
// cfg.Cookies holds one, a new cookie jar with the profile's cookie rules.
// The jar is the transport's, not the client's Jar: it needs what an
// http.CookieJar never sees (the page, the request kind, redirects). The
// client follows redirects as far as the browser does: the Transport
// stops a chain after 20 (a navigation's after the profile's
// redirects.navigation_limit), not http.Client's default 10. A CheckRedirect of
// your own still applies, with the hops as http.Client built them (a PUT
// answered 301 shows as the GET the Transport sends as a PUT).
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
// When the profile sends Accept-Encoding and the caller did not set it
// (on the request or in Config.Headers), response bodies are decoded and
// Content-Encoding and Content-Length removed, as a browser would; HEAD
// responses, 204 and 304 responses, and those with Content-Length 0 are
// left as they are.
//
// A redirect http.Client follows goes out as the browser sends it: a PUT,
// DELETE or PATCH answered 301 or 302 keeps its method and body (its
// GetBody resends it), and a chain stops with an error after 20 redirects
// (a navigation's, or an Exact request's, after the profile's
// redirects.navigation_limit). A request in CORS mode to another origin
// (fetch(), XHR, an element with crossorigin, a font) whose redirect does
// not allow its origin (Access-Control-Allow-Origin, and -Credentials
// with credentials) returns a *CORSError instead of the redirect, so
// nothing more is sent, unless Request.IgnoreCORS. A hop is preflighted
// for its own method and headers, and, where the profile says so
// (redirects.hop_preflight), because the request before it was.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	r, _ := ctx.Value(requestKey{}).(Request)
	exact, _ := ctx.Value(exactKey{}).(bool)
	callerSet := hasHeader(req.Header, "Accept-Encoding") || hasHeader(t.headers, "Accept-Encoding")
	if req.Response != nil {
		next, err := browserHop(t.profile, req, exact || r.Kind == Navigate)
		if err != nil {
			if req.Body != nil && req.Body != http.NoBody {
				_ = req.Body.Close()
			}
			return nil, err
		}
		req = next
	}
	s, err := t.prepare(req, r, exact)
	if err != nil {
		// The RoundTripper contract: the body is closed, even on errors.
		if req.Body != nil && req.Body != http.NoBody {
			_ = req.Body.Close()
		}
		return nil, err
	}
	h := s.header
	ctx = pool.WithPartition(ctx, s.partition)
	if s.plan != nil {
		ctx = headerorder.WithPlan(ctx, s.plan)
	}
	if s.preflighted {
		// The next hop's preflight may depend on it (hopPreflight): it
		// sees this request as its Response's.
		ctx = context.WithValue(ctx, preflightedKey{}, true)
	}
	if t.jar != nil && s.credentialed && !hasHeader(h, "Cookie") {
		if line := cookies.Header(t.jar.Cookies(s.cookies)); line != "" {
			h.Set("Cookie", line)
		}
	}
	req = req.Clone(ctx)
	req.Header = h
	resp, err := t.engine.RoundTrip(req)
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

// prepare returns how to send req, made as r describes (or Exact), after
// its CORS preflight when it needs one.
func (t *Transport) prepare(req *http.Request, r Request, exact bool) (*sending, error) {
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
		}, nil
	}
	if r.Kind == kindPreflight || r.Kind == kindWebSocket {
		// The transport's own: callers never make one.
		return nil, fmt.Errorf("mimicry: unknown request kind %d", int(r.Kind))
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
		return nil, err
	}
	if s.preflighted, err = t.preflight(req, r, from, s.credentialed, s.header); err != nil {
		return nil, err
	}
	return s, nil
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
