package dial

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"
)

// TestLocalAddrs checks that LocalAddrs binds every socket the Dialer opens
// itself: a TCP connect reaches the server from the address of its family,
// QUIC's UDP socket is bound to it, and a server of a family with none is
// refused. 127.0.0.2 needs a host routing all of 127/8 to loopback
// (Linux); elsewhere its rows are skipped.
func TestLocalAddrs(t *testing.T) {
	var lc net.ListenConfig

	v4, v6 := netip.MustParseAddr("127.0.0.2"), netip.MustParseAddr("::1")
	if pc, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.2:0"); err == nil {
		_ = pc.Close()
	} else {
		v4 = netip.Addr{}
	}

	tests := []struct {
		name   string
		local  []netip.Addr
		server string
		// host is the name dialled, "" for server: localhost has an
		// address of each family, raced by Happy Eyeballs.
		host string
		want netip.Addr
		err  bool
	}{
		{name: "IPv4", local: []netip.Addr{v4}, server: "127.0.0.1", want: v4},
		{name: "IPv6", local: []netip.Addr{v6}, server: "::1", want: v6},
		{name: "both, IPv4 server", local: []netip.Addr{v4, v6}, server: "127.0.0.1", want: v4},
		{name: "both, IPv6 server", local: []netip.Addr{v4, v6}, server: "::1", want: v6},
		{name: "both, by name, IPv4 server", local: []netip.Addr{v4, v6}, server: "127.0.0.1", host: "localhost", want: v4},
		{name: "both, by name, IPv6 server", local: []netip.Addr{v4, v6}, server: "::1", host: "localhost", want: v6},
		{name: "IPv6 server, IPv4 only", local: []netip.Addr{v4}, server: "::1", err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, a := range tc.local {
				if !a.IsValid() {
					t.Skip("127.0.0.2 is not a local address here")
				}
			}

			ln, err := lc.Listen(t.Context(), "tcp", net.JoinHostPort(tc.server, "0"))
			if err != nil {
				t.Skipf("no %s listener: %v", tc.server, err)
			}

			t.Cleanup(func() {
				_ = ln.Close()
			})

			accepted := make(chan net.Addr, 1)

			go func() {
				c, err := ln.Accept()
				if err != nil {
					return
				}

				accepted <- c.RemoteAddr()

				_ = c.Close()
			}()

			port := ln.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a TCP listener's
			d := &Dialer{
				Timeout:    5 * time.Second,
				LocalAddrs: tc.local,
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			host := tc.host
			if host == "" {
				host = tc.server
			}

			conn, err := d.Dial(ctx, net.JoinHostPort(host, strconv.Itoa(port)))

			switch {
			case tc.err && err == nil:
				_ = conn.Close()

				t.Error("TCP: connected from a family LocalAddrs does not give")

			case !tc.err && err != nil:
				t.Fatalf("TCP: %v", err)
			case !tc.err:
				_ = conn.Close()

				if got := addrOf(<-accepted); got != tc.want {
					t.Errorf("TCP: the server saw %v, want %v", got, tc.want)
				}
			}

			pc, peer, err := d.ListenUDP(ctx, host, uint16(port))
			if err == nil {
				t.Cleanup(func() {
					_ = pc.Close()
				})
			}

			switch {
			case tc.err && err == nil:
				t.Error("UDP: bound for a family LocalAddrs does not give")
			case tc.err:
			case err != nil:
				t.Fatalf("UDP: %v", err)
			default:
				// The socket's family is the server address it was opened
				// for: by name, the first one resolved.
				local := tc.local[0]
				for _, a := range tc.local {
					if a.Is6() == peer.Addr().Is6() {
						local = a
					}
				}

				if got := addrOf(pc.LocalAddr()); got != local {
					t.Errorf("UDP: bound to %v, want %v (peer %v)", got, local, peer)
				}

				if tc.host == "" && peer.Addr() != netip.MustParseAddr(tc.server) {
					t.Errorf("UDP: peer %v, want %v", peer, tc.server)
				}
			}
		})
	}
}

// addrOf is a TCP or UDP address's IP.
func addrOf(a net.Addr) netip.Addr {
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}
	}

	return ap.Addr().Unmap()
}
