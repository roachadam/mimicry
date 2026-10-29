package clients

import (
	"bytes"
	"context"
	"crypto/tls"

	"github.com/sardanioss/httpcloak"
	"github.com/sardanioss/httpcloak/fingerprint"

	"github.com/roachadam/mimicry/internal/bench/target"
)

// httpcloakClient is github.com/sardanioss/httpcloak's Session with the
// chrome-latest preset, each protocol forced with its own option
// (WithForceHTTP1/2/3). Session.Do returns the body buffered. ECH lookups
// are off (WithDisableECH): the benchmark's names have no DNS HTTPS
// records, and its HTTP/3 dials would otherwise query public resolvers.
// A socks5 proxy carries its HTTP/3 too (WithSessionProxy is its TCP and
// UDP proxy at once, the UDP over an association); rotation is its
// SetProxy.
func httpcloakClient() Factory {
	return Factory{
		Name:    "httpcloak",
		Module:  "github.com/sardanioss/httpcloak",
		Profile: httpcloakPreset(),
		New:     newHTTPCloak,
	}
}

func httpcloakPreset() string {
	if p := fingerprint.Get("chrome-latest"); p != nil {
		return "chrome-latest (" + p.Name + ")"
	}

	return "chrome-latest"
}

type httpcloakAdapter struct {
	s *httpcloak.Session
}

func newHTTPCloak(o Options) (Client, error) {
	opts := []httpcloak.SessionOption{
		httpcloak.WithTLSConfig(&tls.Config{
			RootCAs:    o.RootCAs,
			MinVersion: tls.VersionTLS12,
		}),
		httpcloak.WithDisableECH(),
		httpcloak.WithoutRedirects(),
	}

	switch o.Proto {
	case target.H1:
		opts = append(opts, httpcloak.WithForceHTTP1())
	case target.H2:
		opts = append(opts, httpcloak.WithForceHTTP2())
	case target.H3:
		opts = append(opts, httpcloak.WithForceHTTP3())
	}

	if o.Proxy != "" {
		opts = append(opts, httpcloak.WithSessionProxy(o.Proxy))
	}

	return &httpcloakAdapter{
		s: httpcloak.NewSession("chrome-latest", opts...),
	}, nil
}

func (c *httpcloakAdapter) Do(ctx context.Context, r *Request) (Response, error) {
	req := &httpcloak.Request{
		Method: r.Method,
		URL:    r.URL,
	}

	if r.Body != nil {
		req.Body = bytes.NewReader(r.Body)
	}

	if r.ContentType != "" {
		req.Headers = map[string][]string{
			"Content-Type": {r.ContentType},
		}
	}

	resp, err := c.s.Do(ctx, req)
	if err != nil {
		return Response{}, err
	}

	defer func() {
		_ = resp.Close()
	}()

	n, err := drain(resp.Body, r.Arrivals)

	return Response{
		Status:  resp.StatusCode,
		Proto:   protoOf(first(resp.Headers["x-proto"]), resp.Protocol),
		Resumed: resumedHeader(first(resp.Headers["x-tls-resumed"])),
		Bytes:   n,
	}, err
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}

	return v[0]
}

func (c *httpcloakAdapter) CloseIdle() {
	c.s.Refresh()
}

func (c *httpcloakAdapter) SetProxy(proxy string) error {
	c.s.SetProxy(proxy)

	return nil
}

func (c *httpcloakAdapter) Close() {
	c.s.Close()
}
