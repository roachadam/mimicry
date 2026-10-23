// Package native is the pure-Go engine. It is the one place that assembles the
// request path: pool, dial, handshake, then h1 or h2 by ALPN. Its pool lock is
// never held across network I/O, and concurrent requests for one host share a
// dial.
//
// Imports: any internal package and profile.
package native
