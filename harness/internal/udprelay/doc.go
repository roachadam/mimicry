// Package udprelay is a UDP relay between QUIC clients and a server that
// impairs what it forwards, per direction: it drops, duplicates, corrupts,
// delays and reorders datagrams, drops those over a size cap, and
// blackholes a direction, every choice drawn from a seeded source per
// connection, so the n-th datagram of a direction meets the same fate on
// every run. Each client address gets an upstream socket of its own, which
// the relay can move to another port or address mid-connection (NAT
// rebinding, as the server sees it). It reads the long headers it forwards,
// to count Retry packets and check the server's anti-amplification limit.
//
// Imports: the standard library only.
package udprelay
