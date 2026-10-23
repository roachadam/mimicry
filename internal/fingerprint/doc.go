// Package fingerprint compiles a profile once into an immutable plan, then fills
// in the per-connection randomness (GREASE values, a uniform extension shuffle,
// key shares) to produce the ClientHello for each connection.
//
// Imports: profile and the standard library. It describes the ClientHello; it does
// not perform handshakes.
package fingerprint
