package clients

import (
	"context"
	"net/http"
	"net/url"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/bench/target"
	"github.com/roachadam/mimicry/profiles"
)

// mimicryClient is mimicry with profiles.Chrome(). HTTP/1.1 is tested on the
// servers' http/1.1-only port and HTTP/2 with Config.DisableHTTP3; HTTP/3
// has no switch: the Transport moves to it after the server's Alt-Svc, as
// Chrome does, so callers warm up until responses come over HTTP/3.
//
// Requests are made as their kinds: the micro and load scenarios' API calls
// as Fetch from a page on the same origin (no CORS preflight), the page's
// HTML as a navigation and its subresources as their element kinds.
func mimicryClient() Factory {
	return Factory{
		Name:    "mimicry",
		Module:  "github.com/roachadam/mimicry",
		Profile: profiles.Chrome().Name,
		New:     newMimicry,
	}
}

type mimicryAdapter struct {
	client *http.Client
	tr     *mimicry.Transport
}

func newMimicry(o Options) (Client, error) {
	cfg := mimicry.Config{
		Profile:      profiles.Chrome(),
		RootCAs:      o.RootCAs,
		DisableHTTP3: o.Proto != target.H3,
	}

	if o.Proxy != "" {
		u, err := url.Parse(o.Proxy)
		if err != nil {
			return nil, err
		}

		cfg.Proxy = http.ProxyURL(u)
	}

	client, err := mimicry.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	tr, _ := client.Transport.(*mimicry.Transport)

	return &mimicryAdapter{
		client: client,
		tr:     tr,
	}, nil
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
		page := r.Page
		if page == "" {
			page = req.URL.Scheme + "://" + req.URL.Host + "/"
		}

		from, err := url.Parse(page)
		if err != nil {
			return Response{}, err
		}

		req = mimicry.With(req, mimicry.Request{
			Kind: mimicryKinds[r.Kind],
			From: from,
		})
	}

	return doStd(c.client, req)
}

func (c *mimicryAdapter) CloseIdle() {
	c.tr.CloseIdleConnections()
}

func (c *mimicryAdapter) Close() {
	c.tr.CloseIdleConnections()
}
