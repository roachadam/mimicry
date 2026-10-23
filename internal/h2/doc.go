// Package h2 is mimicry's HTTP/2 client, built on golang.org/x/net/http2's Framer
// and hpack. It sends the profile's SETTINGS, WINDOW_UPDATE and PRIORITY frames
// and pseudo-header and header order, and detects dead connections.
//
// Imports: profile, golang.org/x/net and the standard library.
package h2
