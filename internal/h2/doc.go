// Package h2 is mimicry's HTTP/2 client, built on golang.org/x/net/http2's
// Framer and HPACK decoder, with header blocks encoded by internal/hpack. It
// reproduces the profile's connection preface (SETTINGS, WINDOW_UPDATE,
// PRIORITY), first stream ID, per-stream WINDOW_UPDATE, HEADERS priority
// (optionally chained as Chromium does), pseudo-header and header order,
// cookie splitting, DATA frame cap and flow-control credit thresholds. A
// fully read response is never reset: it waits for the peer's END_STREAM.
//
// Imports: profile, headerorder, hpack, golang.org/x/net and the standard
// library.
package h2
