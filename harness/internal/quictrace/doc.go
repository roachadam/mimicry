// Package quictrace records what a client sends over QUIC and HTTP/3, below
// the decoded requests: every UDP datagram with its coalesced packets'
// headers, each packet's frames (Initial packets decrypted with the keys
// derived from the client's first Destination Connection ID, RFC 9001
// section 5.2; the other packet types with the secrets of the server's TLS
// key log), the ClientHello its CRYPTO frames carry and its QUIC transport
// parameters, and HTTP/3's streams, frames and QPACK instructions. Two
// clients can send identical requests and still differ here.
//
// Imports: mimicry's profile package, golang.org/x/crypto,
// golang.org/x/net/http2/hpack (QPACK uses HPACK's Huffman code) and the
// standard library.
package quictrace
