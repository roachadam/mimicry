// Package dial is the single path from an address to a connected socket,
// directly or through an HTTP or SOCKS5 proxy (internal/proxy speaks their
// protocols; this package asks as the profile's browser does), and to a
// UDP socket of its own for a QUIC connection (ListenUDP). Every step
// honours the context and its deadline, so no other package opens
// connections.
//
// Imports: proxy, profile and the standard library.
package dial
