// Package pool keeps connections per host. It never holds a lock across network
// I/O, and concurrent requests for the same host share one dial.
//
// Imports: the standard library only; dialing is injected.
package pool
