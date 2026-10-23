// Package hpack is an HPACK encoder whose choices follow a browser's
// (profile.HPACK): which fields enter the dynamic table, which table entry a
// literal's name refers to, and when strings are Huffman-coded. Decoding stays
// with golang.org/x/net/http2/hpack; only what the client sends is
// fingerprinted.
//
// Imports: profile, golang.org/x/net/http2/hpack (Huffman coding) and the
// standard library.
package hpack
