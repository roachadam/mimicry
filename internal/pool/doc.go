// Package pool keeps connections by key (scheme, host and port, partition,
// proxy): per key, one shared connection (HTTP/2), the idle exclusive ones
// (HTTP/1.1), closed after an idle timeout or when found dead before reuse,
// and the dial in progress, which concurrent requests wait for and share,
// its error included. A key with no connections is forgotten after the idle
// timeout. The pool never holds its lock across network I/O. Connections are
// given to it, through interfaces; it does not dial.
//
// Imports: clockwork and the standard library.
package pool
