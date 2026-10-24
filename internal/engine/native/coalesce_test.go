package native

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/net/http2"

	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profiles"
)

// ipConn is a connection reporting remote as its peer's address.
type ipConn struct {
	net.Conn
	remote net.Addr
}

func (c *ipConn) RemoteAddr() net.Addr {
	return c.remote
}

// ipDial dials through n, each connection reaching the address ips gives
// its host, on port 443.
func ipDial(n *fakeNet, ips map[string]string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := n.dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(addr)
		return &ipConn{
			Conn: conn,
			remote: &net.TCPAddr{
				IP:   net.ParseIP(ips[host]),
				Port: 443,
			},
		}, nil
	}
}

// fakeLookup answers lookups from ips and records the hosts looked up.
type fakeLookup struct {
	ips   map[string]string
	mu    sync.Mutex
	asked []string
}

func (l *fakeLookup) lookup(_ context.Context, host string) ([]netip.Addr, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, host)
	ip, ok := l.ips[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return []netip.Addr{
		netip.MustParseAddr(ip),
	}, nil
}

func (l *fakeLookup) hosts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.asked)
}

// get GETs target in partition and wants "ok".
func get(t *testing.T, e *Engine, partition, target string) {
	t.Helper()
	req, err := http.NewRequestWithContext(pool.WithPartition(t.Context(), partition), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := e.RoundTrip(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	if body := readAll(t, resp); body != "ok" {
		t.Fatalf("GET %s: body %q", target, body)
	}
}

// wantDials fails the test unless n dialed exactly want.
func wantDials(t *testing.T, n *fakeNet, want ...string) {
	t.Helper()
	if got := n.dials(); !slices.Equal(got, want) {
		t.Fatalf("dialed %q, want %q", got, want)
	}
}

// coalescing is an engine for p whose test servers present one
// certificate for hosts, dialing through n with the addresses ips gives,
// and looking hosts up in the same table.
func coalescing(t *testing.T, n *fakeNet, ips map[string]string, hosts ...string) (*Engine, *fakeLookup) {
	t.Helper()
	e := newTestEngine(t, options{
		dial:      ipDial(n, ips),
		certHosts: hosts,
	})
	l := &fakeLookup{
		ips: ips,
	}
	e.lookup = l.lookup
	return e, l
}

// Chrome and Safari send a request to a host the certificate of an open
// HTTP/2 connection covers on that connection, without dialing, when the
// host resolves to its address (the connections capture: static,
// pair.y).
func TestLookupCoalescesOntoACoveringConnection(t *testing.T) {
	n := newFakeNet(t, serveH2)
	e, l := coalescing(t, n, map[string]string{
		"a.test": "10.0.0.1",
		"b.test": "10.0.0.1",
	}, "a.test", "b.test")
	get(t, e, "", "https://a.test/")
	get(t, e, "", "https://b.test/")
	get(t, e, "", "https://b.test/again")
	wantDials(t, n, "a.test:443")
	// The first request had no connection to take, so no lookup; the
	// third found b.test's own (adopted) connection.
	if got := l.hosts(); !slices.Equal(got, []string{"b.test"}) {
		t.Fatalf("looked up %q, want b.test once", got)
	}
}

func TestLookupNeedsTheSameAddress(t *testing.T) {
	n := newFakeNet(t, serveH2, serveH2)
	e, _ := coalescing(t, n, map[string]string{
		"a.test": "10.0.0.1",
		"b.test": "10.0.0.2",
	}, "a.test", "b.test")
	get(t, e, "", "https://a.test/")
	get(t, e, "", "https://b.test/")
	wantDials(t, n, "a.test:443", "b.test:443")
}

func TestFailedLookupDials(t *testing.T) {
	n := newFakeNet(t, serveH2, serveH2)
	e, _ := coalescing(t, n, map[string]string{
		"a.test": "10.0.0.1",
	}, "a.test", "b.test")
	// b.test is not in the table: its lookup fails, and it dials.
	get(t, e, "", "https://a.test/")
	get(t, e, "", "https://b.test/")
	wantDials(t, n, "a.test:443", "b.test:443")
}

// A connection whose certificate does not cover the host is not taken,
// and the host is not even looked up (the solo control).
func TestLookupNeedsACoveringCertificate(t *testing.T) {
	n := newFakeNet(t, serveH2, serveH2)
	e, l := coalescing(t, n, map[string]string{
		"a.test": "10.0.0.1",
		"b.test": "10.0.0.1",
	}, "a.test")
	get(t, e, "", "https://a.test/")
	certB, _ := testutil.Certificate(t, "b.test")
	leaf, err := x509.ParseCertificate(certB.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	e.rootCAs.AddCert(leaf)
	serverCert.Store(&certB)
	get(t, e, "", "https://b.test/")
	wantDials(t, n, "a.test:443", "b.test:443")
	if got := l.hosts(); len(got) != 0 {
		t.Fatalf("looked up %q, want nothing", got)
	}
}

func TestPartitionsDoNotCoalesce(t *testing.T) {
	n := newFakeNet(t, serveH2, serveH2)
	e, _ := coalescing(t, n, map[string]string{
		"a.test": "10.0.0.1",
		"b.test": "10.0.0.1",
	}, "a.test", "b.test")
	get(t, e, "", "https://a.test/")
	get(t, e, "anonymous", "https://b.test/")
	wantDials(t, n, "a.test:443", "b.test:443")
}

func TestOtherPortsDoNotCoalesce(t *testing.T) {
	n := newFakeNet(t, serveH2, serveH2)
	e, _ := coalescing(t, n, map[string]string{
		"a.test": "10.0.0.1",
		"b.test": "10.0.0.1",
	}, "a.test", "b.test")
	get(t, e, "", "https://a.test/")
	get(t, e, "", "https://b.test:8443/")
	wantDials(t, n, "a.test:443", "b.test:8443")
}

// declinedServer completes the TLS handshake and sends the HTTP/2 frame
// types the client sends after the preface to frames, once it closes.
func declinedServer(frames chan<- []http2.FrameType) func(context.Context, net.Conn) error {
	return func(ctx context.Context, conn net.Conn) error {
		tc, err := tlsServer(ctx, conn)
		if err != nil {
			return err
		}
		br := bufio.NewReader(tc)
		preface := make([]byte, len(http2.ClientPreface))
		if _, err := io.ReadFull(br, preface); err != nil {
			return err
		}
		fr := http2.NewFramer(nil, br)
		var types []http2.FrameType
		for {
			f, err := fr.ReadFrame()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			types = append(types, f.Header().Type)
		}
		frames <- types
		return nil
	}
}

// Firefox connects to the host first; once the handshake is done, it sends
// the new connection its preface and GOAWAY and the request goes on the
// connection that covers the host at the same address (the connections
// capture: static and y get SETTINGS, WINDOW_UPDATE, GOAWAY). Later
// requests take that connection at once.
func TestConnectCoalescingDeclinesTheNewConnection(t *testing.T) {
	frames := make(chan []http2.FrameType, 1)
	n := newFakeNet(t, serveH2, declinedServer(frames))
	ips := map[string]string{
		"a.test": "10.0.0.1",
		"b.test": "10.0.0.1",
	}
	e := newTestEngine(t, options{
		dial:      ipDial(n, ips),
		certHosts: []string{"a.test", "b.test"},
		profile:   profiles.Firefox158MacOS(),
	})
	var lookups atomic.Int32
	e.lookup = func(context.Context, string) ([]netip.Addr, error) {
		lookups.Add(1)
		return nil, errors.New("no lookups on connect")
	}
	get(t, e, "", "https://a.test/")
	get(t, e, "", "https://b.test/")
	want := []http2.FrameType{
		http2.FrameSettings,
		http2.FrameWindowUpdate,
		http2.FrameGoAway,
	}
	if got := <-frames; !slices.Equal(got, want) {
		t.Fatalf("declined connection got %v, want %v", got, want)
	}
	get(t, e, "", "https://b.test/again")
	wantDials(t, n, "a.test:443", "b.test:443")
	if lookups.Load() != 0 {
		t.Fatal("coalescing on connect looked a host up")
	}
}

func TestConnectCoalescingNeedsTheSameAddress(t *testing.T) {
	n := newFakeNet(t, serveH2, serveH2)
	e := newTestEngine(t, options{
		dial: ipDial(n, map[string]string{
			"a.test": "10.0.0.1",
			"b.test": "10.0.0.2",
		}),
		certHosts: []string{"a.test", "b.test"},
		profile:   profiles.Firefox158MacOS(),
	})
	get(t, e, "", "https://a.test/")
	get(t, e, "", "https://b.test/")
	get(t, e, "", "https://b.test/again")
	wantDials(t, n, "a.test:443", "b.test:443")
}

// gatedH1 answers up to two requests on conn, each once gate is closed.
func gatedH1(gate <-chan struct{}) func(context.Context, net.Conn) error {
	return func(_ context.Context, conn net.Conn) error {
		br := bufio.NewReader(conn)
		for range 2 {
			// A connection's second request may never come: the read
			// fails when the test closes it, and the error is ignored.
			if _, err := http.ReadRequest(br); err != nil {
				return err
			}
			<-gate
			if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"); err != nil {
				return err
			}
		}
		return nil
	}
}

// Every browser opens at most 6 connections to an HTTP/1.1 host; the
// requests over that wait for one to be free (the connections capture:
// 10 parallel requests, 6 connections).
func TestHTTP1ConnectionsPerHostAreCapped(t *testing.T) {
	gate := make(chan struct{})
	servers := make([]func(context.Context, net.Conn) error, 6)
	for i := range servers {
		servers[i] = gatedH1(gate)
	}
	n := newFakeNet(t, servers...)
	var dialed atomic.Int32
	e := newTestEngine(t, options{
		dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := n.dial(ctx, network, addr)
			if dialed.Add(1) == 6 {
				close(gate)
			}
			return conn, err
		},
	})
	var wg sync.WaitGroup
	for range 7 {
		wg.Go(func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://h1.test/", nil)
			if err != nil {
				t.Error(err)
				return
			}
			resp, err := e.RoundTrip(req)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		})
	}
	wg.Wait()
	if got := len(n.dials()); got != 6 {
		t.Fatalf("%d connections, want 6", got)
	}
}
