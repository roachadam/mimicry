package e2e

import (
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/udprelay"
)

// TestTrafficMatchesWire checks Config.Traffic (decision 0070) against what
// the far end of each socket saw: an origin's HTTP/1.1 connections, which
// advertise HTTP/3, and its QUIC connection through the relay, after a
// download. With the connections idle, mimicry's HTTP/1.1 counts are the
// TCP server's and its HTTP/3 counts the relay's, byte for byte, all of
// them under the origin's host; nothing counts as HTTP/2, WebSocket or
// Other. Once they are closed, what mimicry sent closing them (TLS
// close_notify, QUIC CONNECTION_CLOSE) is counted too, the server's answer
// to it not: the socket was closed.
func TestTrafficMatchesWire(t *testing.T) {
	pki := newInteropPKI(t)

	for _, name := range testProfiles(t) {
		p, err := loadProfile(name)
		if err != nil {
			t.Fatal(err)
		}

		if p.QUIC == nil || p.HTTP3 == nil {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			meter := mimicry.NewTrafficMeter(mimicry.TrafficOptions{
				ByHost: true,
			})

			e := newInteropEnv(t, p, pki, interopCase{
				name:  "traffic",
				limit: 20 * time.Second,
				config: func(cfg *mimicry.Config) {
					cfg.Traffic = meter
				},
			})

			e.warm()
			e.download(256*kib, e.seed)

			// The connections are idle once the last ACKs are in.
			var got mimicry.Traffic

			e.waitFor("mimicry's counts to equal the server's and the relay's", func() bool {
				got = meter.Snapshot()
				return got.HTTP1 == e.tcpBytes.bytes() && got.HTTP3 == relayBytes(e.relay)
			})

			e.tr.CloseIdleConnections()

			e.waitFor("what mimicry sent closing its connections to be counted", func() bool {
				closed := meter.Snapshot()
				tcp, udp := e.tcpBytes.bytes(), relayBytes(e.relay)

				return closed.HTTP1.Sent == tcp.Sent && closed.HTTP3.Sent == udp.Sent &&
					closed.HTTP1.Sent > got.HTTP1.Sent && closed.HTTP3.Sent > got.HTTP3.Sent &&
					closed.HTTP1.Received <= tcp.Received && closed.HTTP3.Received <= udp.Received
			})

			if got.HTTP1.Sent == 0 || got.HTTP3.Received < 256*kib {
				t.Errorf("HTTP/1.1 %+v, HTTP/3 %+v: the page's connections were not counted", got.HTTP1, got.HTTP3)
			}

			for what, b := range map[string]mimicry.Bytes{
				"HTTP/2":    got.HTTP2,
				"WebSocket": got.WebSocket,
				"other":     got.Other,
			} {
				if b != (mimicry.Bytes{}) {
					t.Errorf("%s: %+v, want none", what, b)
				}
			}

			origin := got.ByHost[mimicry.Peer{
				Host: interopHost,
			}]
			if want := add(got.HTTP1, got.HTTP3); origin != want {
				t.Errorf("%s's counts %+v, want its HTTP/1.1 and HTTP/3 together, %+v (by host: %+v)", interopHost, origin, want, got.ByHost)
			}

			if total := got.Total(); total != add(origin, got.DNS) {
				t.Errorf("total %+v, want the origin's and DNS's, %+v", total, add(origin, got.DNS))
			}

			// Reset hands over the counts and starts from zero.
			closed := meter.Snapshot()
			if taken := meter.Reset(); taken.Total() != closed.Total() || len(taken.ByHost) != len(closed.ByHost) {
				t.Errorf("Reset returned %+v, the counts were %+v", taken, closed)
			}

			if after := meter.Snapshot(); after.Total() != (mimicry.Bytes{}) || len(after.ByHost) != 0 {
				t.Errorf("after Reset: %+v, want nothing", after)
			}

			checkProxiedTraffic(t, e)
		})
	}
}

// checkProxiedTraffic checks that a Transport's connections through a
// proxy count under the origin and that proxy: a SOCKS5 proxy carrying its
// TCP and, through a UDP association, its HTTP/3, and an HTTP proxy's
// CONNECT tunnels.
func checkProxiedTraffic(t *testing.T, e *interopEnv) {
	t.Helper()

	socks := newSOCKSServer(t, true, "", "")
	proxies := map[*url.URL]bool{
		socks.url("socks5h", nil): true,
		connectProxy(t):           false,
	}

	for proxy, h3 := range proxies {
		meter := mimicry.NewTrafficMeter(mimicry.TrafficOptions{
			ByHost: true,
		})

		e.cfg.Traffic = meter

		next := e.via(proxy, h3, e.altSvcAt(e.relay.Addr().Port()))

		if h3 {
			next.untilH3("a request through " + proxy.String())
		} else if _, _, err := next.do(http.MethodGet, "/info?noalt", nil); err != nil {
			t.Fatalf("a request through %s: %v", proxy, err)
		}

		got := meter.Snapshot()
		peer := mimicry.Peer{
			Host:  interopHost,
			Proxy: proxy.Scheme + "://" + proxy.Host,
		}

		if want := add(got.HTTP1, got.HTTP3); got.ByHost[peer] != want || want.Sent == 0 {
			t.Errorf("through %s: %+v under %+v, want its HTTP/1.1 and HTTP/3 together, %+v (by host: %+v)", proxy, got.ByHost[peer], peer, want, got.ByHost)
		}

		if h3 && got.HTTP3.Received == 0 {
			t.Errorf("through %s: no HTTP/3 counted (%+v)", proxy, got)
		}
	}
}

func add(a, b mimicry.Bytes) mimicry.Bytes {
	return mimicry.Bytes{
		Sent:     a.Sent + b.Sent,
		Received: a.Received + b.Received,
	}
}

// relayBytes is what the relay forwarded, as its client counts it: the
// bytes it sent to the server, and those it sent the client.
func relayBytes(r *udprelay.Relay) mimicry.Bytes {
	s := r.Stats()

	return mimicry.Bytes{
		Sent:     s.ForwardedBytes[udprelay.ToServer],
		Received: s.ForwardedBytes[udprelay.ToClient],
	}
}

// wireBytes counts a server's TCP bytes, as its clients count them: what
// the server read is what they sent.
type wireBytes struct {
	read, written atomic.Int64
}

func (w *wireBytes) bytes() mimicry.Bytes {
	return mimicry.Bytes{
		Sent:     w.read.Load(),
		Received: w.written.Load(),
	}
}

// countingListener counts its connections' bytes into n.
type countingListener struct {
	net.Listener

	n *wireBytes
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &countingConn{
		Conn: c,
		n:    l.n,
	}, nil
}

type countingConn struct {
	net.Conn

	n *wireBytes
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.n.read.Add(int64(n))

	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.n.written.Add(int64(n))

	return n, err
}
