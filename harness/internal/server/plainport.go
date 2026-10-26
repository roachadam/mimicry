package server

import (
	"errors"
	"net"
)

// portPair is where a PlainPort scenario's page is served: TLS on the
// first port, plain HTTP/1.1 on the second.
type portPair struct {
	tls, plain string
}

// PlainPort reports a scenario that serves plain HTTP on a second port of
// its own (Server.PlainListener), which refuses TLS: a browser that tries
// an http:// URL of that port over TLS first (Safari's automatic HTTPS
// upgrade keeps the port) fails there and falls back to plain HTTP. Hosts
// map to the same address on either port.
func (sc *Scenario) PlainPort() bool {
	return sc.plainPort
}

// StartsPlain reports a PlainPort scenario whose page starts at http:// on
// its plain port rather than at https:// on the first.
func (sc *Scenario) StartsPlain() bool {
	return sc.plainPort && sc.startPlain
}

// onPorts is the scenario as served on tlsAddr and plainAddr (nil for a
// scenario without a plain port): its answers learn both ports
// (request.ports).
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

// refuseTLS answers a TLS ClientHello on a plain port as an HTTP-only
// server answers bytes that are not HTTP: 400 and close. The browser's TLS
// handshake fails at once, rather than waiting for a ServerHello.
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
