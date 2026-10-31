package native

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// fakeNet hands each connection the engine dials to the next of its
// servers, over an in-memory pipe, and records the addresses dialed.
type fakeNet struct {
	t *testing.T
	// ctx is the test's: servers stop when it ends.
	ctx     context.Context
	mu      sync.Mutex
	servers []func(ctx context.Context, conn net.Conn) error
	dialed  []string
	wg      sync.WaitGroup
	errs    chan error
}

func newFakeNet(t *testing.T, servers ...func(ctx context.Context, conn net.Conn) error) *fakeNet {
	t.Helper()

	n := &fakeNet{
		t:       t,
		ctx:     t.Context(),
		servers: servers,
		errs:    make(chan error, 16),
	}
	t.Cleanup(func() {
		n.wg.Wait()
		close(n.errs)

		for err := range n.errs {
			t.Error(err)
		}
	})

	return n
}

func (n *fakeNet) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.dialed = append(n.dialed, addr)
	if len(n.servers) == 0 {
		return nil, errors.New("no more connections expected")
	}

	serve := n.servers[0]

	n.servers = n.servers[1:]

	client, server := net.Pipe()
	// The server outlives the dial; the pipes close when the test ends.
	ctx = context.WithoutCancel(ctx)

	n.t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	n.wg.Go(func() {
		if err := serve(ctx, server); err != nil && n.ctx.Err() == nil {
			n.errs <- err
		}
	})

	return client, nil
}

func (n *fakeNet) dials() []string {
	n.mu.Lock()
	defer n.mu.Unlock()

	return append([]string(nil), n.dialed...)
}

type options struct {
	dialTimeout time.Duration
	dial        func(ctx context.Context, network, addr string) (net.Conn, error)
	onWait      func(key string)
	// maxConns is Config.MaxConnsPerHost.
	maxConns int
	// profile defaults to profiles.Chrome().
	profile *profile.Profile
	// clock defaults to the real one; proxy is Config.Proxy.
	clock clockwork.Clock
	proxy func(*http.Request) (*url.URL, error)
	// idleConnTimeout is Config.IdleConnTimeout, http3ReadBufferSize
	// Config.HTTP3ReadBufferSize, maxIdleProxies Config.MaxIdleProxies,
	// browserProxyAuth Config.BrowserProxyAuth and http3OverSOCKS5
	// Config.HTTP3OverSOCKS5.
	idleConnTimeout     time.Duration
	http3ReadBufferSize int
	maxIdleProxies      int
	browserProxyAuth    bool
	http3OverSOCKS5     bool
}

func newTestEngine(t *testing.T, o options) *Engine {
	t.Helper()

	cert, roots := testutil.Certificate(t, "localhost")
	serverCert.Store(&cert)

	p := o.profile
	if p == nil {
		p = profiles.Chrome()
	}

	clock := o.clock
	if clock == nil {
		clock = clockwork.NewRealClock()
	}

	e, err := newEngine(Config{
		Profile:             p,
		RootCAs:             roots,
		DialTimeout:         o.dialTimeout,
		DialContext:         o.dial,
		Proxy:               o.proxy,
		Coalesce:            true,
		MaxConnsPerHost:     o.maxConns,
		IdleConnTimeout:     o.idleConnTimeout,
		HTTP3ReadBufferSize: o.http3ReadBufferSize,
		MaxIdleProxies:      o.maxIdleProxies,
		BrowserProxyAuth:    o.browserProxyAuth,
		HTTP3OverSOCKS5:     o.http3OverSOCKS5,
	}, clock, o.onWait)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(e.CloseIdleConnections)

	return e
}

// serverCert is the certificate the test servers present, which the
// engine of the running test trusts.
var serverCert atomic.Pointer[tls.Certificate]

// tlsServer completes a TLS handshake as the server, offering h2 and
// HTTP/1.1.
func tlsServer(ctx context.Context, conn net.Conn) (*tls.Conn, error) {
	srv := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{
			*serverCert.Load(),
		},
		NextProtos: []string{
			"h2",
			"http/1.1",
		},
	})

	return srv, srv.HandshakeContext(ctx)
}

// serveH1 answers one request on conn with 200 and its body, then
// calls after.
func serveH1(after func(conn net.Conn) error) func(context.Context, net.Conn) error {
	return func(_ context.Context, conn net.Conn) error {
		req, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return err
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			return err
		}

		resp := &http.Response{
			StatusCode:    http.StatusOK,
			ProtoMajor:    1,
			ProtoMinor:    1,
			ContentLength: int64(len(body)),
			Body:          io.NopCloser(strings.NewReader(string(body))),
		}
		if err := resp.Write(conn); err != nil {
			return err
		}

		if after != nil {
			return after(conn)
		}

		return nil
	}
}

// serveH1Held answers each request on conn with 200 "ok", keeping the
// connection open, once release is closed; got hears of each request as
// it arrives.
func serveH1Held(got chan<- struct{}, release <-chan struct{}) func(context.Context, net.Conn) error {
	return func(_ context.Context, conn net.Conn) error {
		br := bufio.NewReader(conn)

		for {
			req, err := http.ReadRequest(br)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
					return nil
				}

				return err
			}

			_ = req.Body.Close()

			got <- struct{}{}

			<-release

			if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"); err != nil {
				return err
			}
		}
	}
}

// serveH2 serves conn with net/http's HTTP/2 server, answering 200 "ok",
// until the connection closes.
func serveH2(ctx context.Context, conn net.Conn) error {
	tc, err := tlsServer(ctx, conn)
	if err != nil {
		return err
	}

	serveH2On(ctx, tc, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	return nil
}

// serveH2On serves tc, a TLS connection past its handshake, with net/http's
// HTTP/2 server and h until the connection closes.
func serveH2On(ctx context.Context, tc *tls.Conn, h http.Handler) {
	closed := make(chan struct{})

	var once sync.Once

	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           h,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				once.Do(func() {
					close(closed)
				})
			}
		},
	}

	_ = srv.Serve(&oneConnListener{
		conn:   tc,
		ctx:    ctx,
		closed: closed,
	})
}

// oneConnListener hands out conn once, then blocks until it has closed
// or ctx ends, so http.Server.Serve returns after its one connection.
type oneConnListener struct {
	conn   net.Conn
	ctx    context.Context
	closed chan struct{}
	taken  atomic.Bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if !l.taken.Swap(true) {
		return l.conn, nil
	}

	select {
	case <-l.closed:
	case <-l.ctx.Done():
	}

	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error {
	return nil
}

func (l *oneConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

// get sends a GET on t's context. It does not fail t, so goroutines may
// call it.
func get(t *testing.T, e *Engine, target string) (*http.Response, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}

	return e.RoundTrip(req)
}

// readAll reads and closes resp's body.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()

	b, err := io.ReadAll(resp.Body)

	_ = resp.Body.Close()

	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// postOnce POSTs data in a body that cannot be sent twice: it has no
// GetBody. It does not fail t.
func postOnce(t *testing.T, e *Engine, target, data string) (*http.Response, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, io.NopCloser(strings.NewReader(data)))
	if err != nil {
		return nil, err
	}

	req.ContentLength = int64(len(data))

	return e.RoundTrip(req)
}

func TestDeadIdleConnectionIsNotReused(t *testing.T) {
	closed := make(chan struct{})
	n := newFakeNet(t, serveH1(func(conn net.Conn) error {
		defer close(closed)

		return conn.Close()
	}), serveH1(nil))
	e := newTestEngine(t, options{
		dial: n.dial,
	})

	resp, err := get(t, e, "http://example.test/")
	if err != nil {
		t.Fatal(err)
	}

	readAll(t, resp)
	<-closed

	// The server closed the idle connection: a POST, which is never sent
	// twice, must go on a new one.
	resp, err = postOnce(t, e, "http://example.test/", "data")
	if err != nil {
		t.Fatalf("POST after the server closed the idle connection: %v", err)
	}

	if got := readAll(t, resp); got != "data" {
		t.Fatalf("body %q", got)
	}
}

// blockingDial is a DialContext whose first dial waits for release, then
// fails with err or connects to n.
func blockingDial(n *fakeNet, err error) (dial func(context.Context, string, string) (net.Conn, error), dialing, release chan struct{}) {
	dialing, release = make(chan struct{}), make(chan struct{})

	var first atomic.Bool

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if first.CompareAndSwap(false, true) {
			close(dialing)
			<-release

			if err != nil {
				return nil, err
			}
		}

		return n.dial(ctx, network, addr)
	}, dialing, release
}

func TestWaiterSendsABodyThatCannotBeResent(t *testing.T) {
	n := newFakeNet(t, serveH1(nil), serveH1(nil))
	dial, dialing, release := blockingDial(n, nil)
	waiting := make(chan string, 1)
	e := newTestEngine(t, options{
		dial: dial,
		onWait: func(key string) {
			waiting <- key
		},
	})
	first := make(chan error, 1)

	go func() {
		resp, err := get(t, e, "http://example.test/")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		first <- err
	}()

	<-dialing

	post := make(chan error, 1)

	go func() {
		resp, err := postOnce(t, e, "http://example.test/", "data")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		post <- err
	}()

	<-waiting
	close(release)

	if err := <-first; err != nil {
		t.Fatal(err)
	}

	if err := <-post; err != nil {
		t.Fatalf("POST that waited for another request's dial: %v", err)
	}
}

func TestWaitersGetTheSharedDialsError(t *testing.T) {
	n := newFakeNet(t)
	refused := errors.New("connection refused")
	dial, dialing, release := blockingDial(n, refused)
	waiting := make(chan string, 1)
	e := newTestEngine(t, options{
		dial: dial,
		onWait: func(key string) {
			waiting <- key
		},
	})
	errs := make(chan error, 2)

	go func() {
		_, err := get(t, e, "https://localhost/")
		errs <- err
	}()

	<-dialing

	go func() {
		_, err := get(t, e, "https://localhost/")
		errs <- err
	}()

	<-waiting
	close(release)

	for range 2 {
		if err := <-errs; !errors.Is(err, refused) {
			t.Fatalf("err = %v, want the dial's error", err)
		}
	}

	if d := n.dials(); len(d) != 0 {
		t.Fatalf("dialed again after the shared dial failed: %v", d)
	}
}

// TestMaxConnsPerHost checks Config.MaxConnsPerHost: the HTTP/1.1
// connections open at once to one host stop at it (at the profile's 6 when
// zero), and the request after them waits for one to come back.
func TestMaxConnsPerHost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		maxConns int
		want     int
	}{
		{
			name: "the profile's",
			want: 6,
		},
		{
			name:     "lower",
			maxConns: 2,
			want:     2,
		},
		{
			name:     "higher",
			maxConns: 9,
			want:     9,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, release := make(chan struct{}, tc.want+1), make(chan struct{})
			servers := make([]func(context.Context, net.Conn) error, tc.want)

			for i := range servers {
				servers[i] = serveH1Held(got, release)
			}

			n := newFakeNet(t, servers...)
			waiting := make(chan string, tc.want+1)
			e := newTestEngine(t, options{
				dial:     n.dial,
				maxConns: tc.maxConns,
				onWait: func(key string) {
					waiting <- key
				},
			})
			errs := make(chan error, tc.want+1)
			send := func() {
				go func() {
					resp, err := get(t, e, "http://example.test/")
					if err == nil {
						_, _ = io.Copy(io.Discard, resp.Body)
						_ = resp.Body.Close()
					}

					errs <- err
				}()
			}

			// One at a time, so each finds the others busy.
			for range tc.want {
				send()
				<-got
			}

			send()
			<-waiting

			if d := len(n.dials()); d != tc.want {
				t.Fatalf("%d connections before a request waited, want %d", d, tc.want)
			}

			close(release)

			for range tc.want + 1 {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}

			if d := len(n.dials()); d != tc.want {
				t.Fatalf("%d connections in all, want %d", d, tc.want)
			}
		})
	}
}

// queueWriter hands each write to a goroutine that writes it, so writing
// never blocks.
type queueWriter chan []byte

func (q queueWriter) Write(p []byte) (int, error) {
	q <- append([]byte(nil), p...)
	return len(p), nil
}

// goAwayServer accepts an HTTP/2 connection and, for each stream the
// client opens, calls onHeaders with a framer to answer on.
func goAwayServer(onHeaders func(fr *http2.Framer, f *http2.MetaHeadersFrame) error) func(context.Context, net.Conn) error {
	return func(ctx context.Context, conn net.Conn) error {
		tc, err := tlsServer(ctx, conn)
		if err != nil {
			return err
		}

		if _, err := io.ReadFull(tc, make([]byte, len(http2.ClientPreface))); err != nil {
			return err
		}

		// The pipe has no buffer and the client writes its whole opening
		// before it reads: queue the server's frames.
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

		// Read ahead while a handler waits, as a socket buffer would; a pipe would
		// block the client. Handlers use only what frames copy out.
		frames := make(chan http2.Frame, 64)
		go func() {
			defer close(frames)

			for {
				f, err := fr.ReadFrame()
				if err != nil {
					// The client closed the connection.
					return
				}

				frames <- f
			}
		}()

		for f := range frames {
			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					if err := fr.WriteSettingsAck(); err != nil {
						return err
					}
				}

			case *http2.MetaHeadersFrame:
				if err := onHeaders(fr, f); err != nil {
					return err
				}
			}
		}

		return nil
	}
}

// writeOK answers stream with 200 and no body.
func writeOK(fr *http2.Framer, stream uint32) error {
	var block strings.Builder

	enc := hpack.NewEncoder(&block)

	if err := enc.WriteField(hpack.HeaderField{
		Name:  ":status",
		Value: "200",
	}); err != nil {
		return err
	}

	return fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      stream,
		BlockFragment: []byte(block.String()),
		EndStream:     true,
		EndHeaders:    true,
	})
}

func TestGoAwayLetsStartedStreamsFinish(t *testing.T) {
	first, answer := make(chan struct{}), make(chan struct{})
	n := newFakeNet(t, goAwayServer(func(fr *http2.Framer, f *http2.MetaHeadersFrame) error {
		if f.StreamID == 1 {
			close(first)

			return nil
		}

		// A second request: refuse it and every later one, then finish
		// the first as promised.
		if err := fr.WriteGoAway(1, http2.ErrCodeNo, nil); err != nil {
			return err
		}

		<-answer

		return writeOK(fr, 1)
	}), serveH2)
	e := newTestEngine(t, options{
		dial: n.dial,
	})
	started := make(chan error, 1)

	go func() {
		resp, err := get(t, e, "https://localhost/slow")
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		started <- err
	}()

	<-first

	resp, err := get(t, e, "https://localhost/")
	if err != nil {
		t.Fatalf("request refused by GOAWAY: %v", err)
	}

	readAll(t, resp)
	close(answer)

	if err := <-started; err != nil {
		t.Fatalf("stream the GOAWAY promised to finish: %v", err)
	}
}

func TestHandshakeIsBoundedByTheDialTimeout(t *testing.T) {
	n := newFakeNet(t, func(_ context.Context, conn net.Conn) error {
		// Accept the ClientHello and never answer.
		_, _ = io.Copy(io.Discard, conn)
		return nil
	})
	e := newTestEngine(t, options{
		dial:        n.dial,
		dialTimeout: 50 * time.Millisecond,
	})
	done := make(chan error, 1)

	go func() {
		// No deadline on the request: only the dial timeout ends it.
		req, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodGet, "https://localhost/", nil)
		if err != nil {
			done <- err
			return
		}

		_, err = e.RoundTrip(req)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("handshake with a silent server succeeded")
		}

	case <-time.After(10 * time.Second):
		t.Fatal("the TLS handshake outlived the dial timeout")
	}
}
