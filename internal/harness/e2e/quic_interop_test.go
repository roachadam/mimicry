package e2e

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/big"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/qlogwriter"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/udprelay"
	"github.com/roachadam/mimicry/profile"
)

// TestQUICInterop runs every profile's QUIC and HTTP/3 against quic-go's
// server through a seeded impairing relay (udprelay), via the public
// Transport so dial, pool and retry paths are exercised. Each case checks
// responses byte for byte, HTTP/3 use and its time limit. It covers the
// failures no browser capture reaches.
func TestQUICInterop(t *testing.T) {
	pki := newInteropPKI(t)
	for _, name := range testProfiles(t) {
		p, err := loadProfile(name)
		if err != nil {
			t.Fatal(err)
		}

		if p.QUIC == nil || p.HTTP3 == nil {
			continue
		}

		for _, c := range interopCases {
			t.Run(name+"/"+c.name, func(t *testing.T) {
				t.Parallel()

				if c.zeroRTT && !p.QUIC.ZeroRTT {
					t.Skip("the profile never resumes QUIC (0-RTT not measured)")
				}

				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}

				e := newInteropEnv(t, p, pki, c)
				start := time.Now()

				c.run(e)

				if d := time.Since(start); d > c.limit {
					t.Errorf("took %v, over the case's limit of %v", d, c.limit)
				}
			})
		}
	}
}

// interopHost is the origin's name; the stub resolver maps it to the
// loopback address, where the TCP server and the relay listen.
const interopHost = "interop.test"

// interopCase is one row of TestQUICInterop.
type interopCase struct {
	name string
	// limit bounds the whole case; every request carries it as a
	// deadline.
	limit time.Duration
	// zeroRTT: the case needs a profile that resumes QUIC with 0-RTT.
	zeroRTT bool
	// server configures the first backend, the one the relay starts
	// with.
	server backendOpts
	// config, when set, changes the client's Config.
	config func(cfg *mimicry.Config)
	run    func(e *interopEnv)
}

const (
	kib = 1 << 10
	mib = 1 << 20
)

// lossy is the transfer-loss impairment at drop probability p: reordering,
// duplication and a few milliseconds of delay besides.
func lossy(p float64) udprelay.Impairment {
	return udprelay.Impairment{
		Drop:      p,
		Duplicate: 0.02,
		Reorder:   0.05,
		ReorderBy: 8 * time.Millisecond,
		Delay:     2 * time.Millisecond,
	}
}

var interopCases = []interopCase{
	{
		name:  "handshake",
		limit: 15 * time.Second,
		run: func(e *interopEnv) {
			info := e.warm()
			if info.Cipher != tls.TLS_AES_128_GCM_SHA256 {
				e.t.Errorf("cipher suite %#04x, want TLS_AES_128_GCM_SHA256", info.Cipher)
			}

			e.download(64*kib, 1)
		},
	},
	{
		// A browser's tabs share what servers advertised, and a browser
		// keeps it across restarts: a new Transport sharing the cache, or
		// loading a saved one, knows the origin's HTTP/3 before any
		// response (?noalt: none carries Alt-Svc). Chrome and Safari send
		// its first request over QUIC, opening no TCP connection; Firefox
		// sends it on TCP and validates QUIC beside it, which takes the
		// requests after.
		name:  "shared-altsvc-cache",
		limit: 20 * time.Second,
		config: func(cfg *mimicry.Config) {
			cfg.AltSvc = mimicry.NewAltSvcCache()
		},
		run: func(e *interopEnv) {
			e.warm()

			var saved bytes.Buffer
			if err := e.cfg.AltSvc.WriteJSON(&saved); err != nil {
				e.t.Fatal(err)
			}

			loaded := mimicry.NewAltSvcCache()
			if err := loaded.ReadJSON(&saved); err != nil {
				e.t.Fatal(err)
			}

			for what, cache := range map[string]*mimicry.AltSvcCache{
				"sharing the cache": e.cfg.AltSvc,
				"loading it saved":  loaded,
			} {
				next := e.another(cache)
				tcp := e.tcpConns.Load()

				resp, _, err := next.do(http.MethodGet, "/info?noalt", nil)
				if err != nil {
					e.t.Fatalf("a new Transport %s: %v", what, err)
				}

				if e.p.HTTP3.Discovery.Fallback.Mode == profile.FallbackRace {
					next.h3(resp, "the first request of a new Transport "+what)

					if n := e.tcpConns.Load() - tcp; n != 0 {
						e.t.Errorf("a new Transport %s opened %d TCP connections", what, n)
					}

					continue
				}

				if resp.ProtoMajor == 3 {
					e.t.Errorf("the first request of a new Transport %s went over HTTP/3; Firefox sends it on TCP", what)
				}

				for {
					resp, _, err := next.do(http.MethodGet, "/info?noalt", nil)
					if err != nil {
						e.t.Fatalf("a new Transport %s: %v", what, err)
					}

					if resp.ProtoMajor == 3 {
						break
					}

					select {
					case <-e.ctx.Done():
						e.t.Fatalf("a new Transport %s never moved to HTTP/3", what)
					case <-time.After(20 * time.Millisecond):
					}
				}
			}
		},
	},
	{
		name:  "download-10MB",
		limit: 60 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			e.download(10*mib, 2)
		},
	},
	{
		name:  "upload-2MB",
		limit: 40 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			e.upload(2*mib, 3)
		},
	},
	{
		name:  "concurrent-20",
		limit: 30 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			e.parallel(20, func(e *interopEnv, i int) {
				e.download(256*kib, uint64(100+i))
			})
		},
	},
	{
		name:  "retry",
		limit: 15 * time.Second,
		server: backendOpts{
			retry: true,
		},
		run: func(e *interopEnv) {
			e.warm()
			e.download(256*kib, 4)

			if n := e.relay.Stats().Retries; n == 0 {
				e.t.Error("the server sent no Retry")
			}
		},
	},
	{
		name:    "resumption",
		limit:   15 * time.Second,
		zeroRTT: true,
		run: func(e *interopEnv) {
			first := e.warm()
			e.newConnection()

			info, _ := e.download(64*kib, 5)
			if !info.Resumed {
				e.t.Error("the second connection did not resume the first's session")
			}

			if info.Conn == first.Conn {
				e.t.Error("the request after CloseIdleConnections went on the first connection")
			}
		},
	},
	{
		name:    "0rtt-accepted",
		limit:   15 * time.Second,
		zeroRTT: true,
		run: func(e *interopEnv) {
			e.warm()
			e.newConnection()

			info, _ := e.download(256*kib, 6)
			if !info.Used0RTT || !info.Early {
				e.t.Errorf("the request on the resumed connection was not sent in 0-RTT (accepted %t, before the handshake completed %t)", info.Used0RTT, info.Early)
			}

			e.upload(256*kib, 7)
			// Requests that find the connection while its handshake is
			// still in progress, from goroutines of their own.
			e.newConnection()
			e.parallel(8, func(e *interopEnv, i int) {
				e.download(16*kib, uint64(400+i))
			})
		},
	},
	{
		name:    "0rtt-rejected",
		limit:   15 * time.Second,
		zeroRTT: true,
		run: func(e *interopEnv) {
			e.warm()
			// The same certificate and ticket keys, without 0-RTT: the
			// session resumes and its early data is rejected.
			e.relay.SetTarget(e.backend(backendOpts{
				name:   "B",
				no0RTT: true,
			}).target)
			e.newConnection()

			info, _ := e.download(256*kib, 8)
			if info.Backend != "B" || !info.Resumed || info.Used0RTT || info.Early {
				e.t.Errorf("want B to resume without 0-RTT, got %+v", info)
			}

			e.upload(256*kib, 9)
		},
	},
	{
		// The server answers the request it received in 0-RTT with 425
		// Too Early (RFC 8470): the client sends it again on the same
		// connection, in 1-RTT, where its profile retries
		// (http3.earlyData.retryTooEarly), and returns the 425
		// otherwise.
		name:    "0rtt-too-early",
		limit:   15 * time.Second,
		zeroRTT: true,
		run: func(e *interopEnv) {
			e.warm()
			e.newConnection()

			b := e.backends[0]
			conns := b.conns.Load()

			resp, _, err := e.do(http.MethodGet, "/too-early", nil)
			if err != nil {
				e.t.Fatalf("GET /too-early: %v", err)
			}

			info := e.h3(resp, "GET /too-early")
			if n := b.tooEarly.Load(); n != 1 {
				e.t.Fatalf("the server answered %d requests 425, want the one in 0-RTT (%+v)", n, info)
			}

			if ed := e.p.HTTP3.EarlyData; ed == nil || !ed.RetryTooEarly {
				if resp.StatusCode != http.StatusTooEarly {
					e.t.Errorf("status %d; the profile does not retry, want 425", resp.StatusCode)
				}

				return
			}

			if resp.StatusCode != http.StatusOK || info.Early || !info.Used0RTT {
				e.t.Errorf("want a 200 on the resumed connection, after its handshake; got status %d, %+v", resp.StatusCode, info)
			}

			if n := b.conns.Load() - conns; n != 1 {
				e.t.Errorf("%d new connections; the retry belongs on the one that got the 425", n)
			}
		},
	},
	{
		// A stream opened in 0-RTT, on the remembered 16 KB stream
		// window, takes B's larger one when its transport parameters
		// arrive (RFC 9000, section 7.4.1): blocked at 16 KB in 0-RTT
		// packets at most, never in 1-RTT ones.
		name:    "0rtt-window-grows",
		limit:   15 * time.Second,
		zeroRTT: true,
		server: backendOpts{
			streamWindow: 16 * kib,
			connWindow:   64 * kib,
		},
		run: func(e *interopEnv) {
			e.warm()

			b := e.backend(backendOpts{
				name: "B",
			})
			e.relay.SetTarget(b.target)
			e.newConnection()

			info := e.upload(256*kib, 34)
			if info.Backend != "B" || !info.Used0RTT {
				e.t.Errorf("want B to accept 0-RTT, got %+v", info)
			}

			if n := b.streamBlocked.Load(); n != 0 {
				e.t.Errorf("B received %d STREAM_DATA_BLOCKED frames in 1-RTT: the stream kept the remembered window", n)
			}
		},
	},
	{
		// 0-RTT rejected by B, whose windows are smaller than the
		// remembered ones: what the client sends again in 1-RTT keeps
		// to B's (RFC 9001, section 4.6.2), or B closes the connection
		// with FLOW_CONTROL_ERROR.
		name:    "0rtt-rejected-smaller-windows",
		limit:   20 * time.Second,
		zeroRTT: true,
		run: func(e *interopEnv) {
			e.warm()
			e.relay.SetTarget(e.backend(backendOpts{
				name:         "B",
				no0RTT:       true,
				streamWindow: 8 * kib,
				connWindow:   16 * kib,
			}).target)
			e.newConnection()

			info := e.upload(256*kib, 35)
			if info.Backend != "B" || !info.Resumed || info.Used0RTT {
				e.t.Errorf("want B to resume without 0-RTT, got %+v", info)
			}
		},
	},
	{
		// A server speaking v2 only answers every profile's version 1
		// Initial with a Version Negotiation packet (RFC 9000, section
		// 6): the dial fails at once and the requests go over TCP,
		// none waiting for a handshake timeout.
		name:  "version-negotiation",
		limit: 10 * time.Second,
		server: backendOpts{
			versions: []quic.Version{quic.Version2},
		},
		run: func(e *interopEnv) {
			for i := range 6 {
				start := time.Now()

				resp, _, err := e.do(http.MethodGet, "/info", nil)
				if err != nil {
					e.t.Fatalf("request %d: %v", i, err)
				}

				if resp.ProtoMajor == 3 {
					e.t.Fatalf("request %d went over HTTP/3 to a v2-only server", i)
				}

				if d := time.Since(start); d > time.Second {
					e.t.Errorf("request %d took %v: it waited for the QUIC dial", i, d)
				}

				time.Sleep(50 * time.Millisecond)
			}

			if st := e.relay.Stats(); st.Forwarded[1] == 0 {
				e.t.Errorf("the server never answered the client's Initial: %+v", st)
			}
		},
	},
	{
		name:  "chacha20",
		limit: 20 * time.Second,
		server: backendOpts{
			chacha: true,
		},
		run: func(e *interopEnv) {
			info := e.warm()
			if info.Cipher != tls.TLS_CHACHA20_POLY1305_SHA256 {
				e.t.Fatalf("cipher suite %#04x, want TLS_CHACHA20_POLY1305_SHA256", info.Cipher)
			}

			e.download(2*mib, 10)
			e.upload(512*kib, 11)
		},
	},
	{
		name:  "key-update",
		limit: 20 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			// quic-go updates its keys after 100 packets.
			e.download(2*mib, 12)

			if e.server.keyUpdates.Load() == 0 {
				e.t.Fatal("the server never updated its keys")
			}

			e.download(1*mib, 13)
			e.upload(512*kib, 14)
		},
	},
	{
		name:  "handshake-loss",
		limit: 40 * time.Second,
		run: func(e *interopEnv) {
			e.relay.SetBoth(udprelay.Impairment{
				Drop: 0.3,
			})

			if !e.warmImpaired() {
				return
			}

			e.relay.SetBoth(udprelay.Impairment{})
			e.download(256*kib, 15)
		},
	},
	{
		name:  "transfer-loss-5",
		limit: 40 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			e.relay.SetBoth(lossy(0.05))
			e.download(2*mib, 16)
			e.upload(1*mib, 17)
			e.parallel(10, func(e *interopEnv, i int) {
				e.download(64*kib, uint64(200+i))
			})
		},
	},
	{
		name:  "transfer-loss-10",
		limit: 40 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			e.relay.SetBoth(lossy(0.10))
			e.download(1*mib, 18)
			e.upload(512*kib, 19)
		},
	},
	{
		name:  "handshake-corruption",
		limit: 30 * time.Second,
		run: func(e *interopEnv) {
			e.relay.SetBoth(udprelay.Impairment{
				Corrupt: 0.2,
			})

			if !e.warmImpaired() {
				return
			}

			e.relay.SetBoth(udprelay.Impairment{})
			e.download(256*kib, 20)
		},
	},
	{
		name:  "handshake-ack-loss",
		limit: 15 * time.Second,
		run: func(e *interopEnv) {
			// The client's second Initial datagram is lost, and so is
			// every datagram of the server's that carries only ACKs: the
			// rest of the ClientHello must come with the PTO probes, as
			// loss detection would need those ACKs.
			e.relay.Set(udprelay.ToServer, udprelay.Impairment{
				DropAt: []int{
					1,
				},
			})
			e.relay.Set(udprelay.ToClient, udprelay.Impairment{
				MinSize: 100,
			})

			if !e.warmImpaired() {
				return
			}

			e.relay.SetBoth(udprelay.Impairment{})
			e.download(64*kib, 35)
		},
	},
	{
		name:  "transfer-corruption",
		limit: 40 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			e.relay.SetBoth(udprelay.Impairment{
				Corrupt: 0.05,
				Delay:   time.Millisecond,
			})
			e.download(1*mib, 21)
			e.upload(512*kib, 22)
		},
	},
	{
		name:  "blackhole",
		limit: 30 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			e.relay.SetBoth(udprelay.Impairment{
				Delay: 2 * time.Millisecond,
			})

			start := time.Now()

			e.downloadWith(4*mib, 23, 512*kib, func() {
				e.relay.SetBoth(udprelay.Impairment{
					Blackhole: true,
				})
				time.AfterFunc(2*time.Second, func() {
					e.relay.SetBoth(udprelay.Impairment{})
				})
			})

			if d := time.Since(start); d < 2*time.Second {
				e.t.Errorf("the download ended %v after it started, inside the blackhole", d)
			}

			e.download(64*kib, 24)
		},
	},
	{
		name:  "amplification",
		limit: 20 * time.Second,
		server: backendOpts{
			bigChain: true,
		},
		run: func(e *interopEnv) {
			e.warm()
			e.download(256*kib, 25)

			// The chain is over three times the client's first flight,
			// so the server stops at its limit (quic-go checks it before
			// each packet, so it may pass 3 by one packet) and the client
			// must keep the handshake going for the rest.
			if a := e.relay.Stats().Amplification; a < 2.5 {
				e.t.Errorf("the server sent %.2f times what it received before the address was validated: the chain did not reach the limit", a)
			}
		},
	},
	{
		name:  "small-mtu",
		limit: 30 * time.Second,
		run: func(e *interopEnv) {
			e.relay.SetBoth(udprelay.Impairment{
				MaxSize: 1280,
			})
			e.warm()
			e.download(1*mib, 26)
			e.upload(512*kib, 27)
		},
	},
	{
		name:  "port-rebinding",
		limit: 30 * time.Second,
		run: func(e *interopEnv) {
			e.rebinding(false)
		},
	},
	{
		name:  "address-rebinding",
		limit: 30 * time.Second,
		server: backendOpts{
			dualStack: true,
		},
		run: func(e *interopEnv) {
			e.rebinding(true)
		},
	},
	{
		name:  "rebinding-probe-loss",
		limit: 20 * time.Second,
		server: backendOpts{
			idle: 4 * time.Second,
		},
		run: func(e *interopEnv) {
			first := e.warm()
			delay := udprelay.Impairment{
				Delay: 2 * time.Millisecond,
			}
			e.relay.SetBoth(delay)
			// The server's PATH_CHALLENGE to the new port is lost, and it
			// probes again only when the new path brings it something:
			// the client, only receiving, must speak up before the idle
			// timeout (its keep-alive with a request outstanding).
			e.downloadWith(1*mib, 36, 256*kib, func() {
				e.relay.Set(udprelay.ToClient, udprelay.Impairment{
					Blackhole: true,
				})

				if err := e.relay.RebindPort(); err != nil {
					e.t.Error(err)
				}

				time.AfterFunc(300*time.Millisecond, func() {
					e.relay.Set(udprelay.ToClient, delay)
				})
			})

			if info := e.get("/info"); info.Conn != first.Conn {
				e.t.Error("the connection did not survive the rebinding")
			}
		},
	},
	{
		name:  "goaway",
		limit: 30 * time.Second,
		run: func(e *interopEnv) {
			e.warm()
			e.relay.SetBoth(udprelay.Impairment{
				Delay: 2 * time.Millisecond,
			})

			b := e.backend(backendOpts{
				name: "B",
			})
			info, _ := e.downloadWith(4*mib, 28, 512*kib, func() {
				e.relay.SetTarget(b.target)

				go e.server.shutdown()
			})

			if info.Backend != "A" {
				e.t.Errorf("the download in flight went to %s", info.Backend)
			}

			// Firefox sends the request after a GOAWAY over TCP and
			// races a QUIC connection beside it; the others dial QUIC.
			e.untilH3From("B")
		},
	},
	{
		name:  "idle-close",
		limit: 20 * time.Second,
		server: backendOpts{
			idle: time.Second,
		},
		run: func(e *interopEnv) {
			first := e.warm()

			time.Sleep(2 * time.Second)

			info, _ := e.download(64*kib, 29)
			if info.Conn == first.Conn {
				e.t.Error("the request after the idle timeout went on the timed-out connection")
			}
		},
	},
	{
		name:  "server-close",
		limit: 20 * time.Second,
		run: func(e *interopEnv) {
			first := e.warm()
			// The server's packets take 50 ms: its CONNECTION_CLOSE, sent
			// 50 ms after the response to /close, arrives once the next
			// request has gone out on the connection it closes.
			e.relay.Set(udprelay.ToClient, udprelay.Impairment{
				Delay: 50 * time.Millisecond,
			})
			e.get("/close")
			<-e.server.closed

			info, _ := e.download(64*kib, 30)
			if info.Conn == first.Conn {
				e.t.Error("the request after the server's CONNECTION_CLOSE went on the closed connection")
			}
		},
	},
	{
		name:  "stream-reset",
		limit: 20 * time.Second,
		run: func(e *interopEnv) {
			first := e.warm()

			resp, body, err := e.do(http.MethodGet, "/reset?n=300000", nil)
			if err == nil {
				e.t.Errorf("a response the server reset after %d bytes read as complete", len(body))
			} else if resp != nil && resp.StatusCode != http.StatusOK {
				e.t.Errorf("reset response: status %d", resp.StatusCode)
			}

			// The connection stays usable.
			info, _ := e.download(256*kib, 31)
			if info.Conn != first.Conn {
				e.t.Error("a stream reset cost the connection")
			}

			// H3_REQUEST_REJECTED: sent again, on a new connection.
			info = e.get("/reject")
			if info.Conn == first.Conn {
				e.t.Error("the rejected request was answered on the connection that rejected it")
			}
		},
	},
	{
		name:  "flow-control",
		limit: 40 * time.Second,
		server: backendOpts{
			tiny: true,
		},
		run: func(e *interopEnv) {
			e.warm()
			e.upload(2*mib, 32)
			e.download(1*mib, 33)
			e.parallel(20, func(e *interopEnv, i int) {
				if i%2 == 0 {
					e.upload(64*kib, uint64(300+i))

					return
				}

				e.download(64*kib, uint64(300+i))
			})
		},
	},
	{
		// HTTP/3 through a proxy never sends a datagram to the server
		// directly (decision 0038). The proxies here forward no UDP; the
		// server's relay (e.relay) must see no client at all. With
		// HTTP3OverSOCKS5 off, and through an HTTP proxy, no UDP is sent
		// at all; with it on, the QUIC attempt's datagrams reach the
		// SOCKS5 proxy's relay, naming the host for the proxy to resolve.
		name:  "socks5-no-leak",
		limit: 20 * time.Second,
		run: func(e *interopEnv) {
			noLeak := func(what string) {
				if n := e.relay.Stats().Sessions; n != 0 {
					e.t.Fatalf("%s: %d clients sent the server datagrams directly", what, n)
				}
			}

			off := newSOCKSServer(e.t, false, "", "")
			for what, next := range map[string]*interopEnv{
				"through SOCKS5 with HTTP3OverSOCKS5 off": e.via(off.url("socks5h", nil), false, nil),
				"through an HTTP proxy":                   e.via(connectProxy(e.t), true, nil),
			} {
				for range 3 {
					next.onTCP("a request " + what)
				}

				settle(e.ctx)
				noLeak(what)
			}

			if a, d, _ := off.counts(); a != 0 || d != 0 {
				e.t.Errorf("with HTTP3OverSOCKS5 off the proxy had %d UDP ASSOCIATE requests and %d datagrams", a, d)
			}

			rec := newSOCKSServer(e.t, false, "", "")
			on := e.via(rec.url("socks5h", nil), true, nil)

			for {
				on.onTCP("a request through SOCKS5 with HTTP3OverSOCKS5 on")

				if _, d, _ := rec.counts(); d > 0 {
					break
				}

				select {
				case <-e.ctx.Done():
					e.t.Fatal("no datagram reached the SOCKS5 proxy's relay")
				case <-time.After(20 * time.Millisecond):
				}
			}

			settle(e.ctx)
			noLeak("through SOCKS5 with HTTP3OverSOCKS5 on")

			_, _, dsts := rec.counts()
			for _, dst := range dsts {
				if host, _, _ := net.SplitHostPort(dst); host != interopHost {
					e.t.Fatalf("a datagram to the relay named %s, not the host %s", dst, interopHost)
				}
			}
		},
	},
	{
		// HTTP/3 through a SOCKS5 proxy asking for credentials, socks5 and
		// socks5h alike: the host name goes to the proxy, the server
		// receives the client's first flight as it does without the proxy
		// (datagram sizes and count, through a tap in front of e.relay),
		// and downloads and, where the profile resumes, 0-RTT work.
		name:  "socks5",
		limit: 30 * time.Second,
		run: func(e *interopEnv) {
			tap := newUDPTap(e.t, e.relay.Addr())

			e.another(e.altSvcAt(tap.port())).untilH3("a request sent directly")

			direct := tap.firstFlights()[0]
			s := newSOCKSServer(e.t, true, "user", "pass")

			for i, scheme := range []string{"socks5", "socks5h"} {
				next := e.via(s.url(scheme, url.UserPassword("user", "pass")), true, e.altSvcAt(tap.port()))
				start := len(tap.firstFlights())

				next.untilH3("a request through " + scheme)

				if got := tap.firstFlights()[start]; !slices.Equal(got, direct) {
					e.t.Errorf("through %s the server received a first flight of %v bytes; directly %v", scheme, got, direct)
				}

				next.download(256*kib, uint64(40+i))

				if e.p.QUIC.ZeroRTT {
					next.newConnection()

					if info, _ := next.download(64*kib, uint64(42+i)); !info.Used0RTT {
						e.t.Errorf("through %s the resumed connection sent no 0-RTT: %+v", scheme, info)
					}
				}
			}

			_, d, dsts := s.counts()
			if d == 0 {
				e.t.Fatal("no datagram went through the SOCKS5 proxy")
			}

			for _, dst := range dsts {
				if host, _, _ := net.SplitHostPort(dst); host != interopHost {
					e.t.Fatalf("a datagram to the relay named %s, not the host %s", dst, interopHost)
				}
			}
		},
	},
	{
		// The proxy drops the association under a live HTTP/3 connection
		// (closes its control connection) and refuses new ones: the
		// connection ends with it, and the next request goes on TCP
		// through the proxy at once, not once the connection idles out.
		name:  "socks5-teardown",
		limit: 20 * time.Second,
		run: func(e *interopEnv) {
			s := newSOCKSServer(e.t, true, "", "")
			next := e.via(s.url("socks5h", nil), true, nil)

			next.warm()
			s.setRefuseUDP(true)
			s.dropAssociations()

			start := time.Now()

			next.onTCP("the request after the association ended")

			if d := time.Since(start); d > 2*time.Second {
				e.t.Errorf("the request after the association ended took %v", d)
			}
		},
	},
	{
		// A SOCKS5 proxy that refuses UDP ASSOCIATE is asked once:
		// requests to every origin through it go on TCP from then on.
		name:  "socks5-refused",
		limit: 20 * time.Second,
		run: func(e *interopEnv) {
			s := newSOCKSServer(e.t, true, "", "")
			s.setRefuseUDP(true)

			next := e.via(s.url("socks5h", nil), true, nil)
			for range 3 {
				next.onTCP("a request through a proxy refusing UDP")
			}

			e.waitFor("a UDP ASSOCIATE request", func() bool {
				a, _, _ := s.counts()
				return a > 0
			})
			settle(e.ctx)

			other := *next
			_, port, _ := net.SplitHostPort(e.altSvcServer())

			other.origin = "https://" + interopHost + ":" + port

			var err error
			if other.page, err = url.Parse(other.origin + "/"); err != nil {
				e.t.Fatal(err)
			}

			for range 3 {
				other.onTCP("a request to another origin through the proxy")
			}

			settle(e.ctx)

			if a, _, _ := s.counts(); a != 1 {
				e.t.Errorf("the proxy was asked for %d UDP associations, want 1", a)
			}
		},
	},
}

// interopEnv is one case's world: a backend behind the relay, the TCP
// server that advertises it, and a mimicry client.
type interopEnv struct {
	t      *testing.T
	ctx    context.Context
	p      *profile.Profile
	pki    *interopPKI
	relay  *udprelay.Relay
	server *backend
	client *http.Client
	tr     *mimicry.Transport
	origin string
	// page is the origin's page, every request's fetch() caller.
	page *url.URL
	seed uint64
	// backends are the case's backends, server first.
	backends []*backend
	// cfg is the client's Config; tcpConns counts the TCP server's
	// connections.
	cfg      mimicry.Config
	tcpConns *atomic.Int64
}

func newInteropEnv(t *testing.T, p *profile.Profile, pki *interopPKI, c interopCase) *interopEnv {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), c.limit)
	t.Cleanup(cancel)

	h := fnv.New64a()

	_, _ = h.Write([]byte(p.Name + "/" + c.name))

	e := &interopEnv{
		t:        t,
		ctx:      ctx,
		p:        p,
		pki:      pki,
		seed:     h.Sum64(),
		tcpConns: &atomic.Int64{},
	}
	opts := c.server

	opts.name = "A"
	e.server = e.backend(opts)

	relay, err := udprelay.New(e.server.target, e.seed)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = relay.Close()
	})

	e.relay = relay
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}

		t.Logf("relay: %+v", relay.Stats())

		for _, b := range e.backends {
			b.mu.Lock()
			t.Logf("backend %s: %d connections, ended: %q", b.name, b.conns.Load(), b.ends)
			b.mu.Unlock()
		}
	})

	tcp := e.altSvcServer()
	cfg := mimicry.Config{
		Profile:     p,
		RootCAs:     pki.roots,
		DialTimeout: c.limit,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, tcp)
		},
		Resolver: stubResolver(t),
	}

	if c.config != nil {
		c.config(&cfg)
	}

	e.cfg = cfg
	e.setTransport(cfg)

	_, port, _ := net.SplitHostPort(tcp)

	e.origin = "https://" + interopHost + ":" + port

	if e.page, err = url.Parse(e.origin + "/"); err != nil {
		t.Fatal(err)
	}

	return e
}

// setTransport gives e a new Transport built from cfg.
func (e *interopEnv) setTransport(cfg mimicry.Config) {
	tr, err := mimicry.NewTransport(cfg)
	if err != nil {
		e.t.Fatal(err)
	}

	e.t.Cleanup(tr.CloseIdleConnections)

	e.tr = tr
	e.client = &http.Client{
		Transport: tr,
	}
}

// another returns e with a new Transport of its Config but for cache, as
// another tab or a restarted browser would be.
func (e *interopEnv) another(cache *mimicry.AltSvcCache) *interopEnv {
	next := *e

	next.cfg.AltSvc = cache
	next.setTransport(next.cfg)

	return &next
}

// altSvcServer starts the origin's TCP server (HTTP/1.1, so no profile
// keeps an HTTP/2 connection instead of moving), whose every response but
// those to a ?noalt request advertises the relay as the origin's HTTP/3,
// and returns its address.
func (e *interopEnv) altSvcServer() string {
	t := e.t

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	alt := fmt.Sprintf(`h3=":%d"; ma=86400`, e.relay.Addr().Port())
	h := interopHandler(nil)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !r.URL.Query().Has("noalt") {
				w.Header().Set("Alt-Svc", alt)
			}

			h.ServeHTTP(w, r)
		}),
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateNew {
				e.tcpConns.Add(1)
			}
		},
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				e.pki.leaf,
			},
			NextProtos: []string{
				"http/1.1",
			},
			MinVersion: tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}
	done := make(chan error, 1)

	go func() {
		done <- srv.ServeTLS(ln, "", "")
	}()

	t.Cleanup(func() {
		_ = srv.Close()

		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	})

	return ln.Addr().String()
}

// connInfo is what a backend tells about the connection a response went
// on (the X-Info header).
type connInfo struct {
	Backend string
	// Conn numbers the backend's connections.
	Conn     int64
	Resumed  bool
	Used0RTT bool
	// Early: the request arrived before the handshake completed.
	Early  bool
	Cipher uint16
	Remote string
}

// send sends req as a fetch() from the origin's page.
func (e *interopEnv) send(req *http.Request) (*http.Response, error) {
	return e.client.Do(mimicry.With(req, mimicry.Request{
		Kind: mimicry.Fetch,
		From: e.page,
	}))
}

// do sends a request with the case's deadline and reads the whole body.
func (e *interopEnv) do(method, path string, body []byte) (*http.Response, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(e.ctx, method, e.origin+path, rd)
	if err != nil {
		return nil, nil, err
	}

	resp, err := e.send(req)
	if err != nil {
		return nil, nil, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	b, err := io.ReadAll(resp.Body)

	return resp, b, err
}

// h3 checks resp came over HTTP/3, from a backend, and returns its
// connection.
func (e *interopEnv) h3(resp *http.Response, what string) connInfo {
	e.t.Helper()

	if resp.ProtoMajor != 3 {
		e.t.Fatalf("%s went over %s, not HTTP/3", what, resp.Proto)
	}

	var info connInfo
	if err := json.Unmarshal([]byte(resp.Header.Get("X-Info")), &info); err != nil {
		e.t.Fatalf("%s: X-Info: %v", what, err)
	}

	return info
}

// get sends GET path, which must succeed over HTTP/3.
func (e *interopEnv) get(path string) connInfo {
	e.t.Helper()

	resp, _, err := e.do(http.MethodGet, path, nil)
	if err != nil {
		e.t.Fatalf("GET %s: %v", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}

	return e.h3(resp, "GET "+path)
}

// warm asks over TCP until a response comes over HTTP/3, matching how each
// browser moves to the Alt-Svc connection.
func (e *interopEnv) warm() connInfo {
	e.t.Helper()

	for {
		resp, _, err := e.do(http.MethodGet, "/info", nil)
		if err != nil {
			e.t.Fatalf("warming up: %v", err)
		}

		if resp.ProtoMajor == 3 {
			return e.h3(resp, "GET /info")
		}

		select {
		case <-e.ctx.Done():
			e.t.Fatal("the origin never moved to HTTP/3")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// warmImpaired is warm for a case that impairs the handshake. A profile
// with Fallback.AbandonOnTCP (Safari) loses the race whenever the handshake
// needs a probe and gives up after BrokenAfter attempts: then every request
// must go over TCP and it returns false. Otherwise it returns true.
func (e *interopEnv) warmImpaired() bool {
	e.t.Helper()

	fb := e.p.HTTP3.Discovery.Fallback
	if !fb.AbandonOnTCP {
		e.warm()

		return true
	}

	// The race, and the TCP handshake after it.
	limit := time.Duration(fb.TCPDelayMillis.Max)*time.Millisecond + 500*time.Millisecond
	for e.relay.Stats().Sessions < fb.BrokenAfter {
		start := time.Now()

		resp, _, err := e.do(http.MethodGet, "/info", nil)
		if err != nil {
			e.t.Fatalf("warming up: %v", err)
		}

		if resp.ProtoMajor == 3 {
			e.h3(resp, "GET /info")

			return true
		}

		if d := time.Since(start); d > limit {
			e.t.Errorf("a request took %v over TCP: it waited past the race (%v)", d, limit)
		}

		select {
		case <-e.ctx.Done():
			e.t.Fatal("no QUIC attempt")
		case <-time.After(20 * time.Millisecond):
		}
	}

	for i := range 5 {
		resp, _, err := e.do(http.MethodGet, "/info", nil)
		if err != nil {
			e.t.Fatalf("request %d after the attempts: %v", i, err)
		}

		if resp.ProtoMajor == 3 {
			// The last attempt won its race after all.
			e.h3(resp, "GET /info")

			return true
		}
	}

	e.t.Logf("every QUIC attempt lost the race to TCP; the origin is broken after %d", fb.BrokenAfter)

	if st := e.relay.Stats(); st.Sessions != fb.BrokenAfter {
		e.t.Errorf("%d QUIC attempts; the origin is broken after %d", st.Sessions, fb.BrokenAfter)
	}

	return false
}

// untilH3From asks until a response comes over HTTP/3 from backend name;
// every response must be the backend's or the TCP server's.
func (e *interopEnv) untilH3From(name string) {
	e.t.Helper()

	for {
		resp, body, err := e.do(http.MethodGet, "/bytes?n=1000&seed=7", nil)
		if err != nil {
			e.t.Fatalf("waiting for HTTP/3 from %s: %v", name, err)
		}

		if !bytes.Equal(body, interopBytes(1000, 7)) {
			e.t.Fatalf("waiting for HTTP/3 from %s: wrong body over %s", name, resp.Proto)
		}

		if resp.ProtoMajor == 3 {
			if info := e.h3(resp, "GET"); info.Backend == name {
				return
			}
		}

		select {
		case <-e.ctx.Done():
			e.t.Fatalf("no response over HTTP/3 from %s", name)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// newConnection closes the client's idle connections, so the next request
// dials a new one (resumed where the profile resumes).
func (e *interopEnv) newConnection() {
	// A connection's first response may come before its session ticket
	// is stored; one more request round trip makes sure.
	e.get("/info")
	e.tr.CloseIdleConnections()
}

// download fetches n bytes of seed's stream over HTTP/3 and checks them.
func (e *interopEnv) download(n int, seed uint64) (connInfo, []byte) {
	e.t.Helper()

	return e.downloadWith(n, seed, -1, nil)
}

// downloadWith is download, calling fn once the body has reached at bytes.
func (e *interopEnv) downloadWith(n int, seed uint64, at int, fn func()) (connInfo, []byte) {
	e.t.Helper()

	what := fmt.Sprintf("download of %d bytes", n)

	req, err := http.NewRequestWithContext(e.ctx, http.MethodGet, fmt.Sprintf("%s/bytes?n=%d&seed=%d", e.origin, n, seed), nil)
	if err != nil {
		e.t.Fatal(err)
	}

	resp, err := e.send(req)
	if err != nil {
		e.t.Fatalf("%s: %v", what, err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	info := e.h3(resp, what)

	var r io.Reader = resp.Body

	if fn != nil {
		r = &trigger{
			r:  resp.Body,
			at: at,
			fn: fn,
		}
	}

	got, err := io.ReadAll(r)
	if err != nil {
		e.t.Fatalf("%s: after %d bytes: %v", what, len(got), err)
	}

	if want := interopBytes(n, seed); !bytes.Equal(got, want) {
		e.t.Fatalf("%s: got %d bytes, %s", what, len(got), firstDiff(got, want))
	}

	return info, got
}

// upload posts n bytes of seed's stream and checks the server's digest.
func (e *interopEnv) upload(n int, seed uint64) connInfo {
	e.t.Helper()

	what := fmt.Sprintf("upload of %d bytes", n)
	data := interopBytes(n, seed)

	resp, body, err := e.do(http.MethodPost, "/upload", data)
	if err != nil {
		e.t.Fatalf("%s: %v", what, err)
	}

	info := e.h3(resp, what)
	sum := sha256.Sum256(data)

	if want := fmt.Sprintf("%d %x", n, sum); string(body) != want {
		e.t.Fatalf("%s: the server read %q, want %q", what, body, want)
	}

	return info
}

// parallel runs fn for 0 to n-1 at once, each with the case's env in a
// subtest of its own, and waits for them.
func (e *interopEnv) parallel(n int, fn func(e *interopEnv, i int)) {
	e.t.Helper()
	// Subtests, so each may fail on its own goroutine; Run returns once
	// they all have.
	e.t.Run("parallel", func(t *testing.T) {
		for i := range n {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()

				sub := *e

				sub.t = t
				fn(&sub, i)
			})
		}
	})
}

// rebinding moves the client's apparent port (or address) in the middle of
// a download: the server validates the new path with PATH_CHALLENGE, and
// moves to it only once the client answered.
func (e *interopEnv) rebinding(address bool) {
	e.t.Helper()

	first := e.warm()
	e.relay.SetBoth(udprelay.Impairment{
		Delay: 2 * time.Millisecond,
	})
	e.downloadWith(2*mib, 34, 512*kib, func() {
		var err error
		if address {
			err = e.relay.RebindAddress()
		} else {
			err = e.relay.RebindPort()
		}

		if err != nil {
			e.t.Error(err)
		}
	})

	info := e.get("/info")
	if info.Conn != first.Conn {
		e.t.Fatal("the connection did not survive the rebinding")
	}

	if info.Remote == first.Remote {
		e.t.Fatalf("the server still sends to %s: the new path was never validated", info.Remote)
	}

	want := "127.0.0.1"
	if address {
		want = "::1"
	}

	if ap, err := netip.ParseAddrPort(info.Remote); err != nil || ap.Addr().Unmap().String() != want {
		e.t.Fatalf("the server's path is %s, want an address %s", info.Remote, want)
	}
}

// trigger calls fn once its reader has passed at bytes.
type trigger struct {
	r    io.Reader
	at   int
	n    int
	fn   func()
	done bool
}

func (t *trigger) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)

	t.n += n

	if !t.done && t.n >= t.at {
		t.done = true
		t.fn()
	}

	return n, err
}

// interopBytes is n bytes of a stream seeded with seed.
func interopBytes(n int, seed uint64) []byte {
	var key [32]byte
	binary.LittleEndian.PutUint64(key[:8], seed)

	b := make([]byte, n)

	_, _ = mrand.NewChaCha8(key).Read(b)

	return b
}

func firstDiff(got, want []byte) string {
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			return fmt.Sprintf("first difference at byte %d of %d", i, len(want))
		}
	}

	return fmt.Sprintf("want %d", len(want))
}

// backendOpts configures a backend.
type backendOpts struct {
	// name is the backend's in X-Info.
	name string
	// retry: every connection must validate its address with a Retry.
	retry bool
	// no0RTT: sessions resume without early data.
	no0RTT bool
	// idle is the server's max_idle_timeout (default 30 s).
	idle time.Duration
	// tiny: tiny flow control windows and 2 concurrent request streams.
	tiny bool
	// versions are the QUIC versions the server accepts (default quic-go's,
	// 1 and 2); a client in another answers with Version Negotiation.
	versions []quic.Version
	// streamWindow and connWindow, when set, are the server's stream and
	// connection receive windows, initial and maximum.
	streamWindow, connWindow int
	// bigChain: a certificate chain several times larger than the client's
	// first flight, so the anti-amplification limit holds the server's.
	bigChain bool
	// chacha: a server without AES hardware, which prefers ChaCha20 (in a
	// child process, GODEBUG=cpu.aes=off).
	chacha bool
	// dualStack: an IPv6 loopback socket besides the IPv4 one, for the
	// client's address to move to.
	dualStack bool
}

// backend is quic-go's HTTP/3 server on a dual-stack socket.
type backend struct {
	name   string
	target udprelay.Target
	srv    *http3.Server
	conns  atomic.Int64
	// keyUpdates counts the 1-RTT key updates the server started.
	keyUpdates atomic.Int64
	// ends are how the server's connections ended, for a failure's
	// message.
	mu   sync.Mutex
	ends []string
	// closed is closed when /close has closed its connection.
	closed    chan struct{}
	closeOnce sync.Once
	rejected  atomic.Bool
	// streamBlocked counts the STREAM_DATA_BLOCKED frames received in
	// 1-RTT packets.
	streamBlocked atomic.Int64
	// tooEarly counts the requests answered 425 Too Early.
	tooEarly atomic.Int64
}

// backend starts a backend for the case, stopped when it ends.
func (e *interopEnv) backend(opts backendOpts) *backend {
	t := e.t
	t.Helper()

	if opts.chacha {
		b := e.childBackend(opts)

		e.backends = append(e.backends, b)

		return b
	}

	cert := e.pki.leaf
	if opts.bigChain {
		cert = e.pki.bigChain
	}

	b, stop, err := startBackend(opts, cert, e.pki.ticketKey)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(stop)

	e.backends = append(e.backends, b)

	return b
}

// h3Stream takes over w's HTTP/3 stream. The backend's handlers run under
// http3.Server, whose ResponseWriters are all HTTPStreamers.
func h3Stream(w http.ResponseWriter) *http3.Stream {
	s, ok := w.(http3.HTTPStreamer)
	if !ok {
		panic(fmt.Sprintf("h3Stream: %T is not an http3.HTTPStreamer", w))
	}

	return s.HTTPStream()
}

// startBackend starts a backend; stop closes it.
func startBackend(opts backendOpts, cert tls.Certificate, ticketKey [32]byte) (*backend, func(), error) {
	// Loopback addresses, never a wildcard: a wildcard socket shares its
	// port with any socket bound to a loopback address, which then takes
	// its datagrams (another case's relay, say).
	v4, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP: net.IPv4(127, 0, 0, 1),
	})
	if err != nil {
		return nil, nil, err
	}

	b := &backend{
		name: opts.name,
		target: udprelay.Target{
			IPv4: netip.MustParseAddrPort(v4.LocalAddr().String()),
		},
		closed: make(chan struct{}),
	}

	var pc net.PacketConn = v4

	if opts.dualStack {
		v6, err := net.ListenUDP("udp6", &net.UDPAddr{
			IP: net.IPv6loopback,
		})
		if err != nil {
			_ = v4.Close()
			return nil, nil, err
		}

		b.target.IPv6 = netip.MustParseAddrPort(v6.LocalAddr().String())
		pc = newDualConn(v4, v6)
	}

	tlsConf := http3.ConfigureTLSConfig(&tls.Config{
		Certificates: []tls.Certificate{
			cert,
		},
		MinVersion: tls.VersionTLS13,
	})
	tlsConf.SetSessionTicketKeys([][32]byte{
		ticketKey,
	})

	qconf := &quic.Config{
		MaxIdleTimeout: cmp.Or(opts.idle, 30*time.Second),
		Allow0RTT:      !opts.no0RTT,
		Tracer:         b.tracer,
		Versions:       opts.versions,
	}
	if opts.streamWindow > 0 {
		qconf.InitialStreamReceiveWindow = uint64(opts.streamWindow)
		qconf.MaxStreamReceiveWindow = uint64(opts.streamWindow)
	}

	if opts.connWindow > 0 {
		qconf.InitialConnectionReceiveWindow = uint64(opts.connWindow)
		qconf.MaxConnectionReceiveWindow = uint64(opts.connWindow)
	}

	if opts.tiny {
		qconf.InitialStreamReceiveWindow = 8 * kib
		qconf.MaxStreamReceiveWindow = 8 * kib
		qconf.InitialConnectionReceiveWindow = 16 * kib
		qconf.MaxConnectionReceiveWindow = 16 * kib
		qconf.MaxIncomingStreams = 2
	}

	tr := &quic.Transport{
		Conn: pc,
	}
	if opts.retry {
		tr.VerifySourceAddress = func(net.Addr) bool {
			return true
		}
	}

	ln, err := tr.ListenEarly(tlsConf, qconf)
	if err != nil {
		_ = pc.Close()
		return nil, nil, err
	}

	b.srv = &http3.Server{
		Handler: interopHandler(b),
		ConnContext: func(ctx context.Context, c *quic.Conn) context.Context {
			return context.WithValue(ctx, serverConnKey{}, &serverConn{
				qc: c,
				id: b.conns.Add(1),
			})
		},
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = b.srv.ServeListener(ln)
	}()

	stop := func() {
		_ = b.srv.Close()
		_ = ln.Close()
		_ = tr.Close()
		_ = pc.Close()

		<-done
	}

	return b, stop, nil
}

// dualConn is one PacketConn over an IPv4 and an IPv6 socket: it reads
// from both and writes each datagram from the socket of its address's
// family, so a server on it sees its client move between the two.
type dualConn struct {
	v4, v6 *net.UDPConn
	in     chan dualPacket
	done   chan struct{}
	once   sync.Once

	mu       sync.Mutex
	deadline time.Time
	wake     chan struct{}
}

type dualPacket struct {
	b    []byte
	from net.Addr
}

func newDualConn(v4, v6 *net.UDPConn) *dualConn {
	c := &dualConn{
		v4:   v4,
		v6:   v6,
		in:   make(chan dualPacket, 256),
		done: make(chan struct{}),
		wake: make(chan struct{}),
	}
	for _, s := range []*net.UDPConn{v4, v6} {
		go func() {
			for {
				buf := make([]byte, 64*kib)

				n, from, err := s.ReadFrom(buf)
				if err != nil {
					return
				}

				select {
				case c.in <- dualPacket{
					b:    buf[:n],
					from: from,
				}:
				case <-c.done:
					return
				}
			}
		}()
	}

	return c
}

func (c *dualConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, from, again, err := c.readOnce(p)
		if !again {
			return n, from, err
		}
	}
}

// readOnce waits for a datagram until the read deadline; again: the
// deadline changed meanwhile.
func (c *dualConn) readOnce(p []byte) (n int, from net.Addr, again bool, err error) {
	c.mu.Lock()

	deadline, wake := c.deadline, c.wake
	c.mu.Unlock()

	var expired <-chan time.Time
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return 0, nil, false, os.ErrDeadlineExceeded
		}

		t := time.NewTimer(d)

		defer t.Stop()

		expired = t.C
	}

	select {
	case pkt := <-c.in:
		return copy(p, pkt.b), pkt.from, false, nil
	case <-c.done:
		return 0, nil, false, net.ErrClosed
	case <-expired:
		return 0, nil, false, os.ErrDeadlineExceeded
	case <-wake:
		return 0, nil, true, nil
	}
}

func (c *dualConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, fmt.Errorf("dualConn: address %v", addr)
	}

	if ua.IP.To4() != nil {
		return c.v4.WriteTo(p, addr)
	}

	return c.v6.WriteTo(p, addr)
}

func (c *dualConn) Close() error {
	c.once.Do(func() {
		close(c.done)
	})

	return errors.Join(c.v4.Close(), c.v6.Close())
}

func (c *dualConn) LocalAddr() net.Addr {
	return c.v4.LocalAddr()
}

func (c *dualConn) SetDeadline(t time.Time) error {
	return c.SetReadDeadline(t)
}

// SetReadDeadline wakes a waiting ReadFrom, which takes up the new one.
func (c *dualConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.deadline = t
	close(c.wake)

	c.wake = make(chan struct{})

	return nil
}

func (c *dualConn) SetWriteDeadline(time.Time) error {
	return nil
}

// SetReadBuffer and SetWriteBuffer size both sockets' buffers, as quic-go
// asks.
func (c *dualConn) SetReadBuffer(n int) error {
	return errors.Join(c.v4.SetReadBuffer(n), c.v6.SetReadBuffer(n))
}

func (c *dualConn) SetWriteBuffer(n int) error {
	return errors.Join(c.v4.SetWriteBuffer(n), c.v6.SetWriteBuffer(n))
}

// shutdown sends GOAWAY and closes the backend once its requests are done.
func (b *backend) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_ = b.srv.Shutdown(ctx)
}

// tracer is the backend's qlog: it counts the server's own 1-RTT key
// updates and keeps how each connection ended, and writes every event to
// $QLOGDIR when it is set.
func (b *backend) tracer(ctx context.Context, isClient bool, id quic.ConnectionID) qlogwriter.Trace {
	return &serverTrace{
		b:     b,
		inner: qlog.DefaultConnectionTracer(ctx, isClient, id),
	}
}

type serverTrace struct {
	b     *backend
	inner qlogwriter.Trace
}

func (t *serverTrace) AddProducer() qlogwriter.Recorder {
	r := &serverRecorder{
		b: t.b,
	}
	if t.inner != nil {
		r.inner = t.inner.AddProducer()
	}

	return r
}

func (t *serverTrace) SupportsSchemas(schema string) bool {
	return schema == qlog.EventSchema
}

type serverRecorder struct {
	b     *backend
	inner qlogwriter.Recorder
}

func (r *serverRecorder) RecordEvent(ev qlogwriter.Event) {
	switch ev := ev.(type) {
	case qlog.KeyUpdated:
		if ev.KeyType == qlog.KeyTypeServer1RTT && ev.Trigger == qlog.KeyUpdateLocal {
			r.b.keyUpdates.Add(1)
		}

	case qlog.PacketReceived:
		if ev.Header.PacketType != qlog.PacketType1RTT {
			break
		}

		for _, f := range ev.Frames {
			if _, ok := f.Frame.(*qlog.StreamDataBlockedFrame); ok {
				r.b.streamBlocked.Add(1)
			}
		}

	case qlog.ConnectionClosed:
		r.b.mu.Lock()

		r.b.ends = append(r.b.ends, fmt.Sprintf("%s %s %q", ev.Initiator, ev.Trigger, ev.Reason))
		r.b.mu.Unlock()
	}

	if r.inner != nil {
		r.inner.RecordEvent(ev)
	}
}

func (r *serverRecorder) Close() error {
	if r.inner != nil {
		return r.inner.Close()
	}

	return nil
}

type serverConnKey struct{}

type serverConn struct {
	qc *quic.Conn
	id int64
}

// HTTP/3 error codes (RFC 9114, section 8.1).
const (
	h3NoError          = 0x100
	h3RequestRejected  = 0x10b
	h3RequestCancelled = 0x10c
)

// interopHandler serves the cases' requests; b is nil on the TCP server.
//
//	/info            nothing but X-Info
//	/bytes?n=&seed=  n bytes of seed's stream
//	/upload          the body's length and SHA-256
//	/reset?n=        n bytes, then the stream reset (H3_REQUEST_CANCELLED)
//	/reject          H3_REQUEST_REJECTED on the backend's first one
//	/close           a response, then CONNECTION_CLOSE (H3_NO_ERROR)
//	/too-early       425 Too Early if it arrived before the handshake completed
func interopHandler(b *backend) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, _ := r.Context().Value(serverConnKey{}).(*serverConn)

		var info connInfo

		if sc != nil {
			info = infoOf(b, sc)

			j, _ := json.Marshal(info)
			w.Header().Set("X-Info", string(j))
		}

		q := r.URL.Query()
		n, _ := strconv.Atoi(q.Get("n"))

		switch r.URL.Path {
		case "/info":
		case "/bytes":
			seed, _ := strconv.ParseUint(q.Get("seed"), 10, 64)

			w.Header().Set("Content-Length", strconv.Itoa(n))

			_, _ = w.Write(interopBytes(n, seed))

		case "/upload":
			h := sha256.New()

			m, err := io.Copy(h, r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)

				return
			}

			_, _ = fmt.Fprintf(w, "%d %x", m, h.Sum(nil))

		case "/reset":
			if sc == nil {
				w.WriteHeader(http.StatusNotFound)

				return
			}

			w.WriteHeader(http.StatusOK)

			_, _ = w.Write(interopBytes(n, 1))
			_ = http.NewResponseController(w).Flush()

			str := h3Stream(w)
			str.CancelWrite(h3RequestCancelled)
			str.CancelRead(h3RequestCancelled)

		case "/reject":
			if sc != nil && b.rejected.CompareAndSwap(false, true) {
				str := h3Stream(w)
				str.CancelWrite(h3RequestRejected)
				str.CancelRead(h3RequestRejected)
			}

		case "/close":
			if sc == nil {
				w.WriteHeader(http.StatusNotFound)

				return
			}

			_ = http.NewResponseController(w).Flush()

			go func() {
				time.Sleep(50 * time.Millisecond)

				_ = sc.qc.CloseWithError(h3NoError, "")

				b.closeOnce.Do(func() {
					close(b.closed)
				})
			}()

		case "/too-early":
			if info.Early {
				b.tooEarly.Add(1)
				w.WriteHeader(http.StatusTooEarly)
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// infoOf is sc's connInfo, as of now.
func infoOf(b *backend, sc *serverConn) connInfo {
	cs := sc.qc.ConnectionState()
	info := connInfo{
		Backend:  b.name,
		Conn:     sc.id,
		Resumed:  cs.TLS.DidResume,
		Used0RTT: cs.Used0RTT,
		Cipher:   cs.TLS.CipherSuite,
		Remote:   sc.qc.RemoteAddr().String(),
	}

	select {
	case <-sc.qc.HandshakeComplete():
	default:
		info.Early = true
	}

	return info
}

// interopPKI is the cases' certificates: a root the client trusts, the
// origin's leaf under it, and a large chain (leaf and padded
// intermediates) for the amplification case.
type interopPKI struct {
	roots     *x509.CertPool
	leaf      tls.Certificate
	bigChain  tls.Certificate
	ticketKey [32]byte
	// dir holds the leaf's PEM files, for the child process.
	dir string
}

// bigChainIntermediates and bigChainPad size the large chain: about 16 KB,
// over four times what any profile's first flight lets the server send.
const (
	bigChainIntermediates = 4
	bigChainPad           = 3000
)

func newInteropPKI(t *testing.T) *interopPKI {
	t.Helper()

	now := time.Now()
	rootKey, rootCert := mustCert(t, nil, nil, "interop root", nil, true, 0, now)
	pki := &interopPKI{
		roots: x509.NewCertPool(),
		dir:   t.TempDir(),
	}
	pki.roots.AddCert(rootCert)

	if _, err := rand.Read(pki.ticketKey[:]); err != nil {
		t.Fatal(err)
	}

	leafKey, leafCert := mustCert(t, rootKey, rootCert, interopHost, []string{interopHost}, false, 0, now)

	pki.leaf = tls.Certificate{
		Certificate: [][]byte{
			leafCert.Raw,
		},
		PrivateKey: leafKey,
		Leaf:       leafCert,
	}
	// leaf <- intermediate n <- ... <- intermediate 1 <- root.
	parentKey, parentCert := rootKey, rootCert

	var chain [][]byte

	for i := range bigChainIntermediates {
		parentKey, parentCert = mustCert(t, parentKey, parentCert, fmt.Sprintf("interop intermediate %d", i+1), nil, true, bigChainPad, now)
		chain = append([][]byte{parentCert.Raw}, chain...)
	}

	bigKey, bigLeaf := mustCert(t, parentKey, parentCert, interopHost, []string{interopHost}, false, bigChainPad, now)

	pki.bigChain = tls.Certificate{
		Certificate: append([][]byte{bigLeaf.Raw}, chain...),
		PrivateKey:  bigKey,
		Leaf:        bigLeaf,
	}

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: leafCert.Raw,
	})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	for name, data := range map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	} {
		if err := os.WriteFile(filepath.Join(pki.dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return pki
}

// mustCert issues a certificate for cn (self-signed without a parent),
// padded with a non-critical extension of pad bytes.
func mustCert(t *testing.T, parentKey *ecdsa.PrivateKey, parent *x509.Certificate, cn string, dns []string, ca bool, pad int, now time.Time) (*ecdsa.PrivateKey, *x509.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: cn,
		},
		DNSNames:              dns,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  ca,
		KeyUsage:              x509.KeyUsageDigitalSignature,
	}
	if ca {
		tmpl.KeyUsage |= x509.KeyUsageCertSign
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		}
	}

	if pad > 0 {
		tmpl.ExtraExtensions = []pkix.Extension{
			{
				// A private OID (Go's own arc), ignored by verifiers.
				Id:    asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 99, 1},
				Value: bytes.Repeat([]byte{0x04}, pad),
			},
		}
	}

	if parent == nil {
		parent, parentKey = tmpl, key
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return key, cert
}

// interopChildEnv makes the test binary a backend (TestMain): its value is
// the directory with the leaf's PEM files.
const interopChildEnv = "MIMICRY_INTEROP_BACKEND"

// childBackend starts a backend in a child process with AES hardware
// support off, so crypto/tls prefers ChaCha20-Poly1305 (Go's server picks
// AES-GCM only when it has the hardware and the client lists it first, as
// every profile does). It serves until the case ends.
func (e *interopEnv) childBackend(opts backendOpts) *backend {
	t := e.t
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$") //nolint:gosec // the test binary itself

	cmd.Env = append(os.Environ(), interopChildEnv+"="+e.pki.dir, "GODEBUG=cpu.aes=off", "MIMICRY_INTEROP_NAME="+opts.name)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("child backend: %v", err)
	}

	port, err := strconv.ParseUint(strings.TrimSpace(line), 10, 16)
	if err != nil {
		t.Fatalf("child backend said %q", line)
	}

	return &backend{
		name: opts.name,
		target: udprelay.Target{
			IPv4: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port)),
		},
		closed: make(chan struct{}),
	}
}

// runChildBackend is the child process of childBackend: it serves a
// backend with dir's certificate, prints its port, and stops when its
// standard input closes.
func runChildBackend(dir string) int {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	b, stop, err := startBackend(backendOpts{
		name: os.Getenv("MIMICRY_INTEROP_NAME"),
	}, cert, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}
	defer stop()

	fmt.Println(b.target.IPv4.Port())

	_, _ = io.Copy(io.Discard, os.Stdin)

	return 0
}

func TestMain(m *testing.M) {
	if dir := os.Getenv(interopChildEnv); dir != "" {
		os.Exit(runChildBackend(dir))
	}

	os.Exit(m.Run())
}
