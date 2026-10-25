// Package qpack is a QPACK encoder whose dynamic table use follows a
// browser's (Strategy: Chromium indexes every field, Firefox inserts only
// names no table has, or none at all), with Huffman coding always or only
// where it shortens a string, and a decoder for the peer's field sections
// and encoder stream (RFC 9204). The encoder tracks the peer's
// acknowledgments (Known Received Count, blocked streams) from its
// decoder stream, and never evicts an entry the peer may still need.
//
// Imports: golang.org/x/net/http2/hpack (Huffman coding) and the standard
// library.
package qpack
