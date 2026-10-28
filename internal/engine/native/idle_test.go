package native

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	utls "github.com/roachadam/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/profiles"
)

// serveH2Events answers every request on conn with 200 and no body, and
// reports on events "goaway" when the client sends GOAWAY and "closed"
// once the connection ends.
func serveH2Events(events chan<- string) func(context.Context, net.Conn) error {
	return func(ctx context.Context, conn net.Conn) error {
		tc, err := tlsServer(ctx, conn)
		if err != nil {
			return err
		}

		defer func() {
			events <- "closed"
		}()

		if _, err := io.ReadFull(tc, make([]byte, len(http2.ClientPreface))); err != nil {
			return err
		}

		out := make(chan []byte, 64)

		defer close(out)

		go func() {
			for b := range out {
				_, _ = tc.Write(b)
			}
		}()

		fr := http2.NewFramer(queueWriter(out), tc)

		fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)

		if err := fr.WriteSettings(); err != nil {
			return err
		}

		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return nil
			}

			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					err = fr.WriteSettingsAck()
				}

			case *http2.MetaHeadersFrame:
				err = writeOK(fr, f.StreamID)
			case *http2.GoAwayFrame:
				events <- "goaway"
			}

			if err != nil {
				return err
			}
		}
	}
}

// quicServerConns accepts QUIC connections on a local port and hands each
// on conns.
func quicServerConns(t *testing.T) (uint16, <-chan *quic.Conn) {
	t.Helper()

	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", ":0")
	if err != nil {
		t.Fatal(err)
	}

	cert := serverCert.Load()

	ep, err := quic.NewEndpoint(pc, &quic.Config{
		TLSConfig: &utls.Config{
			Certificates: []utls.Certificate{{
				Certificate: cert.Certificate,
				PrivateKey:  cert.PrivateKey,
			}},
			NextProtos: []string{"h3"},
			MinVersion: utls.VersionTLS13,
		},
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

	conns := make(chan *quic.Conn, 16)

	go func() {
		for {
			c, err := ep.Accept(context.Background())
			if err != nil {
				return
			}

			conns <- c
		}
	}()

	return ep.LocalAddr().Port(), conns
}

// next is the next event, failing t after 5 s.
func next[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s", what)

		var zero T

		return zero
	}
}

// none fails t if ch delivers within a moment.
func none[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()

	select {
	case v := <-ch:
		t.Fatalf("%s: %v", what, v)
	case <-time.After(100 * time.Millisecond):
	}
}

// An idle connection closes Config.IdleConnTimeout (zero: idleTimeout)
// after its last request ended, as the profile's browser lets a
// connection go (decision 0040): HTTP/2 with the close alone (Chrome) or
// GOAWAY first (Firefox, goawayTcp), HTTP/3 silently, as at a QUIC idle
// timeout; a negative IdleConnTimeout never closes it. Fake clock.
func TestIdleConnectionsExpire(t *testing.T) {
	for _, tc := range []struct {
		name string
		h3   bool
		// idle is Config.IdleConnTimeout.
		idle time.Duration
		// firefox runs Firefox's profile, else Chrome's; want is what
		// the server sees once the timeout passed.
		firefox bool
		want    []string
	}{
		{
			name: "http2, chrome",
			want: []string{
				"closed",
			},
		},
		{
			name:    "http2, firefox",
			firefox: true,
			want: []string{
				"goaway",
				"closed",
			},
		},
		{
			name: "http2, 30 s",
			idle: 30 * time.Second,
			want: []string{
				"closed",
			},
		},
		{
			name: "http2, never",
			idle: -1,
		},
		{
			name: "http3",
			h3:   true,
		},
		{
			name: "http3, 30 s",
			h3:   true,
			idle: 30 * time.Second,
		},
		{
			name: "http3, never",
			h3:   true,
			idle: -1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := clockwork.NewFakeClock()
			events := make(chan string, 8)
			n := newFakeNet(t, serveH2Events(events), serveH2Events(events))

			p := profiles.Chrome()
			if tc.firefox {
				p = profiles.Firefox()
			}

			e := newTestEngine(t, options{
				profile:         p,
				dial:            n.dial,
				clock:           clock,
				idleConnTimeout: tc.idle,
			})

			timeout := tc.idle
			if timeout == 0 {
				timeout = idleTimeout
			}

			if tc.h3 {
				port, conns := quicServerConns(t)
				r, a := dialQUIC(t, e, "https://localhost/", port)

				if a.conn == nil {
					t.Fatal("the QUIC attempt failed")
				}

				server := next(t, conns, "QUIC connection at the server")

				if timeout < 0 {
					clock.Advance(100 * idleTimeout)
					none(t, a.conn.Done(), "the HTTP/3 connection ended with no idle timeout")

					if _, ok := e.quic.pool.Shared(h3Key(r)); !ok {
						t.Fatal("the HTTP/3 connection left the pool with no idle timeout")
					}

					return
				}

				clock.Advance(timeout - time.Second)

				if _, ok := e.quic.pool.Shared(h3Key(r)); !ok {
					t.Fatal("the HTTP/3 connection left the pool before the idle timeout")
				}

				clock.Advance(time.Second)
				next(t, a.conn.Done(), "end of the HTTP/3 connection at the idle timeout")

				if _, ok := e.quic.pool.Shared(h3Key(r)); ok {
					t.Fatal("the expired HTTP/3 connection is still pooled")
				}

				// Silently: no CONNECTION_CLOSE reaches the server, which
				// keeps the connection until its own idle timeout.
				none(t, server.Done(), "the server saw the client close its idle connection")

				return
			}

			resp, err := get(t, e, "https://localhost/")
			if err != nil {
				t.Fatal(err)
			}

			readAll(t, resp)

			if timeout < 0 {
				clock.Advance(100 * idleTimeout)
				none(t, events, "with no idle timeout")

				if _, ok := e.pool.Shared("https://localhost:443"); !ok {
					t.Fatal("the HTTP/2 connection left the pool with no idle timeout")
				}

				return
			}

			clock.Advance(timeout - time.Second)
			none(t, events, "before the idle timeout")

			if _, ok := e.pool.Shared("https://localhost:443"); !ok {
				t.Fatal("the HTTP/2 connection left the pool before the idle timeout")
			}

			clock.Advance(time.Second)

			for _, w := range tc.want {
				if got := nextAdvancing(t, clock, events, w); got != w {
					t.Fatalf("server saw %q, want %q", got, w)
				}
			}

			// The next request opens a new connection.
			resp, err = get(t, e, "https://localhost/")
			if err != nil {
				t.Fatal(err)
			}

			readAll(t, resp)

			if d := len(n.dials()); d != 2 {
				t.Fatalf("%d connections, want 2", d)
			}
		})
	}
}

// nextAdvancing is the next event, advancing clock by h2's declineLinger
// while none comes (the GOAWAY's close follows it by that, on a timer
// armed after the GOAWAY is written), failing t after 5 s.
func nextAdvancing[T any](t *testing.T, clock *clockwork.FakeClock, ch <-chan T, what string) T {
	t.Helper()

	deadline := time.After(5 * time.Second)

	for {
		select {
		case v := <-ch:
			return v
		case <-time.After(10 * time.Millisecond):
			clock.Advance(time.Second)
		case <-deadline:
			t.Fatalf("no %s", what)

			var zero T

			return zero
		}
	}
}

// clientGoroutines counts the goroutines running the client's HTTP/2
// connection or the pool's code.
func clientGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]

			break
		}

		buf = make([]byte, 2*len(buf))
	}

	count := 0

	for g := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(g, "mimicry/internal/h2.") || strings.Contains(g, "mimicry/internal/pool.") {
			count++
		}
	}

	return count
}

// A pooled idle HTTP/2 connection costs one goroutine, its read loop, and
// leaves the pool when the server closes it (pool.Shared.OnClose).
func TestPooledHTTP2ConnectionGoroutines(t *testing.T) {
	events := make(chan string, 8)
	closeServer := make(chan struct{})
	n := newFakeNet(t, func(ctx context.Context, conn net.Conn) error {
		go func() {
			<-closeServer

			_ = conn.Close()
		}()

		return serveH2Events(events)(ctx, conn)
	})
	e := newTestEngine(t, options{
		dial:  n.dial,
		clock: clockwork.NewFakeClock(),
	})

	resp, err := get(t, e, "https://localhost/")
	if err != nil {
		t.Fatal(err)
	}

	readAll(t, resp)

	if g := clientGoroutines(); g != 1 {
		t.Errorf("%d goroutines for a pooled idle HTTP/2 connection, want 1", g)
	}

	close(closeServer)
	next(t, events, "close at the server")

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, pooled := e.pool.Shared("https://localhost:443")
		if !pooled && clientGoroutines() == 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("closed by the server: pooled %v, %d goroutines left", pooled, clientGoroutines())
		}

		time.Sleep(time.Millisecond)
	}
}

// proxyNet answers, for every proxy address, plain-HTTP requests sent to it
// whole with 200 "ok", and records the connections dialled to each and
// those that closed.
type proxyNet struct {
	t      *testing.T
	mu     sync.Mutex
	dialed map[string]int
	closed chan string
}

func newProxyNet(t *testing.T) *proxyNet {
	return &proxyNet{
		t:      t,
		dialed: map[string]int{},
		closed: make(chan string, 1024),
	}
}

func (n *proxyNet) dial(_ context.Context, _, addr string) (net.Conn, error) {
	n.mu.Lock()

	n.dialed[addr]++
	n.mu.Unlock()

	client, server := net.Pipe()

	n.t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	go func() {
		br := bufio.NewReader(server)

		for {
			req, err := http.ReadRequest(br)
			if err != nil {
				n.closed <- addr

				return
			}

			_, _ = io.Copy(io.Discard, req.Body)
			_ = req.Body.Close()

			if _, err := io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"); err != nil {
				n.closed <- addr

				return
			}
		}
	}()

	return client, nil
}

// dials is how many connections went to the proxy at addr.
func (n *proxyNet) dials(addr string) int {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.dialed[addr]
}

// proxyAddr is the address of proxy i.
func proxyAddr(i int) string {
	return fmt.Sprintf("p%d.test:8080", i)
}

// A Transport rotating proxies loses the idle connections of those it no
// longer uses (decision 0040), not those it uses in turn: proxies beyond
// the keepProxies most recent and unused for proxyUnused close theirs,
// round-robin over keepProxies proxies, or over more within proxyUnused,
// keeps every connection. Fake clock.
func TestUnusedProxiesCloseTheirConnections(t *testing.T) {
	for _, tc := range []struct {
		name string
		// proxies are used in turn, rounds times, every apart.
		proxies, rounds int
		every           time.Duration
		// wantClosed are the proxies whose connections close once
		// proxyUnused has passed after the last request, none before.
		wantClosed []int
	}{
		{
			name:       "rotation, each proxy once",
			proxies:    keepProxies + 4,
			rounds:     1,
			every:      time.Second,
			wantClosed: []int{0, 1, 2, 3},
		},
		{
			name:    "round-robin over a few, slowly",
			proxies: 4,
			rounds:  10,
			every:   15 * time.Second,
		},
		{
			// Once it stops, the proxies beyond the most recent close.
			name:       "round-robin over many, each again within proxyUnused",
			proxies:    keepProxies + 8,
			rounds:     3,
			every:      proxyUnused / (keepProxies + 16),
			wantClosed: []int{0, 1, 2, 3, 4, 5, 6, 7},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := clockwork.NewFakeClock()
			n := newProxyNet(t)

			var current atomic.Pointer[url.URL]

			e := newTestEngine(t, options{
				dial:  n.dial,
				clock: clock,
				proxy: func(*http.Request) (*url.URL, error) {
					return current.Load(), nil
				},
			})

			for range tc.rounds {
				for i := range tc.proxies {
					current.Store(&url.URL{
						Scheme: "http",
						Host:   proxyAddr(i),
					})

					resp, err := get(t, e, "http://example.test/")
					if err != nil {
						t.Fatal(err)
					}

					readAll(t, resp)
					clock.Advance(tc.every)
				}
			}

			none(t, n.closed, "a connection closed while its proxy was in use")

			for i := range tc.proxies {
				if d := n.dials(proxyAddr(i)); d != 1 {
					t.Errorf("%d connections to proxy %d, want 1", d, i)
				}
			}

			clock.Advance(proxyUnused)

			closed := make([]string, 0, len(tc.wantClosed))
			for range tc.wantClosed {
				closed = append(closed, next(t, n.closed, "close of an unused proxy's connection"))
			}

			none(t, n.closed, "another connection closed")

			want := make([]string, len(tc.wantClosed))
			for i, p := range tc.wantClosed {
				want[i] = proxyAddr(p)
			}

			slices.Sort(closed)
			slices.Sort(want)

			if !slices.Equal(closed, want) {
				t.Fatalf("closed %v, want %v", closed, want)
			}
		})
	}
}
