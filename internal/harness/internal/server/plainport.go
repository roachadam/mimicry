package server

import (
	"errors"
	"net"
)

// portPair is where a PlainPort scenario's page is served: TLS on the
// first port, plain HTTP/1.1 on the second. quic is the UDP port of a
// scenario whose Alt-Svc names its QUIC socket's own (ech-h3), zero
// otherwise.
type portPair struct {
	tls, plain, quic string
}

// PlainPort reports a scenario that serves plain HTTP on a second port of
// its own (Server.PlainListener), which refuses TLS so a browser trying
// HTTPS first there (Safari's automatic upgrade keeps the port) falls back.
func (sc *Scenario) PlainPort() bool {
	return sc.plainPort
}

// StartsPlain reports a PlainPort scenario whose page starts at http:// on
// its plain port rather than at https:// on the first.
func (sc *Scenario) StartsPlain() bool {
	return sc.plainPort && sc.startPlain
}

// DefaultPort reports a scenario whose page names https://'s default
// port: the capture tool forwards 443 to the TLS port. Elsewhere (the e2e
// tests, which dial the server whatever the port) nothing is needed.
func (sc *Scenario) DefaultPort() bool {
	return sc.defaultPort
}

// onPorts is the scenario as served on tlsAddr and plainAddr (nil for a
// scenario without a plain port), so its answers learn both ports.
func (sc *Scenario) onPorts(tlsAddr, plainAddr net.Addr) *Scenario {
	on := *sc

	_, on.ports.tls, _ = net.SplitHostPort(tlsAddr.String())

	if plainAddr != nil {
		_, on.ports.plain, _ = net.SplitHostPort(plainAddr.String())
	}

	return &on
}

// errTLSRefused is a TLS handshake on a plain port.
var errTLSRefused = errors.New("plain port: refused a TLS handshake (answered 400, as an HTTP-only server would)")

// refuseTLS answers a TLS ClientHello on a plain port with 400 and close,
// as an HTTP-only server would, so the handshake fails at once.
func refuseTLS(conn net.Conn) error {
	if err := writeResponse(conn, response{
		status:      400,
		contentType: "text/plain",
		body:        []byte("bad request\n"),
		closeAfter:  true,
	}); err != nil && !isClosed(err) {
		return err
	}

	return errTLSRefused
}
