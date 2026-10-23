// Package h2trace records what a client sends on an HTTP/2 connection below
// the decoded header list: every frame, with timing, and each header block's
// HPACK instructions (representation, table index, Huffman use). Two clients
// can send identical headers and still differ here.
//
// Imports: golang.org/x/net/http2/hpack and the standard library.
package h2trace
