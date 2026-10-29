package clients

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go/http3"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// netHTTP is the baseline: net/http's Transport for HTTP/1.1 and HTTP/2
// (held to the protocol under test with Transport.Protocols), quic-go's
// http3.Transport for HTTP/3. It keeps a TLS session cache, as a caller
// who wants resumption sets one, and no limit on idle connections per host
// below the highest concurrency tested. Its http.Client has the same 120 s
// request timeout as every adapter (requestTimeout). A proxy is
// Transport.Proxy returning the current URL. SetProxy replaces it and
// closes the idle connections, as a net/http user who rotates must: its
// HTTP/2 pool is keyed by host alone, so a request after the switch would
// otherwise go on through the old proxy's tunnel.
func netHTTP() Factory {
	return Factory{
		Name:       "net/http",
		Module:     "std",
		Profile:    "none (Go's own TLS and HTTP/2; quic-go for HTTP/3)",
		New:        newNetHTTP,
		NoH3SOCKS5: "quic-go's http3.Transport has no proxy setting (a caller would write the SOCKS5 UDP association themselves)",
	}
}

type netHTTPClient struct {
	client *http.Client
	closer func()
	idle   func()
	proxy  *atomic.Pointer[url.URL]
}

func newNetHTTP(o Options) (Client, error) {
	tlsConf := &tls.Config{
		RootCAs:            o.RootCAs,
		ClientSessionCache: tls.NewLRUClientSessionCache(256),
		MinVersion:         tls.VersionTLS12,
	}

	if o.Proto == target.H3 {
		tr := &http3.Transport{
			TLSClientConfig: tlsConf,
		}

		return &netHTTPClient{
			client: &http.Client{
				Transport: tr,
				Timeout:   requestTimeout,
			},
			closer: func() {
				_ = tr.Close()
			},
			idle: tr.CloseIdleConnections,
		}, nil
	}

	protos := new(http.Protocols)
	if o.Proto == target.H1 {
		protos.SetHTTP1(true)
	} else {
		protos.SetHTTP2(true)
	}

	tr := &http.Transport{
		TLSClientConfig:     tlsConf,
		Protocols:           protos,
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: 1024,
		IdleConnTimeout:     90 * time.Second,
	}

	proxy, err := proxyFunc(o.Proxy)
	if err != nil {
		return nil, err
	}

	if proxy != nil {
		tr.Proxy = func(*http.Request) (*url.URL, error) {
			return proxy.Load(), nil
		}
	}

	return &netHTTPClient{
		client: &http.Client{
			Transport: tr,
			Timeout:   requestTimeout,
		},
		closer: tr.CloseIdleConnections,
		idle:   tr.CloseIdleConnections,
		proxy:  proxy,
	}, nil
}

func (c *netHTTPClient) Do(ctx context.Context, r *Request) (Response, error) {
	req, err := stdRequest(ctx, r)
	if err != nil {
		return Response{}, err
	}

	return doStd(c.client, req, r.Arrivals)
}

func (c *netHTTPClient) CloseIdle() {
	c.idle()
}

func (c *netHTTPClient) SetProxy(proxy string) error {
	if err := storeProxy(c.proxy, proxy); err != nil {
		return err
	}

	c.idle()

	return nil
}

func (c *netHTTPClient) Close() {
	c.closer()
}

// doStd sends req with a net/http client (net/http's or mimicry's) and
// drains the body, counting it into a when set.
func doStd(client *http.Client, req *http.Request, a *Arrivals) (Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	n, err := drain(resp.Body, a)

	return Response{
		Status:  resp.StatusCode,
		Proto:   protoOf(resp.Header.Get("X-Proto"), resp.Proto),
		Resumed: resumedHeader(resp.Header.Get("X-Tls-Resumed")),
		Bytes:   n,
	}, err
}

func stdRequest(ctx context.Context, r *Request) (*http.Request, error) {
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return nil, err
	}

	if r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}

	return req, nil
}
