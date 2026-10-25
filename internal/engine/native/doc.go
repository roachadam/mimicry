// Package native is the pure-Go engine. It is the one place that assembles the
// request path: pool, dial, handshake, then h1 or h2 by ALPN. It routes each
// request to its pool key (scheme, host and port, partition, proxy), dials
// and hands connections to internal/pool, which keeps them, shares dials
// and caps a host's HTTP/1.1 connections at the profile's number, and
// answers a proxy's credential challenge on plain-HTTP requests. A request
// to a host with no HTTP/2 connection of its own takes another host's as
// the profile's coalescing rule says (connections.coalesce): one whose
// certificate covers the host, in the same partition and port, not through
// a proxy, at an address the host resolves to (lookup) or the address a
// new connection reached, which is then declined (connect). A request
// nothing of which was sent goes again on another connection.
//
// OpenWebSocket opens a WebSocket's transport: an extended CONNECT stream
// on the pool's HTTP/2 connection to the host when its server allows
// one, else an HTTP/1.1 connection of the socket's own, outside the pool,
// its TLS hello the profile's tls.websocket_h1.
//
// Imports: any internal package, profile, clockwork, utls,
// golang.org/x/net/http/httpguts and the standard library.
package native
