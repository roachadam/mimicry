package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/dial"
	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/ws"
	"github.com/roachadam/mimicry/profile"
)

// WebSocket is the transport a WebSocket handshake opened, and the
// server's answer.
type WebSocket struct {
	// Response is the server's answer to the handshake. Its body is
	// readable only when the handshake failed (not 101, or not 2xx over
	// HTTP/2); closing it ends the transport.
	Response *http.Response
	// Conn carries the socket once the handshake succeeded, and Reader
	// reads what the server sent on it (which may have come with the
	// handshake's response).
	Conn   ws.Transport
	Reader io.Reader
	// H2 is set for an extended CONNECT stream (RFC 8441), whose DATA
	// frames carry at most MaxData bytes.
	H2      bool
	MaxData int
}

// OpenWebSocket sends req, a WebSocket handshake to an https or http URL
// with its headers resolved, as the profile's browser opens a socket: as
// an extended CONNECT stream on the pool's HTTP/2 connection to the host
// when it has one whose server allows it (SETTINGS_ENABLE_CONNECT_PROTOCOL),
// else on a new HTTP/1.1 connection of its own, never pooled, which for
// https says the hello of tls.websocket_h1 (ALPN http/1.1). key is the
// Sec-WebSocket-Key, which only HTTP/1.1 sends. Through a proxy, the
// HTTP/2 connection is the one in the route's tunnel, and a connection of
// its own goes through a tunnel of its own, or for ws:// is sent to an
// HTTP proxy whole, as proxy.websocket says.
func (e *Engine) OpenWebSocket(req *http.Request, key string) (*WebSocket, error) {
	ctx := req.Context()
	if req.URL == nil || req.URL.Host == "" || !httpguts.ValidHostHeader(req.URL.Host) {
		return nil, errors.New("mimicry: WebSocket request has no valid host")
	}
	if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
		return nil, fmt.Errorf("mimicry: unsupported WebSocket transport scheme %q", req.URL.Scheme)
	}
	r, err := e.route(req)
	if err != nil {
		return nil, err
	}
	if r.proxy != nil {
		if r, err = e.webSocketRoute(req, r); err != nil {
			return nil, err
		}
	}
	if req.URL.Scheme == "https" {
		if c, ok := e.pool.Shared(r.key); ok && c.ConnectProtocol() {
			resp, st, err := c.Connect(ctx, req, "websocket") //nolint:bodyclose // the body is the socket's stream: the caller's
			if err == nil {
				resp.TLS = c.tls
			}
			switch {
			case err == nil && st == nil:
				// Refused: the body, still bound to ctx, is the answer.
				return &WebSocket{
					Response: resp,
					H2:       true,
				}, nil
			case err == nil:
				return &WebSocket{
					Response: resp,
					Conn:     st,
					Reader:   st,
					H2:       true,
					MaxData:  st.MaxData(),
				}, nil
			case !errors.Is(err, h2.ErrConnUnusable):
				return nil, err
			}
			// Going away before the stream opened: a connection of its
			// own, as when the host has none.
			e.pool.RemoveShared(r.key, c)
		}
	}
	return e.openWebSocketH1(ctx, req, r, key)
}

// webSocketRoute checks that the profile's browser was measured taking
// req, a WebSocket handshake, through r's proxy, and returns r as the
// socket goes: a ws:// socket through an HTTP proxy is sent to it whole
// (r.forward) only where proxy.websocket.plain says so, and tunnelled
// otherwise.
func (e *Engine) webSocketRoute(req *http.Request, r route) (route, error) {
	w := e.profile.Proxy.WebSocket
	switch {
	case w == nil:
		return r, fmt.Errorf("%w: profile %s has no WebSocket through a proxy (proxy.websocket)", headerorder.ErrNotMeasured, e.profile.Name)
	case r.proxy.User != nil && !w.Credentials:
		return r, fmt.Errorf("%w: profile %s has no WebSocket through a proxy with credentials", headerorder.ErrNotMeasured, e.profile.Name)
	case req.URL.Scheme == "http" && r.proxy.Scheme != dial.SchemeHTTP:
		return r, fmt.Errorf("%w: no browser was measured opening a ws:// socket through a SOCKS5 proxy", headerorder.ErrNotMeasured)
	}
	r.forward = r.forward && w.Plain == profile.PlainWebSocketForward
	return r, nil
}

// openWebSocketH1 sends req on a new HTTP/1.1 connection of its own: to
// the server, through a tunnel of its own, or to an HTTP proxy whole
// (r.forward), carrying the proxy's credentials as the browser's other
// requests sent whole do, where a 407 is the proxy refusing the socket.
func (e *Engine) openWebSocketH1(ctx context.Context, req *http.Request, r route, key string) (*WebSocket, error) {
	conn, err := e.dialWebSocket(ctx, req, r)
	if err != nil {
		return nil, err
	}
	var upgraded atomic.Bool
	c := e.newH1(conn, r)
	req = req.Clone(ctx)
	req.Header.Set("Sec-WebSocket-Key", key)
	if r.forward {
		// The request line names the socket's own URL.
		u := *req.URL
		u.Scheme = "ws"
		req.URL = &u
		if auth := e.dialer.ForwardAuthorization(r.proxy); auth != "" {
			req.Header.Set("Proxy-Authorization", auth)
		}
	}
	resp, err := c.RoundTrip(req, func(bool) {
		// Nothing else ever uses the connection.
		if !upgraded.Load() {
			_ = conn.Close()
		}
	})
	if err != nil {
		return nil, err
	}
	if r.forward && resp.StatusCode == http.StatusProxyAuthRequired {
		// The proxy refused the socket; the server never saw it.
		_ = resp.Body.Close()
		return nil, &dial.ProxyError{
			Proxy:      r.proxy,
			StatusCode: resp.StatusCode,
			Header:     resp.Header,
			Err:        fmt.Errorf("answered the WebSocket handshake with %s", resp.Status),
		}
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return &WebSocket{
			Response: resp,
		}, nil
	}
	upgraded.Store(true)
	// The 101 has no body: closing it stops the request's context from
	// closing the connection, which is the socket's from here on.
	_ = resp.Body.Close()
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	nc, br := c.Hijack()
	return &WebSocket{
		Response: resp,
		Conn: &h1Socket{
			conn: nc,
		},
		Reader: br,
	}, nil
}

// dialWebSocket connects for a WebSocket of its own (through its own
// tunnel, or to the proxy itself for r.forward) and, for https, says the
// profile's WebSocket hello (tls.websocket_h1), resuming as the profile's
// other connections on the route do. The server must not pick h2, which
// the hello does not offer.
func (e *Engine) dialWebSocket(ctx context.Context, req *http.Request, r route) (net.Conn, error) {
	var conn net.Conn
	var err error
	switch {
	case r.proxy == nil:
		conn, err = e.dialer.Dial(ctx, r.addr)
	case r.forward:
		conn, err = e.dialer.DialProxy(ctx, r.proxy)
	default:
		conn, err = e.dialer.DialVia(ctx, r.proxy, r.addr)
	}
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme == "http" {
		return conn, nil
	}
	if e.profile.TLS.WebSocketH1 == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: profile %s has no WebSocket hello (tls.websocket_h1)", headerorder.ErrNotMeasured, e.profile.Name)
	}
	cfg := handshake.Config{
		ServerName:       req.URL.Hostname(),
		RootCAs:          e.rootCAs,
		VerifyConnection: e.verifyFor(req.URL.Hostname()),
	}
	hello := e.plan.Hello()
	if e.resumes(r) {
		cfg.Tickets, cfg.TicketKey = e.tickets, r.key
		if cfg.Ticket = e.tickets.Take(cfg.TicketKey); cfg.Ticket != nil && cfg.Ticket.TLS13() {
			hello = e.plan.ResumedHello()
		}
	}
	hctx, cancel := context.WithTimeout(ctx, e.dialer.Timeout)
	defer cancel()
	tc, proto, err := handshake.Client(hctx, conn, e.plan.WebSocketHello(hello), cfg)
	if err != nil {
		return nil, err
	}
	if proto == "h2" {
		_ = tc.Close()
		return nil, errors.New("mimicry: the server chose h2 for a WebSocket hello offering only HTTP/1.1")
	}
	return tc, nil
}

// h1Socket is a WebSocket's HTTP/1.1 connection (ws.Transport).
type h1Socket struct {
	conn net.Conn
	buf  bytes.Buffer
}

// Read reads from the connection; the socket reads through the buffered
// reader the handshake left instead.
func (s *h1Socket) Read(p []byte) (int, error) {
	return s.conn.Read(p)
}

// Send writes the frame's parts in one write, within ctx: a write ctx
// interrupts leaves the connection unusable, and ws closes it.
func (s *h1Socket) Send(ctx context.Context, parts [][]byte, _ bool) error {
	s.buf.Reset()
	for _, p := range parts {
		s.buf.Write(p)
	}
	if d, ok := ctx.Deadline(); ok {
		_ = s.conn.SetWriteDeadline(d)
		defer func() {
			_ = s.conn.SetWriteDeadline(time.Time{})
		}()
	}
	stop := context.AfterFunc(ctx, func() {
		_ = s.conn.SetWriteDeadline(time.Unix(1, 0))
	})
	defer stop()
	_, err := s.conn.Write(s.buf.Bytes())
	if ctxErr := ctx.Err(); err != nil && ctxErr != nil {
		return ctxErr
	}
	return err
}

// Close closes the TCP connection.
func (s *h1Socket) Close() error {
	return s.conn.Close()
}

// Release closes the connection the server closed.
func (s *h1Socket) Release() {
	_ = s.conn.Close()
}
