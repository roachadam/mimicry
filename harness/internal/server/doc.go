// Package server is the capture server. It records what a client sends: the raw
// ClientHello, HTTP/2 frames and header order, and HTTP/1.1 request lines
// and header lines as sent, on the capture page and the scenario pages.
//
// Imports: capture, h2trace, pageshape, mimicry's profile package,
// golang.org/x/net, golang.org/x/crypto and the standard library.
package server
