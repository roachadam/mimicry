// Package h2 is mimicry's HTTP/2 client, built on golang.org/x/net/http2's Framer
// and hpack. It sends the profile's SETTINGS, WINDOW_UPDATE and PRIORITY frames,
// pseudo-header order, HEADERS priority and header order, and implements flow
// control in both directions.
//
// Imports: profile, headerorder, golang.org/x/net and the standard library.
package h2
