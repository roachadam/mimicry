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
// default; Request.IgnoreBody would stream). Its HTTP/3 transport ignores
// Session.ModifyConfig, so over QUIC it trusts the benchmark's CA through
// the system roots: the client container sets SSL_CERT_FILE to the CA.
// Headers: Chrome's navigation set, as its users pass them. Its idle pool
// gets MaxIdleConnsPerHost 1024, as net/http's and tls-client's do; the
// Session creates its Transport on the first request, so the adapter sets
// it after the first response, which every scenario sends alone.
func azuretlsClient() Factory {
	return Factory{
		Name:    "azuretls",
		Module:  "github.com/Noooste/azuretls-client",
		Profile: "chrome (GetLastChromeVersion)",
		New:     newAzureTLS,
	}
}

type azuretlsAdapter struct {
	s      *azuretls.Session
	proto  target.Proto
	poolOK sync.Once
}

func newAzureTLS(o Options) (Client, error) {
	pool := o.RootCAs

	s := azuretls.NewSession()

	s.Browser = azuretls.Chrome
	s.ModifyConfig = func(c *utls.Config) error {
		c.RootCAs = pool

		return nil
	}

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
		s:     s,
		proto: o.Proto,
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
	}

	if r.Body != nil {
		req.Body = r.Body
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

	return Response{
		Status:  resp.StatusCode,
		Proto:   protoOf(resp.Header.Get("X-Proto"), proto),
		Resumed: resumedHeader(resp.Header.Get("X-Tls-Resumed")),
		Bytes:   int64(len(resp.Body)),
	}, nil
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

func (c *azuretlsAdapter) Close() {
	c.s.Close()
}
