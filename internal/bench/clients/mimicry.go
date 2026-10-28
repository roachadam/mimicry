package clients

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/bench/target"
	"github.com/roachadam/mimicry/profiles"
)

// mimicryClient is mimicry with profiles.Chrome(). HTTP/1.1 is tested on the
// servers' http/1.1-only port and HTTP/2 with Config.DisableHTTP3; HTTP/3
// has no switch: the Transport moves to it after the server's Alt-Svc, as
// Chrome does, so callers warm up until responses come over HTTP/3.
// Through a SOCKS5 proxy it goes over HTTP/3 with Config.HTTP3OverSOCKS5.
// Returning clients share one Config.AltSvc cache per process, as a
// returning Chrome starts with the alternatives it saved: once a client
// has learnt an origin's HTTP/3, the next ones start on it. A proxy is
// Config.Proxy returning the current URL, which SetProxy replaces: the
// rotation mimicry's docs show (docs/concepts/coming-from-tls-client.md).
//
// Requests are made as their kinds: the micro and load scenarios' API calls
// as Fetch from a page on the same origin (no CORS preflight), the page's
// HTML as a navigation and its subresources as their element kinds.
func mimicryClient() Factory {
	return Factory{
		Name:      "mimicry",
		Module:    "github.com/roachadam/mimicry",
		Profile:   profiles.Chrome().Name,
		New:       mimicryWith(nil),
		Returning: true,
	}
}

// Variants are mimicry with one Config option set, to measure what the
// option changes: bench run's -clients names them, and runs leave them out
// otherwise. mimicry-nocoalescing gives each host its own HTTP/2
// connection where Chrome shares one (DisableCoalescing); mimicry-conns16
// and -conns64 open up to 16 and 64 HTTP/1.1 connections per host
// (MaxConnsPerHost) instead of Chrome's 6.
func Variants() []Factory {
	variant := func(name string, set func(*mimicry.Config)) Factory {
		f := mimicryClient()

		f.Name = name
		f.New = mimicryWith(set)

		return f
	}

	return []Factory{
		variant("mimicry-nocoalescing", func(c *mimicry.Config) {
			c.DisableCoalescing = true
		}),
		variant("mimicry-conns16", func(c *mimicry.Config) {
			c.MaxConnsPerHost = 16
		}),
		variant("mimicry-conns64", func(c *mimicry.Config) {
			c.MaxConnsPerHost = 64
		}),
	}
}

// returningAltSvc is the Alt-Svc cache every returning client of the
// process shares.
var returningAltSvc = sync.OnceValue(mimicry.NewAltSvcCache)

type mimicryAdapter struct {
	client *http.Client
	tr     *mimicry.Transport
	// proxy is what Config.Proxy returns, when the client has one.
	proxy *atomic.Pointer[url.URL]
	// requests holds each kind and page's mimicry.Request, built once:
	// parsing the page per request would charge mimicry allocations that
	// are the benchmark's. A map under a lock: a sync.Map would box each
	// key it looks up.
	mu       sync.RWMutex
	requests map[requestKey]mimicry.Request
}

// requestKey is what a mimicry.Request depends on: the kind, and the page
// (or, with none, the request's origin).
type requestKey struct {
	kind, page, scheme, host string
}

// mimicryWith builds mimicry clients with set applied to their Config.
func mimicryWith(set func(*mimicry.Config)) func(Options) (Client, error) {
	return func(o Options) (Client, error) {
		return newMimicry(o, set)
	}
}

func newMimicry(o Options, set func(*mimicry.Config)) (Client, error) {
	cfg := mimicry.Config{
		Profile:      profiles.Chrome(),
		RootCAs:      o.RootCAs,
		DisableHTTP3: o.Proto != target.H3,
	}
	if o.Returning {
		cfg.AltSvc = returningAltSvc()
	}

	if set != nil {
		set(&cfg)
	}

	proxy, err := proxyFunc(o.Proxy)
	if err != nil {
		return nil, err
	}

	if proxy != nil {
		cfg.Proxy = func(*http.Request) (*url.URL, error) {
			return proxy.Load(), nil
		}
		cfg.HTTP3OverSOCKS5 = o.Proto == target.H3
	}

	client, err := mimicry.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	tr, _ := client.Transport.(*mimicry.Transport)

	return &mimicryAdapter{
		client:   client,
		tr:       tr,
		proxy:    proxy,
		requests: map[requestKey]mimicry.Request{},
	}, nil
}

// proxyFunc holds proxy for a Proxy function to return, or is nil without
// one.
func proxyFunc(proxy string) (*atomic.Pointer[url.URL], error) {
	if proxy == "" {
		return nil, nil //nolint:nilnil // no proxy
	}

	p := new(atomic.Pointer[url.URL])

	return p, storeProxy(p, proxy)
}

// storeProxy parses proxy into p, for SetProxy.
func storeProxy(p *atomic.Pointer[url.URL], proxy string) error {
	if p == nil {
		return errors.New("clients: SetProxy on a client built without a proxy")
	}

	u, err := url.Parse(proxy)
	if err != nil {
		return err
	}

	p.Store(u)

	return nil
}

var mimicryKinds = map[string]mimicry.Kind{
	"":       mimicry.Fetch,
	"fetch":  mimicry.Fetch,
	"style":  mimicry.Style,
	"script": mimicry.Script,
	"image":  mimicry.Image,
	"font":   mimicry.Font,
}

func (c *mimicryAdapter) Do(ctx context.Context, r *Request) (Response, error) {
	req, err := stdRequest(ctx, r)
	if err != nil {
		return Response{}, err
	}

	if r.Kind != "document" {
		key := requestKey{
			kind: r.Kind,
			page: r.Page,
		}
		if r.Page == "" {
			key.scheme, key.host = req.URL.Scheme, req.URL.Host
		}

		c.mu.RLock()

		mr, ok := c.requests[key]
		c.mu.RUnlock()

		if !ok {
			page := r.Page
			if page == "" {
				page = req.URL.Scheme + "://" + req.URL.Host + "/"
			}

			from, err := url.Parse(page)
			if err != nil {
				return Response{}, err
			}

			mr = mimicry.Request{
				Kind: mimicryKinds[r.Kind],
				From: from,
			}

			c.mu.Lock()

			c.requests[key] = mr
			c.mu.Unlock()
		}

		req = mimicry.With(req, mr)
	}

	return doStd(c.client, req)
}

func (c *mimicryAdapter) CloseIdle() {
	c.tr.CloseIdleConnections()
}

func (c *mimicryAdapter) SetProxy(proxy string) error {
	return storeProxy(c.proxy, proxy)
}

func (c *mimicryAdapter) Close() {
	c.tr.CloseIdleConnections()
}
