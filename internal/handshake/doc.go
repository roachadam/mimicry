// Package handshake performs the TLS handshake and ALPN from a fingerprint plan,
// and keeps session tickets for resumption. It is the only package that
// imports github.com/roachadam/utls.
//
// Imports: fingerprint, profile, utls, clockwork and the standard library.
package handshake
