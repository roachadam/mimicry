// Package server is the capture server. It records what a client sends: the raw
// ClientHello, HTTP/2 frames and header order, and later the HTTP/3 handshake.
//
// Imports: capture, golang.org/x/net and the standard library.
package server
