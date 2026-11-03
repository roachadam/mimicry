package native

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/h2"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// A refused stream (REFUSED_STREAM) is resent on the same connection, as
// Chrome 156 and Firefox 158 do (issue #55): a server refusing every
// stream sees one connection and the profile's number of HEADERS before
// the request fails with the refusal; one refusing the first stream sees
// two HEADERS and the request succeeds. Either way the connection stays
// pooled: the next request takes it.
func TestRefusedStreamResentOnTheConnection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile *profile.Spec
		// refuse is how many streams the server refuses before it
		// answers; headers how many the first request sends.
		refuse  int32
		headers int32
		fails   bool
	}{
		{
			name:    "chrome refuses always",
			profile: profiles.ChromeMacOS().Spec(),
			refuse:  3,
			headers: 3,
			fails:   true,
		},
		{
			name:    "chrome refuses the first",
			profile: profiles.ChromeMacOS().Spec(),
			refuse:  1,
			headers: 2,
		},
		{
			name:    "firefox refuses always",
			profile: profiles.FirefoxMacOS().Spec(),
			refuse:  10,
			headers: 10,
			fails:   true,
		},
		{
			name:    "firefox refuses the first",
			profile: profiles.FirefoxMacOS().Spec(),
			refuse:  1,
			headers: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var headers atomic.Int32

			n := newFakeNet(t, goAwayServer(func(fr *http2.Framer, f *http2.MetaHeadersFrame) error {
				if headers.Add(1) <= tc.refuse {
					return fr.WriteRSTStream(f.StreamID, http2.ErrCodeRefusedStream)
				}

				return writeOK(fr, f.StreamID)
			}))
			e := newTestEngine(t, options{
				dial:    n.dial,
				profile: tc.profile,
			})

			resp, err := get(t, e, "https://localhost/")
			switch {
			case tc.fails && !errors.Is(err, h2.ErrStreamRefused):
				t.Fatalf("request to a server refusing every stream: %v, want h2.ErrStreamRefused", err)
			case !tc.fails && err != nil:
				t.Fatalf("request refused once: %v", err)
			case !tc.fails:
				readAll(t, resp)
			}

			if got := headers.Load(); got != tc.headers {
				t.Errorf("server saw %d HEADERS, want %d", got, tc.headers)
			}

			// Still pooled: the next request takes the same connection.
			resp, err = get(t, e, "https://localhost/next")
			if err != nil {
				t.Fatalf("request after the refusals: %v", err)
			}

			readAll(t, resp)

			if got := n.dials(); len(got) != 1 {
				t.Errorf("dialed %d connections, want 1: %v", len(got), got)
			}
		})
	}
}

// refusalServer accepts one HTTP/2 connection and records each stream's
// HEADERS and DATA. It refuses the first refuseHeaders streams at their
// HEADERS, and with refuseAtData the first stream whose DATA arrives;
// streams on rst are refused when the test says. Every other stream is
// answered 200 once the request has ended.
type refusalServer struct {
	refuseHeaders int
	refuseAtData  bool
	rst           chan uint32

	mu      sync.Mutex
	headers []uint32
	data    map[uint32][]byte
}

func newRefusalServer() *refusalServer {
	return &refusalServer{
		rst:  make(chan uint32),
		data: map[uint32][]byte{},
	}
}

func (s *refusalServer) streams() ([]uint32, map[uint32][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.headers), maps.Clone(s.data)
}

func (s *refusalServer) serve(ctx context.Context, conn net.Conn) error {
	tc, err := tlsServer(ctx, conn)
	if err != nil {
		return err
	}

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

	frames := make(chan http2.Frame, 64)
	go func() {
		defer close(frames)

		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return
			}

			if d, ok := f.(*http2.DataFrame); ok {
				s.mu.Lock()

				s.data[d.StreamID] = append(s.data[d.StreamID], d.Data()...)
				s.mu.Unlock()
			}

			frames <- f
		}
	}()

	refused := map[uint32]bool{}
	refuse := func(id uint32) error {
		refused[id] = true

		return fr.WriteRSTStream(id, http2.ErrCodeRefusedStream)
	}

	for {
		var err error

		select {
		case id := <-s.rst:
			err = refuse(id)

		case f, ok := <-frames:
			if !ok {
				return nil
			}

			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					err = fr.WriteSettingsAck()
				}

			case *http2.MetaHeadersFrame:
				s.mu.Lock()

				s.headers = append(s.headers, f.StreamID)

				n := len(s.headers)
				s.mu.Unlock()

				switch {
				case refused[f.StreamID]:
				case n <= s.refuseHeaders:
					err = refuse(f.StreamID)
				case f.StreamEnded():
					err = writeOK(fr, f.StreamID)
				}

			case *http2.DataFrame:
				switch {
				case refused[f.StreamID]:
				case s.refuseAtData:
					s.refuseAtData = false
					err = refuse(f.StreamID)

				case f.StreamEnded():
					err = writeOK(fr, f.StreamID)
				}
			}
		}

		if err != nil {
			return err
		}
	}
}

// gateConn holds the first Write after arm until release: the request
// whose HEADERS it carries has its stream, and nothing of it is sent.
type gateConn struct {
	net.Conn
	armed   *atomic.Bool
	blocked chan struct{}
	release chan struct{}
}

func (g gateConn) Write(b []byte) (int, error) {
	if g.armed.CompareAndSwap(true, false) {
		close(g.blocked)
		<-g.release
	}

	return g.Conn.Write(b)
}

// trackedBody is a request body without GetBody that records its reads
// and its Close; read after Close fails, as a real body's does.
type trackedBody struct {
	r      *strings.Reader
	read   atomic.Int64
	closed atomic.Bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	if b.closed.Load() {
		return 0, http.ErrBodyReadAfterClose
	}

	n, err := b.r.Read(p)
	b.read.Add(int64(n))

	return n, err
}

func (b *trackedBody) Close() error {
	b.closed.Store(true)

	return nil
}

// closedSoon reports whether b is closed within 5 seconds.
func closedSoon(b *trackedBody) bool {
	deadline := time.Now().Add(5 * time.Second)
	for !b.closed.Load() {
		if time.Now().After(deadline) {
			return false
		}

		time.Sleep(time.Millisecond)
	}

	return true
}

func post(ctx context.Context, e *Engine, body *trackedBody) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://localhost/up", body)
	if err != nil {
		return nil, err
	}

	req.ContentLength = body.r.Size()

	return e.RoundTrip(req)
}

// A refused request with a body (issue #55): refused before any of the
// body was read, it is resent whole on the same connection and the
// server receives the body once; refused once the body has started, it
// fails and is not resent, as it may have been processed; a context
// cancelled while it waits to be resent stops it, with the body closed
// and unread. A profile that never measured resending (refusedStreamSends
// 0) moves the request to a new connection.
func TestRefusedStreamWithBody(t *testing.T) {
	const payload = "a body that must reach the server exactly once"

	chrome := profiles.ChromeMacOS().Spec()

	t.Run("refused before the body", func(t *testing.T) {
		srv := newRefusalServer()
		e, gate := gatedEngine(t, chrome, srv.serve)

		readAll(t, mustGet(t, e))
		gate.armed.Store(true)

		body := &trackedBody{
			r: strings.NewReader(payload),
		}
		done := make(chan error, 1)

		go func() {
			resp, err := post(t.Context(), e, body)
			if err == nil {
				_ = resp.Body.Close()
			}

			done <- err
		}()

		refuseHeld(t, e, srv, gate, 3)
		close(gate.release)

		if err := <-done; err != nil {
			t.Fatalf("POST refused before its body: %v", err)
		}

		headers, data := srv.streams()
		if !slices.Equal(headers, []uint32{1, 3, 5}) {
			t.Errorf("HEADERS on streams %v, want [1 3 5]", headers)
		}

		if got := string(data[5]); got != payload || len(data[3]) != 0 || len(data) != 1 {
			t.Errorf("server received DATA %v, want %q once, on stream 5", data, payload)
		}

		// The body is closed by the goroutine that sent it, once its last
		// DATA frame is written, which can be after RoundTrip returns.
		if !closedSoon(body) {
			t.Error("body not closed")
		}
	})

	t.Run("refused at the last send", func(t *testing.T) {
		// One send allowed: the first refusal is the last, and the
		// body the connection left unread is closed.
		p := *chrome
		h := *p.HTTP2

		h.RefusedStreamSends = 1
		p.HTTP2 = &h

		srv := newRefusalServer()
		e, gate := gatedEngine(t, &p, srv.serve)

		readAll(t, mustGet(t, e))
		gate.armed.Store(true)

		body := &trackedBody{
			r: strings.NewReader(payload),
		}
		done := make(chan error, 1)

		go func() {
			_, err := post(t.Context(), e, body)
			done <- err
		}()

		refuseHeld(t, e, srv, gate, 3)
		close(gate.release)

		if err := <-done; !errors.Is(err, h2.ErrStreamRefused) {
			t.Fatalf("POST refused at its last send: %v, want h2.ErrStreamRefused", err)
		}

		if !body.closed.Load() || body.read.Load() != 0 {
			t.Errorf("body closed %v after %d bytes read, want closed and unread", body.closed.Load(), body.read.Load())
		}

		readAll(t, mustGet(t, e))
	})

	t.Run("refused after the body started", func(t *testing.T) {
		srv := newRefusalServer()

		srv.refuseAtData = true

		e, _ := gatedEngine(t, chrome, srv.serve)

		body := &trackedBody{
			r: strings.NewReader(payload),
		}

		_, err := post(t.Context(), e, body)
		if err == nil || errors.Is(err, h2.ErrStreamRefused) || errors.Is(err, h2.ErrConnUnusable) {
			t.Fatalf("POST refused after its body started: %v, want a failure not to resend", err)
		}

		if headers, _ := srv.streams(); !slices.Equal(headers, []uint32{1}) {
			t.Errorf("HEADERS on streams %v, want [1]: resent", headers)
		}

		// The connection stays pooled.
		readAll(t, mustGet(t, e))
	})

	t.Run("cancelled before the resend", func(t *testing.T) {
		srv := newRefusalServer()
		e, gate := gatedEngine(t, chrome, srv.serve)

		readAll(t, mustGet(t, e))
		gate.armed.Store(true)

		ctx, cancel := context.WithCancel(t.Context())
		body := &trackedBody{
			r: strings.NewReader(payload),
		}
		done := make(chan error, 1)

		go func() {
			_, err := post(ctx, e, body)
			done <- err
		}()

		refuseHeld(t, e, srv, gate, 3)
		cancel()
		close(gate.release)

		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("POST cancelled after a refusal: %v, want context.Canceled", err)
		}

		// The next request, on the same connection, is answered after
		// the server has read everything sent before it.
		readAll(t, mustGet(t, e))

		if headers, _ := srv.streams(); !slices.Equal(headers, []uint32{1, 3, 5}) {
			t.Errorf("HEADERS on streams %v, want [1 3 5]: stream 3 resent", headers)
		}

		if !body.closed.Load() || body.read.Load() != 0 {
			t.Errorf("body closed %v after %d bytes read, want closed and unread", body.closed.Load(), body.read.Load())
		}
	})

	t.Run("unmeasured moves to a new connection", func(t *testing.T) {
		p := *chrome
		h := *p.HTTP2

		h.RefusedStreamSends = 0
		p.HTTP2 = &h

		first, second := newRefusalServer(), newRefusalServer()

		first.refuseHeaders = 1

		n := newFakeNet(t, first.serve, second.serve)
		e := newTestEngine(t, options{
			dial:    n.dial,
			profile: &p,
		})

		readAll(t, mustGet(t, e))

		if got := n.dials(); len(got) != 2 {
			t.Errorf("dialed %d connections, want 2: %v", len(got), got)
		}

		h1, _ := first.streams()
		h2s, _ := second.streams()

		if len(h1) != 1 || len(h2s) != 1 {
			t.Errorf("HEADERS %v on the first connection, %v on the second, want one each", h1, h2s)
		}
	})
}

// gatedEngine is a Chrome engine whose one connection goes to serve
// through a gateConn.
func gatedEngine(t *testing.T, p *profile.Spec, serve func(context.Context, net.Conn) error) (*Engine, gateConn) {
	t.Helper()

	n := newFakeNet(t, serve)
	gate := gateConn{
		armed:   new(atomic.Bool),
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
	e := newTestEngine(t, options{
		profile: p,
		dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := n.dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			g := gate

			g.Conn = c

			return g, nil
		},
	})

	return e, gate
}

func mustGet(t *testing.T, e *Engine) *http.Response {
	t.Helper()

	resp, err := get(t, e, "https://localhost/")
	if err != nil {
		t.Fatal(err)
	}

	return resp
}

// refuseHeld refuses stream id while its HEADERS are held at the gate,
// and waits until the client has ended the stream.
func refuseHeld(t *testing.T, e *Engine, srv *refusalServer, gate gateConn, id uint32) {
	t.Helper()

	<-gate.blocked

	srv.rst <- id

	c, ok := e.pool.Shared("https://localhost:443")
	if !ok {
		t.Fatal("no pooled connection")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, idle := c.IdleSince(); idle {
			return
		}

		if time.Now().After(deadline) {
			t.Fatal("the client never ended the refused stream")
		}

		time.Sleep(time.Millisecond)
	}
}
