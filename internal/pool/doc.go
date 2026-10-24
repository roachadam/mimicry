// Package pool keeps connections by key (scheme, host and port, partition,
// proxy): per key, one shared connection (HTTP/2), the idle exclusive ones
// (HTTP/1.1), closed after an idle timeout or when found dead before reuse,
// the dial in progress, which concurrent requests wait for and share, its
// error included, and the requests waiting, first come first served, once
// a key has as many exclusive connections as Config.MaxExclusive allows
// (a browser's per-host limit). A key may adopt another key's shared
// connection (HTTP/2 coalescing; the caller decides when). A key with no
// connections is forgotten after the idle timeout. The pool never holds
// its lock across network I/O. Connections are given to it, through
// interfaces; it does not dial.
//
// Imports: clockwork and the standard library.
package pool
