package clients

import (
	"context"
	"sync"

	azuretls "github.com/Noooste/azuretls-client"
	utls "github.com/Noooste/utls"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// azuretlsClient is github.com/Noooste/azuretls-client's Session with its
// Chrome preset. HTTP/1.1 is forced per request (Request.ForceHTTP1),
// HTTP/2 is ALPN's choice with HTTP/3 never enabled, HTTP/3 is EnableHTTP3
// plus Request.ForceHTTP3. Session.Do returns the body buffered (its
// default). Its HTTP/3 transport ignores Session.ModifyConfig, so over
// QUIC it trusts the benchmark's CA through the system roots: the client
// container sets SSL_CERT_FILE to the CA. Headers: Chrome's navigation
// set, as its users pass them. Its idle pool gets MaxIdleConnsPerHost
// 1024, as net/http's and tls-client's do; the Session creates its
// Transport on the first request, so the adapter sets it after the first
// response, which every scenario sends alone. Its request timeout is
// requestTimeout (SetTimeout; its default is 30 s). Through a socks5 proxy
// with HTTP/3 enabled its QUIC goes over a UDP association; rotation is
// its SetProxy.
func azuretlsClient() Factory {
	return Factory{
		Name:    "azuretls",
		Module:  "github.com/Noooste/azuretls-client",
		Profile: "chrome (GetLastChromeVersion)",
		New: func(o Options) (Client, error) {
			return newAzureTLS(o, false)
		},
	}
}

// azuretlsStreamClient is azuretls with Request.IgnoreBody: Session.Do
// returns before the body, which the adapter reads from Response.RawBody
// as it arrives (8 KB reads, as the streaming clients), the same bytes
// over the same connection. What IgnoreBody also skips: the response's
// cookies (Session.Do sets them in its jar only when it reads the body;
// the servers set none) and Do's own deadline, which the adapter applies
// to the context instead (requestTimeout).
func azuretlsStreamClient() Factory {
	f := azuretlsClient()

	f.Name = "azuretls-stream"
	f.New = func(o Options) (Client, error) {
		return newAzureTLS(o, true)
	}

	return f
}

type azuretlsAdapter struct {
	s      *azuretls.Session
	proto  target.Proto
	stream bool
	poolOK sync.Once
}

func newAzureTLS(o Options, stream bool) (Client, error) {
	pool := o.RootCAs

	s := azuretls.NewSession()

	s.Browser = azuretls.Chrome
	s.ModifyConfig = func(c *utls.Config) error {
		c.RootCAs = pool

		return nil
	}

	s.SetTimeout(requestTimeout)

	if o.Proxy != "" {
		if err := s.SetProxy(o.Proxy); err != nil {
			s.Close()

			return nil, err
		}
	}

	if o.Proto == target.H3 {
		if err := s.EnableHTTP3(); err != nil {
			s.Close()

			return nil, err
		}
	}

	return &azuretlsAdapter{
		s:      s,
		proto:  o.Proto,
		stream: stream,
	}, nil
}

func (c *azuretlsAdapter) Do(ctx context.Context, r *Request) (Response, error) {
	headers := make(azuretls.OrderedHeaders, 0, len(chromeHeaders)+1)
	for _, h := range chromeHeaders {
		headers = append(headers, []string{h[0], h[1]})
	}

	if r.ContentType != "" {
		headers = append(headers, []string{"content-type", r.ContentType})
	}

	req := &azuretls.Request{
		Method:         r.Method,
		Url:            r.URL,
		OrderedHeaders: headers,
		ForceHTTP1:     c.proto == target.H1,
		ForceHTTP3:     c.proto == target.H3,
		IgnoreBody:     c.stream,
	}

	if r.Body != nil {
		req.Body = r.Body
	}

	if c.stream {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}

	resp, err := c.s.Do(req, ctx)
	if err != nil {
		return Response{}, err
	}

	c.poolOK.Do(func() {
		if c.s.Transport != nil {
			c.s.Transport.MaxIdleConnsPerHost = 1024
		}
	})

	proto := ""
	if resp.HttpResponse != nil {
		proto = resp.HttpResponse.Proto
	}

	n := int64(len(resp.Body))

	switch {
	case c.stream:
		n, err = drain(resp.RawBody, r.Arrivals)

		_ = resp.RawBody.Close()

	case r.Arrivals != nil:
		_, _ = r.Arrivals.Write(resp.Body) // buffered: it arrived whole
	}

	return Response{
		Status:  resp.StatusCode,
		Proto:   protoOf(resp.Header.Get("X-Proto"), proto),
		Resumed: resumedHeader(resp.Header.Get("X-Tls-Resumed")),
		Bytes:   n,
	}, err
}

func (c *azuretlsAdapter) CloseIdle() {
	if c.s.Transport != nil {
		c.s.Transport.CloseIdleConnections()
	}

	if c.proto == target.H3 {
		// The HTTP/3 transport has no CloseIdleConnections: replace it.
		// EnableHTTP3 leaves Enabled as DisableHTTP3 set it.
		c.s.DisableHTTP3()

		_ = c.s.EnableHTTP3()
		c.s.HTTP3Config.Enabled = true
	}
}

func (c *azuretlsAdapter) SetProxy(proxy string) error {
	return c.s.SetProxy(proxy)
}

func (c *azuretlsAdapter) Close() {
	c.s.Close()
}
