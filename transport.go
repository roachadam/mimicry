package mimicry

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"
	"golang.org/x/net/publicsuffix"

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
	// everything above stay the profile's. Nil uses net.Dialer.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
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
}

// Transport is an http.RoundTripper that sends requests as the profile's
// browser. It is safe for concurrent use.
type Transport struct {
	engine  Engine
	profile *profile.Profile
	headers http.Header
	// decode is true when the profile sends Accept-Encoding by default.
	decode bool
	// lastNav is the URL of the last navigation sent: the page other kinds
	// come from by default.
	lastNav atomic.Pointer[url.URL]
}

// NewTransport builds a Transport from cfg.
func NewTransport(cfg Config) (*Transport, error) {
	t := &Transport{
		engine:  cfg.Engine,
		profile: cfg.Profile,
		headers: cfg.Headers.Clone(),
	}
	if t.engine == nil {
		e, err := native.New(native.Config{
			Profile:     cfg.Profile,
			RootCAs:     cfg.RootCAs,
			DialTimeout: cfg.DialTimeout,
			DialContext: cfg.DialContext,
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

// NewClient returns an *http.Client using a new Transport and a cookie jar.
func NewClient(cfg Config) (*http.Client, error) {
	t, err := NewTransport(cfg)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(&cookiejar.Options{
		PublicSuffixList: publicsuffix.List,
	})
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: t,
		Jar:       jar,
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
		if exact {
			s.plan, s.header = &headerorder.Plan{}, req.Header.Clone()
		} else {
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
		ctx := pool.WithPartition(req.Context(), s.partition)
		if s.plan != nil {
			ctx = headerorder.WithPlan(ctx, s.plan)
		}
		req = req.Clone(ctx)
		req.Header = h
	}
	resp, err := t.engine.RoundTrip(req)
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
