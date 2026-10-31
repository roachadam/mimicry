// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package quic

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"
)

func TestUDPSourceUnspecified(t *testing.T) {
	// Send datagram with no source address set.
	runUDPTest(t, func(t *testing.T, test udpTest) {
		t.Logf("%v", test.dstAddr)
		data := []byte("source unspecified")
		if err := test.src.Write(datagram{
			b:    data,
			path: pathAddrs{peer: test.dstAddr},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}

		got := test.recv(t) // mimicry: not <-test.dgramc
		if !bytes.Equal(got.b, data) {
			t.Errorf("got datagram {%x}, want {%x}", got.b, data)
		}
	})
}

func TestUDPSourceSpecified(t *testing.T) {
	// Send datagram with source address set.
	runUDPTest(t, func(t *testing.T, test udpTest) {
		data := []byte("source specified")
		if err := test.src.Write(datagram{
			b: data,
			path: pathAddrs{
				peer:  test.dstAddr,
				local: test.src.LocalAddr(),
			},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}

		got := test.recv(t) // mimicry: not <-test.dgramc
		if !bytes.Equal(got.b, data) {
			t.Errorf("got datagram {%x}, want {%x}", got.b, data)
		}
	})
}

func TestUDPSourceInvalid(t *testing.T) {
	// Send datagram with source address set to an address not associated with the connection.
	if !udpInvalidLocalAddrIsError {
		t.Skipf("%v: sending from invalid source succeeds", runtime.GOOS)
	}
	runUDPTest(t, func(t *testing.T, test udpTest) {
		var localAddr netip.AddrPort
		if test.src.LocalAddr().Addr().Is4() {
			localAddr = netip.MustParseAddrPort("127.0.0.2:1234")
		} else {
			localAddr = netip.MustParseAddrPort("[::2]:1234")
		}
		data := []byte("source invalid")
		if err := test.src.Write(datagram{
			b: data,
			path: pathAddrs{
				peer:  test.dstAddr,
				local: localAddr,
			},
		}); err == nil {
			t.Errorf("Write with invalid localAddr succeeded; want error")
		}
	})
}

func TestUDPECN(t *testing.T) {
	if !udpECNSupport {
		t.Skipf("%v: no ECN support", runtime.GOOS)
	}
	// Send datagrams with ECN bits set, verify the ECN bits are received.
	runUDPTest(t, func(t *testing.T, test udpTest) {
		for _, ecn := range []ecnBits{ecnNotECT, ecnECT1, ecnECT0, ecnCE} {
			if err := test.src.Write(datagram{
				b:    []byte{1, 2, 3, 4},
				path: pathAddrs{peer: test.dstAddr},
				ecn:  ecn,
			}); err != nil {
				t.Fatalf("Write: %v", err)
			}

			got := test.recv(t) // mimicry: not <-test.dgramc
			if got.ecn != ecn {
				t.Errorf("sending ECN bits %x, got %x", ecn, got.ecn)
			}
		}
	})
}

type udpTest struct {
	src     *netUDPConn
	dst     *netUDPConn
	dstAddr netip.AddrPort
	dgramc  chan *datagram
}

// mimicry: udpRecvTimeout bounds recv's wait. A loopback datagram arrives
// at once; the bound is for the read goroutine starved under load.
const udpRecvTimeout = 10 * time.Second

// mimicry: recv returns the next datagram dst read, and fails the test if
// none comes within udpRecvTimeout, instead of hanging until the test
// binary's timeout.
func (u udpTest) recv(t *testing.T) *datagram {
	t.Helper()

	select {
	case d := <-u.dgramc:
		return d
	case <-time.After(udpRecvTimeout):
		t.Fatalf("no datagram to %v within %v", u.dstAddr, udpRecvTimeout)

		return nil
	}
}

// mimicry: listenDualStackForIPv4 binds a dual-stack socket ([::]) on a
// port no IPv4 socket has, for a test sending it IPv4 datagrams. On macOS
// an ephemeral dual-stack bind ignores IPv4 sockets: it can take the port
// of any process's socket bound to 127.0.0.1 or 0.0.0.0, and the datagrams
// sent to 127.0.0.1 on it then go to that socket, never to this one. An
// IPv4 wildcard bind takes a port no IPv4 or dual-stack socket has and
// keeps other IPv4 binds off it until the dual-stack socket has it; from
// then on, macOS gives no IPv4 bind that port either. Linux refuses the
// dual-stack bind beside the IPv4 one, and never gives an ephemeral
// dual-stack bind a port an IPv4 socket has, so there the ephemeral bind
// is used.
func listenDualStackForIPv4() (*net.UDPConn, error) {
	hold, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}

	addr, _ := hold.LocalAddr().(*net.UDPAddr)
	c, err := net.ListenUDP("udp", &net.UDPAddr{
		Port: addr.Port,
	})

	_ = hold.Close()

	if err != nil {
		return net.ListenUDP("udp", nil)
	}

	return c, nil
}

// runUDPTest calls f with a pair of UDPConns in a matrix of network variations:
// udp, udp4, and udp6, and variations on binding to an unspecified address (0.0.0.0)
// or a specified one.
func runUDPTest(t *testing.T, f func(t *testing.T, u udpTest)) {
	for _, test := range []struct {
		srcNet, srcAddr, dstNet, dstAddr string
	}{
		{"udp4", "127.0.0.1", "udp", ""},
		{"udp4", "127.0.0.1", "udp4", ""},
		{"udp4", "127.0.0.1", "udp4", "127.0.0.1"},
		{"udp6", "::1", "udp", ""},
		{"udp6", "::1", "udp6", ""},
		{"udp6", "::1", "udp6", "::1"},
	} {
		spec := "spec"
		if test.dstAddr == "" {
			spec = "unspec"
		}
		t.Run(fmt.Sprintf("%v/%v/%v", test.srcNet, test.dstNet, spec), func(t *testing.T) {
			// See: https://go.googlesource.com/go/+/refs/tags/go1.22.0/src/net/ipsock.go#47
			// On these platforms, conns with network="udp" cannot accept IPv6.
			switch runtime.GOOS {
			case "dragonfly", "openbsd":
				if test.srcNet == "udp6" && test.dstNet == "udp" {
					t.Skipf("%v: no support for mapping IPv4 address to IPv6", runtime.GOOS)
				}
			}
			if runtime.GOARCH == "wasm" && test.srcNet == "udp6" {
				t.Skipf("%v: IPv6 tests fail when using wasm fake net", runtime.GOARCH)
			}

			srcAddr := netip.AddrPortFrom(netip.MustParseAddr(test.srcAddr), 0)
			srcConn, err := net.ListenUDP(test.srcNet, net.UDPAddrFromAddrPort(srcAddr))
			if err != nil {
				// If ListenUDP fails here, we presumably don't have
				// IPv4/IPv6 configured.
				t.Skipf("ListenUDP(%q, %v) = %v", test.srcNet, srcAddr, err)
			}
			t.Cleanup(func() { srcConn.Close() })
			src, err := newNetUDPConn(srcConn)
			if err != nil {
				t.Fatalf("newNetUDPConn: %v", err)
			}

			var dstAddr netip.AddrPort
			if test.dstAddr != "" {
				dstAddr = netip.AddrPortFrom(netip.MustParseAddr(test.dstAddr), 0)
			}

			// mimicry: see listenDualStackForIPv4.
			var dstConn *net.UDPConn
			if test.dstNet == "udp" && !dstAddr.IsValid() && srcAddr.Addr().Is4() {
				dstConn, err = listenDualStackForIPv4()
			} else {
				dstConn, err = net.ListenUDP(test.dstNet, net.UDPAddrFromAddrPort(dstAddr))
			}

			if err != nil {
				t.Skipf("ListenUDP(%q, nil) = %v", test.dstNet, err)
			}
			dst, err := newNetUDPConn(dstConn)
			if err != nil {
				dstConn.Close()
				t.Fatalf("newNetUDPConn: %v", err)
			}

			dgramc := make(chan *datagram)
			go func() {
				defer close(dgramc)
				dst.Read(func(dgram *datagram) {
					dgramc <- dgram
				})
			}()
			t.Cleanup(func() {
				dstConn.Close()
				for range dgramc {
					t.Errorf("test read unexpected datagram")
				}
			})

			f(t, udpTest{
				src: src,
				dst: dst,
				dstAddr: netip.AddrPortFrom(
					srcAddr.Addr(),
					dst.LocalAddr().Port(),
				),
				dgramc: dgramc,
			})
		})
	}
}
