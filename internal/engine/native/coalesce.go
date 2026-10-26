package native

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/netip"

	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/internal/handshake"
)

// shared is a pooled HTTP/2 connection with what decides whether a
// request to another host may go on it (coalescing).
type shared struct {
	*h2.Conn
	// group is the route group of the request that opened it; "" for one
	// no other host's request may take (through a proxy).
	group string
	// addr is the server's address, invalid when the connection's remote
	// address is not an IP address.
	addr netip.Addr
	// tls is the connection's TLS state, which responses carry; leaf is
	// the server's certificate.
	tls  *tls.ConnectionState
	leaf *x509.Certificate
	// sent is closed once the request that opened it has its response
	// or failed: a GOAWAY the engine sends waits for it (goAwayTCP).
	sent chan struct{}
}

// newShared wraps hc, open on conn (a TLS connection) for r.
func newShared(hc *h2.Conn, conn net.Conn, r route) *shared {
	s := &shared{
		Conn:  hc,
		group: r.group,
		addr:  remoteAddr(conn),
		tls:   handshake.ConnState(conn),
		sent:  make(chan struct{}),
	}
	if s.tls != nil && len(s.tls.PeerCertificates) > 0 {
		s.leaf = s.tls.PeerCertificates[0]
	}
	return s
}

// remoteAddr is conn's remote IP address, or an invalid one.
func remoteAddr(conn net.Conn) netip.Addr {
	ap, err := netip.ParseAddrPort(conn.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

// covering returns the pool's HTTP/2 connections a request on r could
// take: in r's group (scheme, port, partition; never through a proxy),
// to a known address, with a certificate covering r's host, that
// Config.VerifyConnection accepts for r's host (its pins). The
// certificate was verified when the connection was made.
func (e *Engine) covering(r route) []*shared {
	if r.group == "" {
		return nil
	}
	var out []*shared
	for _, c := range e.pool.SharedConns() {
		if c.group == r.group && c.addr.IsValid() && c.leaf != nil && c.leaf.VerifyHostname(r.host) == nil && e.verifyShared(r.host, c) {
			out = append(out, c)
		}
	}
	return out
}

// verifyShared reports whether Config.VerifyConnection accepts c for a
// request to host, which c was not opened for.
func (e *Engine) verifyShared(host string, c *shared) bool {
	if e.verify == nil {
		return true
	}
	if c.tls == nil {
		return false
	}
	cs := *c.tls
	cs.ServerName = host
	return e.verify(host, cs) == nil
}

// coalesceOnLookup returns a connection r's request may take instead of
// dialing (profile.CoalesceOnLookup): one covering r's host, to an
// address the host resolves to. It looks the host up only when such a
// connection exists; a failed lookup coalesces nothing.
func (e *Engine) coalesceOnLookup(ctx context.Context, r route) *shared {
	conns := e.covering(r)
	if len(conns) == 0 {
		return nil
	}
	lctx, cancel := context.WithTimeout(ctx, e.dialer.Timeout)
	defer cancel()
	addrs, err := e.lookup(lctx, r.host)
	if err != nil {
		return nil
	}
	for _, c := range conns {
		for _, a := range addrs {
			if a.Unmap() == c.addr {
				return c
			}
		}
	}
	return nil
}

// coalesceOnConnect returns a connection r's request goes on instead of
// conn, just connected and handshaked (profile.CoalesceOnConnect): one
// covering r's host, to the address conn reached.
func (e *Engine) coalesceOnConnect(r route, conn net.Conn) *shared {
	addr := remoteAddr(conn)
	if !addr.IsValid() {
		return nil
	}
	for _, c := range e.covering(r) {
		if c.addr == addr {
			return c
		}
	}
	return nil
}

// lookupFunc looks host names up with resolver, or net.DefaultResolver.
func lookupFunc(resolver *net.Resolver) func(ctx context.Context, host string) ([]netip.Addr, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		return resolver.LookupNetIP(ctx, "ip", host)
	}
}
