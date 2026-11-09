package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/internal/dial"
	"github.com/roachadam/mimicry/internal/h3"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/proxy"
	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/profile"
)

// HTTP/3 through an http or https proxy (decision 0066): with
// Config.ConnectUDPProxy, each QUIC connection to a site through the proxy
// is a CONNECT-UDP stream (RFC 9298) of an HTTP/3 connection of its own to
// the CONNECT-UDP proxy that names, its packets that stream's HTTP/3
// datagrams. The QUIC to the site is the profile's, unchanged; the proxy's
// own connection is the profile's QUIC and HTTP/3 too, with datagrams on
// and larger packets (connectUDPOverhead). Every MASQUE proxy service
// documented speaks HTTP/3 only, so CONNECT-UDP over TCP (capsules on an
// HTTP/2 or HTTP/1.1 stream) is not offered.

// connectUDPOverhead is how much larger the QUIC datagrams to a CONNECT-UDP
// proxy are than the profile's: room for the outer packet's header (the
// longest connection ID and packet number) and AEAD tag, the DATAGRAM
// frame's type and length, the quarter stream ID and the context ID around
// a datagram of the inner connection, so the inner connection's datagrams
// keep the profile's size and the site sees them as sent directly.
const connectUDPOverhead = 64

// connectUDPCapable reports whether p's QUIC and HTTP/3 carry datagrams,
// as CONNECT-UDP needs (RFC 9297): its transport parameters announce
// max_datagram_frame_size and its SETTINGS H3_DATAGRAM (Chrome, Edge,
// Firefox; not Safari). A profile without them would have to announce
// what its browser does not.
func connectUDPCapable(p *profile.Spec) bool {
	if p.QUIC == nil || p.HTTP3 == nil {
		return false
	}

	frames := false

	for _, tp := range p.QUIC.TransportParameters {
		if tp.ID == 0x20 && tp.Value != nil && *tp.Value > 0 {
			frames = true
		}
	}

	for _, s := range p.HTTP3.Settings {
		if s.ID == 0x33 && s.Value == 1 {
			return frames
		}
	}

	return false
}

// listenConnectUDP opens a CONNECT-UDP stream to r's host at port through
// r's CONNECT-UDP proxy (r.udp) on a new HTTP/3 connection to it, and
// returns it as a socket with the peer address its datagrams are reported
// from: the host's address when it is an IP address, else the unspecified
// IPv4 address (the proxy resolves the name, as for CONNECT tunnels, and
// the host never goes to the Resolver). The socket's Close closes that
// connection. The proxy's credentials (r.udp's) go as an http proxy's
// (dial.Dialer.ConnectUDP); a refusal it gives every target is remembered.
// A failure is a *dial.ProxyError.
func (e *Engine) listenConnectUDP(ctx context.Context, r route, port uint16) (net.PacketConn, netip.AddrPort, error) {
	u := r.udp

	peer := netip.AddrPortFrom(netip.IPv4Unspecified(), port)
	if a, err := netip.ParseAddr(r.host); err == nil {
		peer = netip.AddrPortFrom(a.Unmap(), port)
	}

	authority := connectUDPAuthority(u)

	hc, err := e.dialConnectUDP(ctx, authority)
	if err != nil {
		return nil, netip.AddrPort{}, asProxyError(u, err)
	}

	path := connectUDPPath(r.host, port)

	pc, err := e.dialer.ConnectUDP(ctx, u, func(ctx context.Context, header []profile.Header) (proxy.Answer, net.PacketConn, error) {
		resp, uc, err := hc.ConnectUDP(ctx, authority, path, header, peer)
		if errors.Is(err, h3.ErrNoConnectUDP) {
			return proxy.Answer{}, nil, fmt.Errorf("%w: %w", dial.ErrNoConnectUDP, err)
		}

		if err != nil {
			return proxy.Answer{}, nil, err
		}

		a := proxy.Answer{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Header:     resp.Header,
		}

		if uc == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()

			return a, nil, nil
		}

		return a, uc, nil
	})
	if err != nil {
		_ = hc.Close()

		return nil, netip.AddrPort{}, err
	}

	return &connectUDPSocket{
		PacketConn: pc,
		hc:         hc,
	}, peer, nil
}

// dialConnectUDP opens an HTTP/3 connection to the CONNECT-UDP proxy at
// authority: a UDP socket as for a site (dial.Dialer.ListenUDP), the
// profile's QUIC hello to its host, verified as an https proxy is
// (Config.RootCAs, VerifyConnection, Pins), its secrets to KeyLogWriter;
// no session resumed; datagrams on, in packets connectUDPOverhead larger
// than the profile's.
func (e *Engine) dialConnectUDP(ctx context.Context, authority string) (*h3.Conn, error) {
	host, portText, err := net.SplitHostPort(authority)
	if err != nil {
		return nil, err
	}

	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("port %q: %w", portText, err)
	}

	omit, err := e.omitSNI(host)
	if err != nil {
		return nil, err
	}

	spec, err := handshake.Spec(e.plan.QUICHello())
	if err != nil {
		return nil, err
	}

	pc, addr, err := e.dialer.ListenUDP(ctx, host, uint16(port))
	if err != nil {
		return nil, err
	}

	outer := *e.quicProfile

	outer.DatagramSize += connectUDPOverhead

	qc, err := quic.Dial(ctx, pc, addr, quic.DialConfig{
		Profile: &outer,
		Hello:   spec,
		TLS: handshake.TLSConfig(handshake.Config{
			ServerName:       host,
			OmitSNI:          omit,
			RootCAs:          e.rootCAs,
			VerifyConnection: e.verifyFor(host),
			KeyLogWriter:     e.keyLog,
		}),
		Datagrams: true,
		NewPath: func(ctx context.Context) (net.PacketConn, error) {
			// The same proxy from a new local port (Chrome's migration).
			pc, _, err := e.dialer.ListenUDP(ctx, addr.Addr().String(), addr.Port())
			return pc, err
		},
	})
	if err != nil {
		return nil, err
	}

	return h3.New(qc, e.profile, h3.Options{
		Clock: e.clock,
	})
}

// connectUDPAuthority is the CONNECT-UDP proxy u's host and port (443 by
// default), its requests' :authority.
func connectUDPAuthority(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}

	return net.JoinHostPort(u.Hostname(), port)
}

// connectUDPPath is the default URI template's path (RFC 9298, section 3)
// for host at port: /.well-known/masque/udp/{target_host}/{target_port}/,
// each variable expanded as RFC 6570's simple string expansion does (all
// but unreserved characters percent-encoded: an IPv6 address's colons).
func connectUDPPath(host string, port uint16) string {
	var b strings.Builder

	b.WriteString("/.well-known/masque/udp/")

	for i := range len(host) {
		c := host[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)

			continue
		}

		fmt.Fprintf(&b, "%%%02X", c)
	}

	b.WriteByte('/')
	b.WriteString(strconv.Itoa(int(port)))
	b.WriteByte('/')

	return b.String()
}

// connectUDPSocket is a CONNECT-UDP stream as the QUIC connection's
// socket: closing it closes the HTTP/3 connection to the proxy as well.
type connectUDPSocket struct {
	net.PacketConn

	hc *h3.Conn
}

func (s *connectUDPSocket) Close() error {
	err := s.PacketConn.Close()

	_ = s.hc.Close()

	return err
}
