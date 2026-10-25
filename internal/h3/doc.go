// Package h3 is mimicry's HTTP/3 client (RFC 9114) on internal/quic, with
// field sections encoded by internal/qpack. It reproduces the profile's
// HTTP/3 (profile.HTTP3): the control stream's SETTINGS, GREASE setting
// and GREASE frame; which unidirectional streams it opens and when (with
// the client's Finished or after it, in 0-RTT before or after the first
// request, the QPACK encoder stream on its first insert); the QPACK
// encoder's use of the dynamic table and its Huffman rule, and whether a
// request's inserts go before or after its HEADERS; PRIORITY_UPDATE before
// each request with a priority header; header lists as HTTP/2 sends them,
// changed as HTTP3.Headers says (pseudo-header order, headers dropped,
// alt-used placed by kind); and request bodies in DATA frames as the
// browser cuts them. Each request's frames leave in one flight, in the
// browser's order (quic.Conn.Cork); where HTTP3.RequestsAfterConfirmed
// says so (Safari), a full handshake's requests wait until it is
// confirmed (quic.Conn.Confirmed).
//
// The server is held to RFC 9114: SETTINGS first on its control stream,
// no push, no bidirectional streams, one stream of each critical type.
// Its GOAWAY stops new requests; a request it did not process fails with
// ErrConnUnusable so it can go on another connection. Responses' field
// sections may use the dynamic table the profile offers; sections are
// acknowledged on the decoder stream, opened on its first instruction.
//
// Imports: profile, headerorder, qpack, quic (and quicwire),
// golang.org/x/net/http/httpguts and the standard library.
package h3
