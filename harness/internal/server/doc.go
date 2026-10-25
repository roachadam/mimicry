// Package server is the capture server. It records what a client sends: the raw
// ClientHello, HTTP/2 frames and header order, HTTP/1.1 request lines
// and header lines as sent, WebSocket frames (over HTTP/1.1 Upgrade and
// HTTP/2 extended CONNECT), and QUIC and HTTP/3 (every datagram, packet and
// frame, through quictrace), on the capture page and the scenario pages.
//
// Imports: capture, h2trace, pageshape, quictrace, mimicry's profile
// package, github.com/quic-go/quic-go, golang.org/x/net, golang.org/x/crypto
// and the standard library.
package server
