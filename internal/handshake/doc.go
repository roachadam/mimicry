// Package handshake performs the TLS handshake and ALPN from a fingerprint plan,
// and keeps session tickets for resumption. It and internal/quic (QUIC's TLS)
// are the only packages that import github.com/roachadam/utls. A
// Config.VerifyConnection runs in every handshake's verification, resumed
// ones included; State and ConnState describe a connection as crypto/tls
// does.
//
// Imports: fingerprint, profile, utls, clockwork and the standard library.
package handshake
