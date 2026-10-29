package clients

import (
	"bytes"
	"context"
	"io"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	tlsprofiles "github.com/bogdanfinn/tls-client/profiles"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// tlsClient is github.com/bogdanfinn/tls-client with Chrome_152_PSK, its
// newest Chrome profile in the variant that resumes TLS sessions as Chrome
// does (plain Chrome_152 has no session cache, so it never resumes).
// HTTP/1.1 is forced with WithForceHttp1, HTTP/2 is ALPN's choice with
// WithDisableHttp3, and HTTP/3 has no switch of its own: WithProtocolRacing
// races QUIC against TCP per host and keeps the winner, so callers warm up
// until responses come over HTTP/3; through a SOCKS5 proxy its QUIC goes
// over a UDP association (and it refuses racing through any other proxy).
// Rotation is its SetProxy, which builds a new transport. Headers:
// Chrome's navigation set, as its users pass them.
func tlsClient() Factory {
	return Factory{
		Name:    "tls-client",
		Module:  "github.com/bogdanfinn/tls-client",
		Profile: "Chrome_152_PSK",
		New:     newTLSClient,
	}
}

type tlsClientAdapter struct {
	client tlsclient.HttpClient
}

func newTLSClient(o Options) (Client, error) {
	opts := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(tlsprofiles.Chrome_152_PSK),
		tlsclient.WithRandomTLSExtensionOrder(),
		tlsclient.WithTimeoutSeconds(int(requestTimeout.Seconds())),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithTransportOptions(&tlsclient.TransportOptions{
			RootCAs:             o.RootCAs,
			MaxIdleConnsPerHost: 1024,
		}),
	}

	switch o.Proto {
	case target.H1:
		opts = append(opts, tlsclient.WithForceHttp1())
	case target.H2:
		opts = append(opts, tlsclient.WithDisableHttp3())
	case target.H3:
		opts = append(opts, tlsclient.WithProtocolRacing())
	}

	if o.Proxy != "" {
		opts = append(opts, tlsclient.WithProxyUrl(o.Proxy))
	}

	c, err := tlsclient.NewHttpClient(nil, opts...)
	if err != nil {
		return nil, err
	}

	return &tlsClientAdapter{
		client: c,
	}, nil
}

func (c *tlsClientAdapter) Do(ctx context.Context, r *Request) (Response, error) {
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}

	req, err := fhttp.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return Response{}, err
	}

	order := make([]string, 0, len(chromeHeaders)+1)
	for _, h := range chromeHeaders {
		req.Header[h[0]] = []string{h[1]}
		order = append(order, h[0])
	}

	if r.ContentType != "" {
		req.Header["content-type"] = []string{r.ContentType}

		order = append(order, "content-type")
	}

	req.Header[fhttp.HeaderOrderKey] = order

	resp, err := c.client.Do(req)
	if err != nil {
		return Response{}, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	n, err := drain(resp.Body, r.Arrivals)

	return Response{
		Status:  resp.StatusCode,
		Proto:   protoOf(resp.Header.Get("X-Proto"), resp.Proto),
		Resumed: resumedHeader(resp.Header.Get("X-Tls-Resumed")),
		Bytes:   n,
	}, err
}

func (c *tlsClientAdapter) CloseIdle() {
	c.client.CloseIdleConnections()
}

func (c *tlsClientAdapter) SetProxy(proxy string) error {
	return c.client.SetProxy(proxy)
}

func (c *tlsClientAdapter) Close() {
	c.client.CloseIdleConnections()
}
