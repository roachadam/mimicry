package h3

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	utls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profiles"
)

// A CONNECT-UDP proxy whose max_datagram_frame_size leaves less room than
// the connection it would carry needs (its datagrams with the quarter
// stream ID and context ID) is refused before anything is sent: every
// write would fail and the carried connection stall. quic-go's server
// announces a fixed 16383, so no interop row reaches this; a raw peer
// announcing the limit does.
func TestConnectUDPNeedsRoomForTheDatagrams(t *testing.T) {
	const payload = 1250 // Chrome's datagrams

	for _, tc := range []struct {
		limit int64
		ok    bool
	}{
		{payload, false},
		{payload + 3 + 2, true}, // the frame's type and length, then the prefix
	} {
		c, requests := connectUDPPeer(t, tc.limit)

		resp, u, err := c.ConnectUDP(t.Context(), "localhost:443", "/.well-known/masque/udp/example.test/443/", nil, netip.AddrPort{}, payload)
		switch {
		case tc.ok && (err != nil || u == nil):
			t.Errorf("limit %d: %v, want a tunnel", tc.limit, err)
		case !tc.ok && !errors.Is(err, ErrNoConnectUDP):
			t.Errorf("limit %d: %v, want ErrNoConnectUDP", tc.limit, err)
		case !tc.ok && requests.Load() != 0:
			t.Errorf("limit %d: the peer got %d requests, want none", tc.limit, requests.Load())
		}

		if u != nil {
			_ = u.Close()
		}

		if resp != nil {
			_ = resp.Body.Close()
		}
	}
}

// connectUDPPeer returns a Chrome connection, with DATAGRAM frames, to a
// raw HTTP/3 peer announcing max_datagram_frame_size limit, extended CONNECT
// and HTTP/3 datagrams, which answers every request 200 and counts them.
func connectUDPPeer(t *testing.T, limit int64) (*Conn, *atomic.Int32) {
	t.Helper()

	cert, roots := testutil.Certificate(t, "localhost")

	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ep, err := quic.NewEndpoint(pc, &quic.Config{
		TLSConfig: &utls.Config{
			Certificates: []utls.Certificate{{
				Certificate: cert.Certificate,
				PrivateKey:  cert.PrivateKey,
			}},
			NextProtos: []string{"h3"},
			MinVersion: utls.VersionTLS13,
		},
		MaxDatagramFrameSize: limit,
	})
	if err != nil {
		_ = pc.Close()

		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_ = ep.Close(ctx)
	})

	requests := new(atomic.Int32)

	go func() {
		for {
			sc, err := ep.Accept(context.Background())
			if err != nil {
				return
			}

			go serveConnectUDP(sc, requests)
		}
	}()

	p := profiles.ChromeMacOS().Spec()

	spec, err := handshake.Spec(fingerprint.Compile(p).QUICHello())
	if err != nil {
		t.Fatal(err)
	}

	cpc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	outer := *p.QUIC

	outer.DatagramSize += 64

	qc, err := quic.Dial(t.Context(), cpc, ep.LocalAddr(), quic.DialConfig{
		Profile: &outer,
		Hello:   spec,
		TLS: handshake.TLSConfig(handshake.Config{
			ServerName: "localhost",
			RootCAs:    roots,
		}),
		Datagrams: true,
		NewPath: func(ctx context.Context) (net.PacketConn, error) {
			return (&net.ListenConfig{}).ListenPacket(ctx, "udp", "127.0.0.1:0")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	c, err := New(qc, p, Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = c.Close()
	})

	return c, requests
}

// serveConnectUDP sends SETTINGS allowing extended CONNECT and HTTP/3
// datagrams, drains the client's uni streams and answers each request
// :status 200, keeping its stream open.
func serveConnectUDP(sc *quic.Conn, requests *atomic.Int32) {
	ctx := context.Background()

	control, err := sc.NewSendOnlyStream(ctx)
	if err != nil {
		return
	}

	// Control stream; SETTINGS: ENABLE_CONNECT_PROTOCOL 1, H3_DATAGRAM 1.
	_, _ = control.Write([]byte{0x00, 0x04, 0x04, 0x08, 0x01, 0x33, 0x01})
	_ = control.Flush()

	for {
		s, err := sc.AcceptStream(ctx)
		if err != nil {
			return
		}

		if s.IsReadOnly() {
			go func() {
				_, _ = io.Copy(io.Discard, s)
			}()

			continue
		}

		requests.Add(1)

		// HEADERS: QPACK prefix 0 0, static index 25 (:status 200).
		_, _ = s.Write([]byte{0x01, 0x03, 0x00, 0x00, 0xd9})
		_ = s.Flush()
	}
}
