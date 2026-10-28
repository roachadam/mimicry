package proxy

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"testing"
)

// TestAssociationFromAHostileRelay sends an association what a hostile or
// broken relay might: each malformed datagram is dropped without a crash
// and the well-formed one after them is read, its header gone; once the
// control connection closes, reads fail with ErrAssociationEnded.
func TestAssociationFromAHostileRelay(t *testing.T) {
	relay, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.IPv4(127, 0, 0, 1),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = relay.Close()
	})

	raddr, err := net.ResolveUDPAddr("udp4", relay.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}

	udp, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		t.Fatal(err)
	}

	ctrl, proxySide := net.Pipe()
	peer := netip.MustParseAddrPort("0.0.0.0:443")

	a, err := NewAssociation(ctrl, udp, "example.com", 443, peer)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = a.Close()
	})

	// The association's header names the host, so the relay learns the
	// client's address from its first datagram.
	if _, err := a.WriteTo([]byte("hello"), nil); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 64)

	n, client, err := relay.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}

	if want := append([]byte("\x00\x00\x00\x03\x0bexample.com\x01\xbb"), "hello"...); !bytes.Equal(buf[:n], want) {
		t.Fatalf("relay received % x, want % x", buf[:n], want)
	}

	for _, d := range [][]byte{
		{},
		{0, 0},
		{0, 0, 1, 1, 127, 0, 0, 1, 1, 187, 'x'}, // a fragment
		{0, 0, 0, 9, 127, 0, 0, 1, 1, 187, 'x'}, // unknown address type
		{0, 0, 0, 1, 127, 0, 0},                 // address cut short
		{0, 0, 0, 3},                            // no name length
		{0, 0, 0, 3, 200, 'a', 'b'},             // name longer than the datagram
		{0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, // IPv6 cut short
		{0, 0, 0, 1, 127, 0, 0, 1, 1, 187, 'o', 'k'},
	} {
		if _, err := relay.WriteToUDPAddrPort(d, client); err != nil {
			t.Fatal(err)
		}
	}

	n, from, err := a.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}

	if string(buf[:n]) != "ok" || from.String() != peer.String() {
		t.Fatalf("read %q from %v, want %q from %v", buf[:n], from, "ok", peer)
	}

	_ = proxySide.Close()

	if _, _, err := a.ReadFrom(buf); !errors.Is(err, ErrAssociationEnded) {
		t.Fatalf("read after the control connection closed: %v, want ErrAssociationEnded", err)
	}

	if _, err := a.WriteTo([]byte("late"), nil); !errors.Is(err, ErrAssociationEnded) {
		t.Fatalf("write after the control connection closed: %v, want ErrAssociationEnded", err)
	}
}
