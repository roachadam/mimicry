package clients

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// netHTTP is the baseline: net/http's Transport for HTTP/1.1 and HTTP/2
// (held to the protocol under test with Transport.Protocols), quic-go's
// http3.Transport for HTTP/3. It keeps a TLS session cache, as a caller
// who wants resumption sets one, and no limit on idle connections per host
// below the highest concurrency tested.
func netHTTP() Factory {
	return Factory{
		Name:    "net/http",
		Module:  "std",
		Profile: "none (Go's own TLS and HTTP/2; quic-go for HTTP/3)",
		New:     newNetHTTP,
	}
}

type netHTTPClient struct {
	client *http.Client
	closer func()
	idle   func()
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
			QUICConfig: &quic.Config{
				MaxIncomingStreams: 1000,
			},
		}

		return &netHTTPClient{
			client: &http.Client{
				Transport: tr,
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

	if o.Proxy != "" {
		u, err := url.Parse(o.Proxy)
		if err != nil {
			return nil, err
		}

		tr.Proxy = http.ProxyURL(u)
	}

	return &netHTTPClient{
		client: &http.Client{
			Transport: tr,
		},
		closer: tr.CloseIdleConnections,
		idle:   tr.CloseIdleConnections,
	}, nil
}

func (c *netHTTPClient) Do(ctx context.Context, r *Request) (Response, error) {
	req, err := stdRequest(ctx, r)
	if err != nil {
		return Response{}, err
	}

	return doStd(c.client, req)
}

func (c *netHTTPClient) CloseIdle() {
	c.idle()
}

func (c *netHTTPClient) Close() {
	c.closer()
}

// doStd sends req with a net/http client (net/http's or mimicry's) and
// drains the body.
func doStd(client *http.Client, req *http.Request) (Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	n, err := io.Copy(io.Discard, resp.Body)

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
