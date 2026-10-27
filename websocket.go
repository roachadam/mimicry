package mimicry

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/engine/native"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/internal/site"
	"github.com/roachadam/mimicry/internal/ws"
	"github.com/roachadam/mimicry/profile"
)

// MessageType is a WebSocket message's type: what a page's send() makes of
// a string (text) or an ArrayBuffer or Blob (binary).
type MessageType int

// Message types, as their opcodes (RFC 6455, section 5.2).
const (
	TextMessage   MessageType = 1
	BinaryMessage MessageType = 2
)

// WebSocketOptions are what a page passes to new WebSocket(), and its
// own headers.
type WebSocketOptions struct {
	// From is the page opening the socket: it decides Origin,
	// sec-fetch-site and which cookies go. Nil is the transport's last
	// navigation, or else the socket's own origin (as for other request
	// kinds).
	From *url.URL
	// Protocols are the subprotocols asked for (Sec-WebSocket-Protocol),
	// in order; none sends no header.
	Protocols []string
	// Header holds headers of the caller's own, added to the handshake
	// after the profile's (no page can set a WebSocket's headers). A key
	// with no values suppresses a profile default.
	Header http.Header
}

// WebSocket is an open WebSocket that speaks as the profile's browser:
// every frame masked with a fresh key and its length in the shortest
// form, messages compressed as the browser compresses them when the server
// accepted permessage-deflate, pings answered at once, and the close
// handshake and the end of the connection or stream as the browser does
// them. ReadMessage, WriteMessage and Close are safe for concurrent use.
type WebSocket struct {
	conn       *ws.Conn
	protocol   string
	extensions string
}

// CloseError is what ReadMessage returns once the socket has closed with
// a close handshake: the server's close frame (its code and reason, which
// the socket echoed) or its echo of the socket's own. Code is 1005 when
// the frame had none.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("mimicry: WebSocket closed: %d %q", e.Code, e.Reason)
}

// webSocketEngine opens WebSocket transports: the pure-Go engine.
type webSocketEngine interface {
	OpenWebSocket(req *http.Request, key string) (*native.WebSocket, error)
}

// DialWebSocket opens a WebSocket to rawURL (ws:// or wss://) through t, as
// the profile's browser opens one from opts.From. The handshake is the
// profile's websocket request kind: its headers in their order, Origin,
// sec-fetch-* and the jar's cookies as the browser sends them, and the
// browser's extension offer. A wss:// socket goes as an extended CONNECT
// stream (RFC 8441) on t's HTTP/2 connection to its host when there is one
// and its server allows it, else on an HTTP/1.1 connection of its own
// (never reused, its TLS hello offering only http/1.1), as every browser
// does; a ws:// socket always on a TCP connection of its own. A ws://
// socket from an https:// page is refused before anything is sent (mixed
// content), as Chrome refuses it.
//
// Through Config.Proxy a socket goes as the profile's browser takes one
// through a proxy (profile proxy.websocket): a wss:// stream on the HTTP/2
// connection in the host's tunnel, or a connection of its own in a tunnel
// of its own; a ws:// socket in a tunnel of its own or sent to an HTTP
// proxy whole. A proxy failure is a *ProxyError, and Config.Pins and
// VerifyConnection apply to the server's TLS inside the tunnel.
//
// The server's answer is returned too: on success its headers (the body
// is empty); when the handshake fails, with an error, its status and the
// start of its body. A profile without a websocket section, a proxy the
// profile's browser was not measured taking sockets through (no
// proxy.websocket; credentials without proxy.websocket.credentials; ws://
// through SOCKS5), or subprotocols where the profile's list has no place
// for them return ErrNotMeasured.
//
// NewClient's Transport is client.Transport.(*mimicry.Transport).
func (t *Transport) DialWebSocket(ctx context.Context, rawURL string, opts WebSocketOptions) (*WebSocket, *http.Response, error) {
	p := t.profile
	if p.WebSocket == nil {
		return nil, nil, fmt.Errorf("%w: profile %s has no websocket section", ErrNotMeasured, p.Name)
	}
	e, ok := t.engine.(webSocketEngine)
	if !ok {
		return nil, nil, errors.New("mimicry: the transport's engine opens no WebSockets")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, err
	}
	target, err := webSocketTarget(u)
	if err != nil {
		return nil, nil, err
	}
	from := opts.From
	if from == nil {
		from = t.lastNav.Load()
		if from == nil {
			from = &url.URL{
				Scheme: target.Scheme,
				Host:   target.Host,
				Path:   "/",
			}
		}
	}
	if from.Scheme == "https" && !site.Trustworthy(u) {
		return nil, nil, fmt.Errorf("mimicry: an insecure WebSocket connection (%s) may not be opened from a page loaded over HTTPS", u.Redacted())
	}
	if err := checkProtocols(opts.Protocols); err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range opts.Header {
		req.Header[k] = append([]string(nil), v...)
	}
	offer := p.WebSocket.Extensions
	req.Header.Set("Sec-WebSocket-Extensions", offer)
	if len(opts.Protocols) > 0 {
		req.Header.Set("Sec-WebSocket-Protocol", strings.Join(opts.Protocols, ", "))
	}
	s, err := resolve(p, req, Request{
		Kind: kindWebSocket,
		From: from,
	}, from, t.headers)
	if err != nil {
		return nil, nil, err
	}
	if len(opts.Protocols) > 0 && !placesProtocols(s.plan) {
		return nil, nil, fmt.Errorf("%w: profile %s has no WebSocket handshake with subprotocols for this page (site, non-secure)", ErrNotMeasured, p.Name)
	}
	h := s.header
	if t.jar != nil && s.credentialed && !hasHeader(h, "Cookie") {
		if line := cookies.Header(t.jar.Cookies(s.cookies)); line != "" {
			h.Set("Cookie", line)
		}
	}
	key, err := ws.NewKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	sctx := headerorder.WithPlan(pool.WithPartition(ctx, s.partition), s.plan)
	req = req.Clone(sctx)
	req.Header = h

	sock, err := e.OpenWebSocket(req, key)
	if err != nil {
		return nil, nil, publicError(err)
	}
	resp := sock.Response
	if t.jar != nil && s.credentialed {
		t.jar.SetCookies(s.cookies, resp.Header.Values("Set-Cookie"))
	}
	deflate, err := checkHandshake(sock, key, offer, opts.Protocols)
	if err != nil {
		if sock.Conn != nil {
			// Accepted, but not as asked: the body is the socket's.
			resp.Body = http.NoBody
			_ = sock.Conn.Close()
			return nil, resp, err
		}
		return nil, failedResponse(resp), err
	}
	// The body is the socket's from here on, which outlives ctx: its
	// pongs and close replies go out whatever ctx does.
	resp.Body = http.NoBody
	conn, err := ws.New(sock.Conn, sock.Reader, ws.Config{ //nolint:contextcheck // the socket outlives the dial's context
		Profile: p.WebSocket,
		Name:    p.Name,
		H2:      sock.H2,
		MaxData: sock.MaxData,
		Deflate: deflate,
		Clock:   t.clock,
		Rand:    rand.Reader,
	})
	if err != nil {
		// The browser ends the transport at once, sending nothing.
		_ = sock.Conn.Close()
		return nil, resp, err
	}
	return &WebSocket{
		conn:       conn,
		protocol:   resp.Header.Get("Sec-WebSocket-Protocol"),
		extensions: strings.Join(resp.Header.Values("Sec-WebSocket-Extensions"), ", "),
	}, resp, nil
}

// webSocketTarget checks a WebSocket URL as the WebSocket constructor
// does (ws or wss, no fragment) and returns the http or https URL its
// handshake requests, which cookies, sites and connections use.
func webSocketTarget(u *url.URL) (*url.URL, error) {
	target := *u
	switch u.Scheme {
	case "wss":
		target.Scheme = "https"
	case "ws":
		target.Scheme = "http"
	default:
		return nil, fmt.Errorf("mimicry: a WebSocket URL is ws:// or wss://, not %q", u.Scheme)
	}
	switch {
	case u.Host == "" || !httpguts.ValidHostHeader(u.Host):
		return nil, fmt.Errorf("mimicry: WebSocket URL %s has no valid host", u.Redacted())
	case u.Fragment != "" || u.RawFragment != "":
		return nil, fmt.Errorf("mimicry: WebSocket URL %s has a fragment", u.Redacted())
	case u.User != nil:
		return nil, fmt.Errorf("mimicry: WebSocket URL %s has credentials", u.Redacted())
	}
	return &target, nil
}

// checkProtocols checks subprotocols as the WebSocket constructor does:
// tokens (RFC 7230), none twice.
func checkProtocols(protocols []string) error {
	for i, pr := range protocols {
		if pr == "" || !httpguts.ValidHeaderFieldName(pr) {
			return fmt.Errorf("mimicry: WebSocket subprotocol %q is not a token", pr)
		}
		if slices.Contains(protocols[:i], pr) {
			return fmt.Errorf("mimicry: WebSocket subprotocol %q given twice", pr)
		}
	}
	return nil
}

// placesProtocols reports whether every list plan sends from has a place
// for Sec-WebSocket-Protocol.
func placesProtocols(plan *headerorder.Plan) bool {
	if plan == nil {
		return false
	}
	places := func(list []profile.Header) bool {
		return len(list) == 0 || slices.ContainsFunc(list, func(h profile.Header) bool {
			return strings.EqualFold(h.Name, "sec-websocket-protocol")
		})
	}
	return places(plan.Headers) && places(plan.HTTP1)
}

// checkHandshake checks the server's answer to a handshake with key,
// offer and protocols (RFC 6455, section 4.1; RFC 8441, section 5) and
// returns the permessage-deflate it accepted, if any.
func checkHandshake(sock *native.WebSocket, key, offer string, protocols []string) (*ws.Deflate, error) {
	resp := sock.Response
	if sock.H2 {
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("mimicry: WebSocket handshake answered %s", resp.Status)
		}
	} else {
		switch {
		case resp.StatusCode != http.StatusSwitchingProtocols:
			return nil, fmt.Errorf("mimicry: WebSocket handshake answered %s", resp.Status)
		case !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket"):
			return nil, errors.New("mimicry: WebSocket handshake answered without Upgrade: websocket")
		case !httpguts.HeaderValuesContainsToken(resp.Header.Values("Connection"), "upgrade"):
			return nil, errors.New("mimicry: WebSocket handshake answered without Connection: Upgrade")
		case resp.Header.Get("Sec-WebSocket-Accept") != ws.AcceptKey(key):
			return nil, errors.New("mimicry: WebSocket handshake answered with a wrong Sec-WebSocket-Accept")
		}
	}
	if got := resp.Header.Values("Sec-WebSocket-Protocol"); len(got) > 0 {
		if len(got) > 1 || !slices.Contains(protocols, got[0]) {
			return nil, fmt.Errorf("mimicry: WebSocket handshake answered subprotocol %q, not one asked for", strings.Join(got, ", "))
		}
	}
	return ws.ParseExtensions(offer, strings.Join(resp.Header.Values("Sec-WebSocket-Extensions"), ", "))
}

// failedBody is how much of a failed handshake's response body is kept.
const failedBody = 4 << 10

// failedResponse returns resp with the start of its body read into
// memory and the transport behind it closed.
func failedResponse(resp *http.Response) *http.Response {
	if resp.Body == nil || resp.Body == http.NoBody {
		return resp
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, failedBody))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return resp
}

// ReadMessage returns the next message the server sent. Once the socket
// has closed it returns a *CloseError after a close handshake, or the
// error that ended it.
func (w *WebSocket) ReadMessage(ctx context.Context) (MessageType, []byte, error) {
	m, err := w.conn.ReadMessage(ctx)
	if err != nil {
		return 0, nil, closeError(err)
	}
	return MessageType(m.Opcode), m.Payload, nil
}

// WriteMessage sends data as one message of type typ: in one frame, as
// every browser sends a message (Chrome sometimes fragments one of about
// 70 KiB; mimicry never does), compressed where the profile's browser
// compresses it. A text message must be UTF-8. On a socket that
// negotiated permessage-deflate, a message the profile's browser was never
// seen compressing returns ErrNotMeasured (any, for Firefox, whose
// compressor no setting of zlib reproduces) and is not sent.
func (w *WebSocket) WriteMessage(ctx context.Context, typ MessageType, data []byte) error {
	if typ != TextMessage && typ != BinaryMessage {
		return fmt.Errorf("mimicry: unknown WebSocket message type %d", typ)
	}
	return w.conn.WriteMessage(ctx, byte(typ), data)
}

// Close closes the socket with code (1000, or 3000 to 4999, as a page's
// close() allows) and reason (at most 123 bytes), and returns once the
// close handshake is done and the connection or stream has ended as the
// browser ends it: Chrome closes its HTTP/1.1 connection 2 s after the
// handshake unless the server did first, and sets END_STREAM on its
// close frame's HTTP/2 DATA, then waits for the server's END_STREAM (its
// close event comes once the stream has closed; mimicry resets a stream
// the server has not ended after 10 s, not measured); Safari closes the
// connection, or resets the stream (NO_ERROR), at once. Firefox ended
// neither in any capture: mimicry leaves it to the server, and ends it
// itself after 10 s (not measured), after Close has returned. A server
// that never answers the close has the transport ended after 10 s (not
// measured). After the server closed first (ReadMessage's *CloseError),
// Close only waits for that end.
func (w *WebSocket) Close(code int, reason string) error {
	return closeError(w.conn.Close(code, reason))
}

// Protocol is the subprotocol the server chose, or "".
func (w *WebSocket) Protocol() string {
	return w.protocol
}

// Extensions is the server's Sec-WebSocket-Extensions answer, its
// parameters included, or "". (A page's extensions attribute is this in
// Chrome and Safari; Firefox's names the extension alone.)
func (w *WebSocket) Extensions() string {
	return w.extensions
}

// closeError turns the socket's close error into a *CloseError.
func closeError(err error) error {
	var ce *ws.CloseError
	if errors.As(err, &ce) {
		return &CloseError{
			Code:   ce.Code,
			Reason: ce.Reason,
		}
	}
	return err
}
