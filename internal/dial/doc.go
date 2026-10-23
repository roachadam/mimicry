// Package dial is the single path from an address to a connected socket: DNS,
// then an optional proxy, then TCP. Every step honours the context and its
// deadline.
//
// Imports: dns, proxy and the standard library.
package dial
