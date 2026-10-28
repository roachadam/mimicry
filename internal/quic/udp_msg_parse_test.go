//go:build !quicbasicnet && (darwin || linux)

package quic

import (
	"net/netip"
	"syscall"
	"testing"
)

// mimicry: parseControl runs once per received datagram on the read loop:
// it must read ECN and the destination address as the kernel delivers
// them, take nothing from a malformed buffer, and allocate nothing.
func TestParseControl(t *testing.T) {
	dst := netip.MustParseAddr("10.1.2.3")

	var good []byte

	good, tos := appendCmsg(good, syscall.IPPROTO_IP, syscall.IP_TOS, 1)
	tos[0] = byte(ecnCE)

	good, pkt := appendCmsg(good, syscall.IPPROTO_IP, syscall.IP_PKTINFO, 12)
	ip := dst.As4()
	copy(pkt[8:], ip[:])

	// The second header claims more than the buffer holds.
	bad := append([]byte(nil), good...)
	second := syscall.CmsgSpace(1)
	bad[second] = 0xff

	for _, tc := range []struct {
		name    string
		control []byte
		ecn     ecnBits
		local   netip.Addr
	}{
		{"tos and pktinfo", good, ecnCE, dst},
		{"malformed", bad, ecnNotECT, netip.Addr{}},
		{"trailing fragment", append(append([]byte(nil), good...), 1, 2, 3), ecnCE, dst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &datagram{}
			parseControl(d, tc.control)
			if d.ecn != tc.ecn || d.path.local.Addr() != tc.local {
				t.Errorf("ecn %v local %v, want %v %v", d.ecn, d.path.local.Addr(), tc.ecn, tc.local)
			}
		})
	}

	d := &datagram{}
	if n := testing.AllocsPerRun(100, func() {
		parseControl(d, good)
	}); n != 0 {
		t.Errorf("parseControl allocates %v times per datagram, want 0", n)
	}
}
