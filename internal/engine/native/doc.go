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
// still goes on TCP and starts a QUIC connection beside it; requests after
// it is up take it. Firefox (on_alt_svc): the response carrying Alt-Svc
// starts the QUIC connection at once, and requests wait for it; once up,
// the origin's HTTP/2 connection gets a GOAWAY (goaway_tcp). Safari
// (new_connection): only a request finding no open connection to the
// origin dials QUIC. Once an origin has used HTTP/3, a request finding no
// QUIC connection (idled out, or after the server's GOAWAY) dials a new
// one, resumed with 0-RTT where the profile does, and goes on it at once;
// with race_after_goaway, after the server's GOAWAY the request goes on
// TCP and QUIC follows race_delay_ms later. A QUIC connection that fails
// leaves the origin on TCP for brokenFor. HTTP/3 connections are pooled
// apart (route key plus " h3"), never through a proxy, never coalesced;
// QUIC tickets, NEW_TOKEN tokens and the last smoothed RTT are kept per
// origin. A request kind the profile did not measure over HTTP/3
// (h3.Measured) stays on TCP.
//
// OpenWebSocket opens a WebSocket's transport: an extended CONNECT stream
// on the pool's HTTP/2 connection to the host when its server allows
// one, else an HTTP/1.1 connection of the socket's own, outside the pool,
// its TLS hello the profile's tls.websocket_h1.
//
// Imports: any internal package, profile, clockwork, utls,
// golang.org/x/net/http/httpguts and the standard library.
package native
