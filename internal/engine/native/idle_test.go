package native

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
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
	t.Helper()

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
// longer uses (decision 0040), not those it uses in turn: by default
// proxies beyond the keepProxies most recent and unused for proxyUnused
// close theirs, and round-robin over keepProxies proxies, or over more
// within proxyUnused, keeps every connection; with MaxIdleProxies, those
// beyond that many most recent close at once, however recently used, and
// round-robin over that many keeps them. Fake clock.
func TestUnusedProxiesCloseTheirConnections(t *testing.T) {
	for _, tc := range []struct {
		name string
		// maxIdle is Config.MaxIdleProxies.
		maxIdle int
		// proxies are used in turn, rounds times, every apart.
		proxies, rounds int
		every           time.Duration
		// wantClosed are the proxies whose connections close: by default
		// once proxyUnused has passed after the last request, none
		// before; with maxIdle, as the requests go, the clock barely
		// moving.
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
		{
			name:       "MaxIdleProxies 4, rotation",
			maxIdle:    4,
			proxies:    6,
			rounds:     1,
			every:      time.Nanosecond,
			wantClosed: []int{0, 1},
		},
		{
			name:       "MaxIdleProxies 1, rotation",
			maxIdle:    1,
			proxies:    4,
			rounds:     1,
			every:      time.Nanosecond,
			wantClosed: []int{0, 1, 2},
		},
		{
			name:    "MaxIdleProxies 4, round-robin over 4, slowly",
			maxIdle: 4,
			proxies: 4,
			rounds:  10,
			every:   15 * time.Second,
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
				maxIdleProxies: tc.maxIdle,
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

			if tc.maxIdle == 0 {
				none(t, n.closed, "a connection closed while its proxy was in use")
			}

			for i := range tc.proxies {
				if d := n.dials(proxyAddr(i)); d != 1 {
					t.Errorf("%d connections to proxy %d, want 1", d, i)
				}
			}

			if tc.maxIdle == 0 {
				clock.Advance(proxyUnused)
			}

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

// identityProxies are HTTP or SOCKS5 proxies in front of one https server,
// recording by proxy address what each connection to them did: CONNECT,
// with credentials or not (an HTTP proxy answers 407 without), UDP
// ASSOCIATE (a SOCKS5 proxy refuses it) and whether the TLS handshake
// through the tunnel resumed.
type identityProxies struct {
	t      *testing.T
	socks5 bool
	// tls is the server's, one for every connection so that its tickets
	// resume.
	tls  func() *tls.Config
	mu   sync.Mutex
	seen map[string][]string
}

func newIdentityProxies(t *testing.T, socks5 bool) *identityProxies {
	t.Helper()

	return &identityProxies{
		t:      t,
		socks5: socks5,
		tls: sync.OnceValue(func() *tls.Config {
			return &tls.Config{
				Certificates: []tls.Certificate{
					*serverCert.Load(),
				},
				NextProtos: []string{
					"h2",
					"http/1.1",
				},
			}
		}),
		seen: map[string][]string{},
	}
}

func (p *identityProxies) dial(_ context.Context, _, addr string) (net.Conn, error) {
	client, server := net.Pipe()

	p.t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	go p.serve(addr, server)

	return client, nil
}

// serve opens the tunnel on conn, then answers its requests over HTTP/2
// with 200 "ok" and an HTTP/3 alternative.
func (p *identityProxies) serve(addr string, conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	open := p.connect
	if p.socks5 {
		open = p.socks
	}

	// The client writes nothing more before the tunnel's answer: br holds
	// none of the TLS handshake.
	if err := open(addr, bufio.NewReader(conn), conn); err != nil {
		return
	}

	tc := tls.Server(conn, p.tls())
	if err := tc.HandshakeContext(p.t.Context()); err != nil {
		return
	}

	if tc.ConnectionState().DidResume {
		p.record(addr, "TLS resumed")
	} else {
		p.record(addr, "TLS")
	}

	serveH2On(p.t.Context(), tc, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Alt-Svc", `h3=":443"`)

		_, _ = io.WriteString(w, "ok")
	}))
}

// connect answers CONNECTs on conn: 407 without credentials, 200 with.
func (p *identityProxies) connect(addr string, br *bufio.Reader, conn net.Conn) error {
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return err
		}

		if req.Header.Get("Proxy-Authorization") != "" {
			p.record(addr, "CONNECT with credentials")

			_, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")

			return err
		}

		p.record(addr, "CONNECT")

		if _, err := io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"test\"\r\nContent-Length: 0\r\n\r\n"); err != nil {
			return err
		}
	}
}

// socks answers a SOCKS5 handshake on conn (RFC 1928; RFC 1929's username
// and password, when offered, accepted): CONNECT succeeds, UDP ASSOCIATE
// is refused (command not supported).
func (p *identityProxies) socks(addr string, br *bufio.Reader, conn net.Conn) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		return err
	}

	methods := make([]byte, head[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return err
	}

	method := byte(0x00)
	if slices.Contains(methods, 0x02) {
		method = 0x02
	}

	if _, err := conn.Write([]byte{0x05, method}); err != nil {
		return err
	}

	if method == 0x02 {
		// Its version, then the username and the password, each after
		// its length.
		if _, err := br.ReadByte(); err != nil {
			return err
		}

		for range 2 {
			n, err := br.ReadByte()
			if err != nil {
				return err
			}

			if _, err := br.Discard(int(n)); err != nil {
				return err
			}
		}

		if _, err := conn.Write([]byte{0x01, 0x00}); err != nil {
			return err
		}
	}

	// Version, command, reserved, address type; the address and port.
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return err
	}

	skip := 4

	switch req[3] {
	case 0x03:
		n, err := br.ReadByte()
		if err != nil {
			return err
		}

		skip = int(n)

	case 0x04:
		skip = 16
	}

	if _, err := br.Discard(skip + 2); err != nil {
		return err
	}

	if req[1] == 0x03 {
		p.record(addr, "UDP ASSOCIATE")

		_, _ = conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

		return errors.New("UDP ASSOCIATE refused")
	}

	p.record(addr, "CONNECT")

	_, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

	return err
}

func (p *identityProxies) record(addr, what string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.seen[addr] = append(p.seen[addr], what)
}

// take returns, sorted, what connections to the proxy at addr did since
// the last take.
func (p *identityProxies) take(addr string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	seen := p.seen[addr]
	delete(p.seen, addr)
	slices.Sort(seen)

	return seen
}

// A Transport rotating through proxy identities keeps what it learned
// through the recent ones only. One that falls out of the rememberProxies
// most recent (with MaxIdleProxies, out of that many) comes back as a new
// proxy would, with a full TLS handshake and, through an HTTP proxy with
// BrowserProxyAuth, the 407 again, through a SOCKS5 one with
// HTTP3OverSOCKS5, UDP ASSOCIATE asked again; and no HTTP/3 origin state
// is left of it. By default one out of the keepProxies most recent but
// within rememberProxies loses its connections only: it comes back on a
// new connection that resumes and keeps what its proxy said, as the live
// one does past the idle timeout. Fake clock; rememberProxies is lowered
// to keepProxies+4 to keep the rotation short.
func TestSweptProxiesAreForgotten(t *testing.T) {
	for _, tc := range []struct {
		name   string
		socks5 bool
		// kept and fresh are what the proxy sees, sorted, of a request on
		// a new connection through an identity whose state was kept, and
		// through one that was forgotten.
		kept, fresh []string
	}{
		{
			name: "HTTP proxy",
			kept: []string{
				"CONNECT with credentials",
				"TLS resumed",
			},
			fresh: []string{
				"CONNECT",
				"CONNECT with credentials",
				"TLS",
			},
		},
		{
			name:   "SOCKS5 proxy refusing UDP",
			socks5: true,
			kept: []string{
				"CONNECT",
				"TLS resumed",
			},
			fresh: []string{
				"CONNECT",
				"TLS",
				"UDP ASSOCIATE",
			},
		},
	} {
		for _, maxIdle := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s, MaxIdleProxies %d", tc.name, maxIdle), func(t *testing.T) {
				testSweptProxiesAreForgotten(t, tc.socks5, maxIdle, tc.kept, tc.fresh)
			})
		}
	}
}

// testSweptProxiesAreForgotten is one case of TestSweptProxiesAreForgotten:
// 4 identities more than are remembered are used in turn, then each
// remembered one out of the most recent, the last one and the forgotten
// ones come back, the first ones' requests looking to the proxy as kept
// says, the forgotten ones' as fresh says.
func testSweptProxiesAreForgotten(t *testing.T, socks5 bool, maxIdle int, kept, fresh []string) {
	t.Helper()

	const forgotten = 4

	// uTLS times tickets by the real clock: start there.
	clock := clockwork.NewFakeClockAt(time.Now())
	proxies := newIdentityProxies(t, socks5)

	var current atomic.Pointer[url.URL]

	e := newTestEngine(t, options{
		dial:  proxies.dial,
		clock: clock,
		proxy: func(*http.Request) (*url.URL, error) {
			return current.Load(), nil
		},
		maxIdleProxies:   maxIdle,
		browserProxyAuth: true,
		http3OverSOCKS5:  true,
	})
	if maxIdle == 0 {
		e.proxies.remember = keepProxies + 4
	}

	keep, remember := e.proxies.keep, e.proxies.remember
	identities := remember + forgotten

	through := func(i int) []string {
		t.Helper()

		scheme := "http"
		if socks5 {
			scheme = "socks5h"
		}

		current.Store(&url.URL{
			Scheme: scheme,
			User:   url.UserPassword(fmt.Sprintf("user%d", i), "secret"),
			Host:   proxyAddr(i),
		})

		resp, err := get(t, e, "https://localhost/")
		if err != nil {
			t.Fatal(err)
		}

		readAll(t, resp)
		clock.Advance(time.Nanosecond)

		return proxies.take(proxyAddr(i))
	}

	for i := range identities {
		through(i)
	}

	deadline := time.Now().Add(5 * time.Second)

	for {
		e.proxies.mu.Lock()

		known, closed := len(e.proxies.byURL), 0
		for _, pk := range e.proxies.byURL {
			if pk.closed {
				closed++
			}
		}

		e.proxies.mu.Unlock()

		if known == remember && closed == remember-keep {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("%d proxies known, %d of them closed; want the %d remembered, %d closed", known, closed, remember, remember-keep)
		}

		time.Sleep(time.Millisecond)
		clock.Advance(time.Second)
	}

	e.quic.mu.Lock()

	origins := len(e.quic.origins)
	e.quic.mu.Unlock()

	if origins > remember {
		t.Errorf("HTTP/3 state kept for %d origins, want the %d remembered proxies' only", origins, remember)
	}

	// Remembered, connections closed: a new connection, resumed.
	for i := forgotten; i < identities-keep; i++ {
		if got := through(i); !slices.Equal(got, kept) {
			t.Errorf("through remembered proxy %d: %q, want %q", i, got, kept)
		}
	}

	// Past the idle timeout: the live proxies' connections close, what
	// they learned stays.
	clock.Advance(2 * time.Minute)
	e.CloseIdleConnections()

	if got := through(identities - 1); !slices.Equal(got, kept) {
		t.Errorf("through the live proxy: %q, want %q", got, kept)
	}

	for i := range forgotten {
		if got := through(i); !slices.Equal(got, fresh) {
			t.Errorf("through forgotten proxy %d: %q, want %q", i, got, fresh)
		}
	}
}
