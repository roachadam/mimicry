// Package native is the pure-Go engine. It is the one place that assembles the
// request path: pool, dial, handshake, then h1 or h2 by ALPN, or h3 over
// QUIC. It routes each request to its pool key (scheme, host and port,
// partition, proxy), dials and hands connections to internal/pool, which
// keeps them, shares dials and caps a host's HTTP/1.1 connections at the
// profile's number, and answers a proxy's credential challenge on
// plain-HTTP requests. A request to a host with no HTTP/2 connection of
// its own takes another host's as the profile's coalescing rule says
// (connections.coalesce): one whose certificate covers the host, in the
// same partition and port, not through a proxy, at an address the host
// resolves to (lookup) or the address a new connection reached, which is
// then declined (connect). A request nothing of which was sent goes again
// on another connection.
//
// HTTP/3 (a profile with http3 and quic sections, unless disabled): every
// response's Alt-Svc h3 on the same host is kept per route key, and the
// origin moves to HTTP/3 as the profile's http3.discovery says. Chrome
// (next_request): the first request to an origin with a known Alt-Svc
// still goes on TCP and starts a QUIC attempt beside it; requests after
// it is up take it. Firefox (on_alt_svc): the response carrying Alt-Svc
// starts the attempt at once, in the background; once up, the origin's
// HTTP/2 connections get a GOAWAY (goaway_tcp), each once the request
// that opened it has its response, one still being established then
// too. Safari (new_connection): only a request finding no open
// connection to the origin dials QUIC. Once an origin has used HTTP/3, a
// request finding no QUIC connection dials a new one, resumed with 0-RTT
// where the profile does; with race_after_goaway, after the server's
// GOAWAY the request goes on TCP and QUIC follows race_delay_ms later,
// never before the request has its response on TCP.
//
// While no QUIC connection is up, http3.discovery.fallback decides
// (attempt, quicFor): a request racing an attempt (race: Chrome, Safari)
// waits for it tcp_delay_ms, then goes on TCP, Safari dropping the
// attempt; Chrome's request sent in 0-RTT waits for that attempt's give-up
// instead, then a fresh attempt runs beside TCP; a background attempt
// (Firefox) holds no request, but those made while its resumed attempt
// after the server's close is pending (hold). Failed attempts in a row
// (broken_after, resumed ones counted where broken_counts_resumed) leave
// the origin on TCP for the engine's life; after a failed resumed attempt
// or a dead connection Firefox validates afresh, beside TCP, with a
// request revalidate_ms later. Requests pending on a connection that died
// (its idle timeout, or its path: quic.loss.path_degrading) go on TCP
// (pending tcp: one TCP connection opened per request, all sent on the
// first) or on a new QUIC connection reconnect_ms later (pending quic).
// HTTP/3 connections are pooled apart (route key plus " h3"), never
// through a proxy, never coalesced; QUIC tickets, NEW_TOKEN tokens and the
// last smoothed RTT are kept per origin. A request kind the profile did
// not measure over HTTP/3 (h3.Measured) stays on TCP.
//
// OpenWebSocket opens a WebSocket's transport: an extended CONNECT stream
// on the pool's HTTP/2 connection to the host when its server allows
// one, else an HTTP/1.1 connection of the socket's own, outside the pool,
// its TLS hello the profile's tls.websocket_h1. Through a proxy the
// stream is on the connection in the route's tunnel, and the socket's own
// connection gets a tunnel of its own, or (proxy.websocket.plain forward)
// its ws:// handshake goes to the HTTP proxy whole. TLS connections
// through an HTTP proxy's tunnel resume nothing where
// proxy.connect_no_resume says so.
//
// Config.VerifyConnection (the root's pins and its own check) runs in
// every TLS and QUIC handshake's verification, resumed ones included, and
// before a request takes another host's HTTP/2 connection; responses
// carry their connection's TLS state. Handshake connects and verifies
// without a request (FetchPins).
//
// Imports: any internal package, profile, clockwork,
// golang.org/x/net/http/httpguts and the standard library (utls only in
// tests, for the QUIC test server).
package native
