package mimicry

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/x509"
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

// HeaderOrderKey is a pseudo request header whose values set the order of the
// request's headers, overriding the profile's. It is never sent.
const HeaderOrderKey = headerorder.Key

// Engine sends requests. The default is the pure-Go engine.
type Engine interface {
	RoundTrip(req *http.Request) (*http.Response, error)
	CloseIdleConnections()
}

// Config configures a Transport.
type Config struct {
	// Profile is the browser to impersonate. Required.
	Profile *profile.Profile
	// RootCAs verifies servers; nil uses the system roots.
	RootCAs *x509.CertPool
	// DialTimeout bounds TCP connects; zero means 30 seconds.
	DialTimeout time.Duration
	// DialContext opens TCP connections, as http.Transport's does (to pin a
	// host to an address, or dial through your own network code); TLS and
	// everything above stay the profile's. Through a proxy it reaches the
	// proxy. Nil uses net.Dialer.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
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
	// Engine replaces the pure-Go engine; Profile, RootCAs and DialTimeout
	// are then ignored by the engine, which also ignores request kinds'
	// header lists and priorities.
	Engine Engine
	// Headers are added to every request: the caller's own, such as an API
	// key, not the profile's. They take the place the request's kind gives
	// them (page-set headers), override the profile's defaults and computed
	// headers, and yield to the request's own; a key with no values
	// suppresses the profile's default.
	Headers http.Header
	// Cookies is the cookie jar requests send from and responses store to,
	// under the profile's cookie rules. NewClient creates one when nil;
	// NewTransport leaves cookies to the caller (an http.Client Jar, or
	// Cookie headers) when nil. A request that sets its own Cookie header
	// sends that instead.
	Cookies *cookies.Jar
}

// Transport is an http.RoundTripper that sends requests as the profile's
// browser. It is safe for concurrent use.
type Transport struct {
	engine  Engine
	profile *profile.Profile
	headers http.Header
	jar     *cookies.Jar
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
	t := &Transport{
		engine:  cfg.Engine,
		profile: cfg.Profile,
		headers: cfg.Headers.Clone(),
		jar:     cfg.Cookies,
		clock:   clockwork.NewRealClock(),
	}
	if t.engine == nil {
		e, err := native.New(native.Config{
			Profile:     cfg.Profile,
			RootCAs:     cfg.RootCAs,
			DialTimeout: cfg.DialTimeout,
			DialContext: cfg.DialContext,
			Proxy:       cfg.Proxy,
		})
		if err != nil {
			return nil, err
		}
		t.engine = e
	}
	if cfg.Profile != nil {
		for _, h := range cfg.Profile.Headers {
			if strings.EqualFold(h.Name, "accept-encoding") {
				t.decode = true
			}
		}
	}
	return t, nil
}

// NewClient returns an *http.Client using a new Transport and, unless
// cfg.Cookies holds one, a new cookie jar with the profile's cookie rules.
// The jar is the transport's, not the client's Jar: it needs what an
// http.CookieJar never sees (the page, the request kind, redirects).
func NewClient(cfg Config) (*http.Client, error) {
	if cfg.Cookies == nil {
		var rules *profile.Cookies
		if cfg.Profile != nil {
			rules = cfg.Profile.Cookies
		}
		cfg.Cookies = cookies.New(cookies.Config{
			Rules: rules,
		})
	}
	t, err := NewTransport(cfg)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: t,
	}, nil
}

// RoundTrip implements http.RoundTripper. It sends req as its kind (As,
// With; a navigation by default) from its page, with Config.Headers added.
// When the profile chose Accept-Encoding (the request did not set it),
// response bodies are decoded and Content-Encoding removed, as a browser
// would.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	_, callerSet := req.Header[http.CanonicalHeaderKey("Accept-Encoding")]
	r, _ := req.Context().Value(requestKey{}).(Request)
	exact, _ := req.Context().Value(exactKey{}).(bool)
	if t.profile != nil {
		s := &sending{}
		var from *url.URL
		if exact {
			s.plan, s.header = &headerorder.Plan{}, req.Header.Clone()
			// Exact still keeps cookies, as a navigation from its page.
			s.credentialed = true
			s.cookies = cookies.Request{
				URL:        req.URL,
				Method:     req.Method,
				Navigation: true,
				Page:       r.From,
				Redirects:  redirectsBefore(req),
			}
		} else {
			from = r.From
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
			var err error
			if s, err = resolve(t.profile, req, r, from); err != nil {
				return nil, err
			}
		}
		h := s.header
		if h == nil {
			h = http.Header{}
		}
		for k, v := range t.headers {
			if _, set := h[k]; !set {
				h[k] = v
			}
		}
		if !exact {
			if err := t.preflight(req, r, from, s.credentialed, h); err != nil {
				return nil, err
			}
		}
		ctx := pool.WithPartition(req.Context(), s.partition)
		if s.plan != nil {
			ctx = headerorder.WithPlan(ctx, s.plan)
		}
		if t.jar != nil && s.credentialed && h.Get("Cookie") == "" {
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
		return t.finish(req, r, exact, callerSet, resp, err)
	}
	resp, err := t.engine.RoundTrip(req)
	return t.finish(req, r, exact, callerSet, resp, err)
}

// finish records the last navigation and decodes the body, as RoundTrip
// describes.
func (t *Transport) finish(req *http.Request, r Request, exact, callerSet bool, resp *http.Response, err error) (*http.Response, error) {
	if err == nil && r.Kind == Navigate && !exact {
		t.lastNav.Store(req.URL)
	}
	if err != nil || !t.decode || callerSet || req.Method == http.MethodHead {
		return resp, err
	}
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	var open func(io.Reader) (io.ReadCloser, error)
	switch enc {
	case "gzip", "x-gzip":
		open = func(r io.Reader) (io.ReadCloser, error) {
			return gzip.NewReader(r)
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
		d.dec, d.err = d.open(d.src)
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
