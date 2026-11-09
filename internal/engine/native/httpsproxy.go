package native

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"

	"github.com/roachadam/mimicry/internal/dial"
	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/internal/proxy"
	"github.com/roachadam/mimicry/profile"
)

// An https:// proxy is an HTTP proxy reached over TLS (decision 0065):
// the profile's ClientHello to the proxy's host, then CONNECTs over the
// protocol the proxy picked. Over HTTP/2 every tunnel is a stream of one
// connection per proxy URL (proxyConns), as browsers multiplex them; over
// HTTP/1.1 each tunnel takes a TLS connection of its own, as through an
// http:// proxy. The credential flow is the same either way
// (dial.Dialer.ConnectOn, TunnelH2).

// proxyKey is the pool key of the connections to the https proxy whose
// URL is via: "proxy via <url>", which no route's key can be (those start
// with a scheme). ticketKey makes it the key of their TLS sessions,
// "proxy via <hash>": kept per proxy URL as the routes' through it are,
// and saved with neither the proxy's address nor its credentials.
// [INFERENCE] Not partitioned: only the proxy sees these connections, and
// it sees the same client either way.
func proxyKey(via string) string {
	return proxyKeyPrefix + via
}

// proxyConn is an HTTP/2 connection to an https proxy, its tunnels the
// streams.
type proxyConn struct {
	*h2.Conn
	// goAway: Expire sends GOAWAY before closing (Engine.expireGoAway).
	goAway bool
}

// Expire closes the idle connection as the profile's browser lets go of
// an HTTP/2 connection to a site (shared.Expire). [INFERENCE] A proxy's
// idle connection was not captured ending.
func (c *proxyConn) Expire() {
	if c.goAway {
		c.GoAway()

		return
	}

	_ = c.Close()
}

// dialTunnel connects to r.addr through r's proxy: an http proxy's
// CONNECT tunnel or a SOCKS5 connection (dial.Dialer.DialVia), or an
// https proxy's tunnel (tunnelHTTPS).
func (e *Engine) dialTunnel(ctx context.Context, r route) (net.Conn, error) {
	if r.proxy.Scheme == dial.SchemeHTTPS {
		return e.tunnelHTTPS(ctx, r)
	}

	return e.dialer.DialVia(ctx, r.proxy, r.addr)
}

// tunnelHTTPS opens a tunnel to r.addr through r's https proxy, bounded by
// the dial timeout: a stream of the proxy's HTTP/2 connection when it has
// one, else on a new connection to the proxy, which becomes that
// connection if the proxy picks h2 (others then wait for it, as for a
// site's), or carries one CONNECT over HTTP/1.1. A connection going away
// before its stream opened (h2.ErrConnUnusable) leaves the pool and the
// tunnel opens on another. A failure is a *dial.ProxyError.
func (e *Engine) tunnelHTTPS(ctx context.Context, r route) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, e.dialer.Timeout)
	defer cancel()

	for attempt := 1; ; attempt++ {
		got, err := e.proxyConns.Get(ctx, r.proxyKey)
		if err != nil {
			return nil, asProxyError(r.proxy, err)
		}

		if got.Found != pool.FoundShared {
			// FoundDial: the pool keeps no idle connection to a proxy
			// (FoundExclusive).
			return e.dialHTTPSProxy(ctx, r, got.Dial)
		}

		t, err := e.dialer.TunnelH2(ctx, r.proxy, r.addr, e.connectStream(got.Shared, r.addr))
		if errors.Is(err, h2.ErrConnUnusable) && attempt < maxAttempts {
			e.proxyConns.RemoveShared(r.proxyKey, got.Shared)

			continue
		}

		return t, err
	}
}

// dialHTTPSProxy makes the connection to r's https proxy that d asked for
// and opens the tunnel on it.
func (e *Engine) dialHTTPSProxy(ctx context.Context, r route, d *pool.Dial[*proxyConn, noExclusive]) (net.Conn, error) {
	conn, err := e.dialer.DialProxy(ctx, r.proxy)
	if err != nil {
		d.Fail(err)

		return nil, err
	}

	if cs := handshake.ConnState(conn); cs == nil || cs.NegotiatedProtocol != "h2" {
		// The connection is the tunnel's: the proxy's requests dial
		// connections of their own from now on.
		d.Exclusive()
		e.proxyConns.Release(r.proxyKey, noExclusive{}, false)

		return e.dialer.ConnectOn(ctx, conn, r.proxy, r.addr)
	}

	hc, err := h2.New(conn, e.profile, e.clock)
	if err != nil {
		_ = conn.Close()
		err = asProxyError(r.proxy, err)

		d.Fail(err)

		return nil, err
	}

	c := d.Shared(&proxyConn{
		Conn:   hc,
		goAway: e.expireGoAway,
	})

	return e.dialer.TunnelH2(ctx, r.proxy, r.addr, e.connectStream(c, r.addr))
}

// connectStream sends CONNECTs to addr as streams of c, as the profile's
// browser: at proxy.https's weight, the tunnel's stream reset with its
// code when the tunnel closes. A refusal's body is read to its end, so
// the stream ends as the proxy ended it.
func (e *Engine) connectStream(c *proxyConn, addr string) dial.ConnectStream {
	h := e.profile.Proxy.HTTPS

	return func(ctx context.Context, header []profile.Header) (proxy.Answer, net.Conn, error) {
		resp, t, err := c.Tunnel(ctx, addr, header, h.Weight, h.Reset)
		if err != nil {
			return proxy.Answer{}, nil, err
		}

		if t == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
		}

		return proxy.Answer{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Header:     resp.Header,
		}, t, nil
	}
}

// proxyHandshake is the TLS handshake with the https proxy p on conn
// (dial.Dialer.ProxyTLS): the profile's ClientHello, server_name p's host
// (the trailing-dot rule as for a site), verified against Config.RootCAs
// and Config.VerifyConnection (Pins with it) for that host, its secrets
// to Config.KeyLogWriter; resumed with a session from an earlier
// connection to p where proxy.https.resume says so. No client
// certificate: a proxy asking gets none, as from a browser with none
// (a browser's answer to a proxy's request was not measured).
func (e *Engine) proxyHandshake(ctx context.Context, conn net.Conn, p *url.URL) (net.Conn, error) {
	host := p.Hostname()

	omit, err := e.omitSNI(host)
	if err != nil {
		_ = conn.Close()

		return nil, err
	}

	cfg := handshake.Config{
		ServerName:       host,
		OmitSNI:          omit,
		RootCAs:          e.rootCAs,
		VerifyConnection: e.verifyFor(host),
		KeyLogWriter:     e.keyLog,
	}
	hello := e.plan.Hello()

	if e.tickets != nil && e.profile.Proxy.HTTPS.Resume {
		cfg.Tickets, cfg.TicketKey = e.tickets, ticketKey(proxyKey(p.String()))
		if cfg.Ticket = e.tickets.Take(cfg.TicketKey); cfg.Ticket != nil && cfg.Ticket.TLS13() {
			// A TLS 1.2 ticket goes in the full hello's session_ticket.
			hello = e.plan.ResumedHello()
		}
	}

	tc, _, err := handshake.Client(ctx, conn, hello, cfg)

	return tc, err
}

// asProxyError is err, a failure on the way to the proxy p, as a
// *dial.ProxyError, unless it is one.
func asProxyError(p *url.URL, err error) error {
	if _, ok := errors.AsType[*dial.ProxyError](err); ok {
		return err
	}

	return &dial.ProxyError{
		Proxy: p,
		Err:   err,
	}
}
