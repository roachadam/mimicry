// Package h2 is mimicry's HTTP/2 client, built on golang.org/x/net/http2's
// Framer and HPACK decoder, with header blocks encoded by internal/hpack. It
// reproduces the profile's connection preface (SETTINGS, WINDOW_UPDATE,
// PRIORITY), first stream ID, per-stream WINDOW_UPDATE, HEADERS priority
// (optionally chained as Chromium does), pseudo-header and header order,
// cookie splitting, DATA frame cap (END_STREAM on the last frame of a body
// of known length) and flow-control credit thresholds. A fully read
// response is never reset: it waits for the peer's END_STREAM. Connect
// opens an extended CONNECT stream (RFC 8441, :protocol after the
// profile's pseudo-headers) when the peer's SETTINGS_ENABLE_CONNECT_PROTOCOL
// allows it, a byte stream in DATA frames that its caller cuts, ends with
// END_STREAM or resets, or forgets once the peer ended it.
//
// The peer is held to what the profile advertises: receive windows, frame
// size and header list size. A request the peer cannot have processed
// fails with ErrConnUnusable, its body unread; after GOAWAY the streams the
// peer will answer run on and the connection closes after the last.
// Decline sends a connection the preface and GOAWAY and closes it unused,
// as Firefox does to one another connection made unnecessary; GoAway
// sends a live connection GOAWAY and lets its streams finish, as Firefox
// does once the origin's HTTP/3 connection is up.
//
// Head builds a request's header list with the code Conn.RoundTrip
// writes it with, without a connection (the engine's plans).
//
// Imports: profile, headerorder, hpack, golang.org/x/net and the standard
// library.
package h2
