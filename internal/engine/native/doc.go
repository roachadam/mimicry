// Package native is the pure-Go engine. It is the one place that assembles the
// request path: pool, dial, handshake, then h1 or h2 by ALPN. It routes each
// request to its pool key (scheme, host and port, partition, proxy), dials
// and hands connections to internal/pool, which keeps them and shares dials,
// and answers a proxy's credential challenge on plain-HTTP requests. A
// request nothing of which was sent goes again on another connection.
//
// Imports: any internal package, profile, clockwork,
// golang.org/x/net/http/httpguts and the standard library.
package native
