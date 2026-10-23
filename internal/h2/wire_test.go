package h2

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	xhpack "golang.org/x/net/http2/hpack"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// peer is the server end of a client connection, driven frame by frame.
type peer struct {
	t      *testing.T
	conn   net.Conn
	fr     *http2.Framer
	frames chan http2.Frame
	enc    *xhpack.Encoder
	buf    bytes.Buffer
}

// newPeer connects a client for p to a raw server peer over net.Pipe. Frames
// the client sends arrive on peer.frames, preface excluded.
func newPeer(t *testing.T, p *profile.Profile, clock clockwork.Clock) (*Conn, *peer) {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	pr := &peer{
		t:      t,
		conn:   serverSide,
		fr:     http2.NewFramer(serverSide, serverSide),
		frames: make(chan http2.Frame, 256),
	}
	pr.fr.ReadMetaHeaders = xhpack.NewDecoder(4096, nil)
	pr.enc = xhpack.NewEncoder(&pr.buf)
	go func() {
		defer close(pr.frames)
		preface := make([]byte, len(http2.ClientPreface))
		if _, err := io.ReadFull(serverSide, preface); err != nil {
			return
		}
		for {
			f, err := pr.fr.ReadFrame()
			if err != nil {
				return
			}
			// ReadFrame reuses DATA buffers; keep a copy of what tests need.
			if d, ok := f.(*http2.DataFrame); ok {
				f = &dataCopy{
					stream: d.StreamID,
					data:   bytes.Clone(d.Data()),
					end:    d.StreamEnded(),
				}
			}
			pr.frames <- f
		}
	}()
	c, err := New(clientSide, p, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		_ = serverSide.Close()
	})
	return c, pr
}

type dataCopy struct {
	http2.Frame
	stream uint32
	data   []byte
	end    bool
}

// next returns the next frame the client sent that is not part of its
// connection preface (SETTINGS, connection WINDOW_UPDATE, PRIORITY).
func (p *peer) next() http2.Frame {
	p.t.Helper()
	for {
		select {
		case f, ok := <-p.frames:
			if !ok {
				p.t.Fatal("client closed the connection")
			}
			switch f := f.(type) {
			case *http2.SettingsFrame, *http2.PriorityFrame:
				continue
			case *http2.WindowUpdateFrame:
				if f.StreamID == 0 {
					continue
				}
			}
			return f
		case <-time.After(5 * time.Second):
			p.t.Fatal("no frame from the client")
		}
	}
}

func (p *peer) headers() *http2.MetaHeadersFrame {
	p.t.Helper()
	f := p.next()
	h, ok := f.(*http2.MetaHeadersFrame)
	if !ok {
		p.t.Fatalf("got %T, want HEADERS", f)
	}
	return h
}

// respond sends response headers with content-length n and, if body is not
// nil, a DATA frame carrying it; end ends the stream on the last frame sent.
func (p *peer) respond(stream uint32, n int, body []byte, end bool) {
	p.t.Helper()
	p.buf.Reset()
	_ = p.enc.WriteField(xhpack.HeaderField{
		Name:  ":status",
		Value: "200",
	})
	_ = p.enc.WriteField(xhpack.HeaderField{
		Name:  "content-length",
		Value: strconv.Itoa(n),
	})
	if err := p.fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      stream,
		BlockFragment: p.buf.Bytes(),
		EndHeaders:    true,
		EndStream:     end && body == nil,
	}); err != nil {
		p.t.Fatal(err)
	}
	if body != nil {
		if err := p.fr.WriteData(stream, end, body); err != nil {
			p.t.Fatal(err)
		}
	}
}

func get(t *testing.T, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// roundTripAsync starts req and delivers its result.
func roundTripAsync(c *Conn, req *http.Request) <-chan responseOrError {
	ch := make(chan responseOrError, 1)
	go func() {
		resp, err := c.RoundTrip(req)
		ch <- responseOrError{
			resp: resp,
			err:  err,
		}
	}()
	return ch
}

// A body read to its declared length and then closed waits for the server's
// END_STREAM instead of resetting the stream; one closed early is reset.
func TestClosingFullyReadBodyDoesNotReset(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())

	result := roundTripAsync(c, get(t, "/full"))
	h := pr.headers()
	pr.respond(h.StreamID, 5, []byte("hello"), false)
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	if b, err := io.ReadAll(io.LimitReader(r.resp.Body, 5)); err != nil || string(b) != "hello" {
		t.Fatalf("body %q, %v", b, err)
	}
	_ = r.resp.Body.Close()

	// The server ends the stream a moment later in an empty DATA frame.
	if err := pr.fr.WriteData(h.StreamID, true, nil); err != nil {
		t.Fatal(err)
	}
	// The next request's HEADERS is the next frame: no RST_STREAM before it.
	result = roundTripAsync(c, get(t, "/partial"))
	h2f := pr.headers()
	pr.respond(h2f.StreamID, 5, []byte("hello"), false)
	r = <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	if _, err := r.resp.Body.Read(make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
	_ = r.resp.Body.Close()
	rst, ok := pr.next().(*http2.RSTStreamFrame)
	if !ok || rst.StreamID != h2f.StreamID || rst.ErrCode != http2.ErrCodeCancel {
		t.Fatalf("closing a partly read body sent %+v, want RST_STREAM(CANCEL) on stream %d", rst, h2f.StreamID)
	}
	if !c.Idle() {
		t.Fatal("streams still open after both ended")
	}
}

// Firefox starts at stream 3 and tops up every stream's window right after
// its HEADERS.
func TestFirstStreamAndStreamWindowUpdate(t *testing.T) {
	p := profiles.Firefox158MacOS()
	c, pr := newPeer(t, p, clockwork.NewRealClock())
	for _, want := range []uint32{3, 5} {
		result := roundTripAsync(c, get(t, "/"))
		h := pr.headers()
		if h.StreamID != want {
			t.Fatalf("request on stream %d, want %d", h.StreamID, want)
		}
		wu, ok := pr.next().(*http2.WindowUpdateFrame)
		if !ok || wu.StreamID != want || wu.Increment != p.HTTP2.StreamWindowUpdate {
			t.Fatalf("after HEADERS got %+v, want WINDOW_UPDATE(%d) on stream %d", wu, p.HTTP2.StreamWindowUpdate, want)
		}
		pr.respond(want, 0, nil, true)
		if r := <-result; r.err != nil {
			t.Fatal(r.err)
		}
	}
}

// Chrome makes each request depend, exclusively, on the newest stream still
// open, and on stream 0 when none is.
func TestChainedPriorities(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	first := roundTripAsync(c, get(t, "/1"))
	h1 := pr.headers()
	second := roundTripAsync(c, get(t, "/2"))
	h2f := pr.headers()
	if !h2f.HasPriority() || h2f.Priority.StreamDep != h1.StreamID || !h2f.Priority.Exclusive {
		t.Fatalf("second request priority %+v, want exclusive dependency on stream %d", h2f.Priority, h1.StreamID)
	}
	pr.respond(h1.StreamID, 0, nil, true)
	pr.respond(h2f.StreamID, 0, nil, true)
	<-first
	<-second
	third := roundTripAsync(c, get(t, "/3"))
	h3 := pr.headers()
	if h3.Priority.StreamDep != 0 {
		t.Fatalf("with no stream open, depends on %d, want 0", h3.Priority.StreamDep)
	}
	pr.respond(h3.StreamID, 0, nil, true)
	<-third
}

// Request bodies go out in DATA frames no larger than the profile's cap, cut
// from 65535-byte chunks as Chrome cuts its own.
func TestDataFrameCap(t *testing.T) {
	p := profiles.Chrome155MacOS()
	c, pr := newPeer(t, p, clockwork.NewRealClock())
	// Room for the whole upload, as the capture server allows.
	if err := pr.fr.WriteWindowUpdate(0, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := pr.fr.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("x"), 70000)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/upload", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	result := roundTripAsync(c, req)
	h := pr.headers()
	var sizes []int
	for {
		f := pr.next()
		d, ok := f.(*dataCopy)
		if !ok {
			continue
		}
		if len(d.data) > 0 {
			sizes = append(sizes, len(d.data))
		}
		if d.end {
			break
		}
	}
	want := []int{16375, 16375, 16375, 16375, 35, 4465}
	if !slices.Equal(sizes, want) {
		t.Fatalf("DATA payloads %v, want %v", sizes, want)
	}
	pr.respond(h.StreamID, 0, nil, true)
	<-result
}

// Cookies go out one per field, as every captured browser sends them, in the
// profile's order: creation order for Chrome, by name for Safari.
func TestCookieSplitAndOrder(t *testing.T) {
	for _, tt := range []struct {
		p    *profile.Profile
		want string
	}{
		{profiles.Chrome155MacOS(), "b=2|a=1|c=3"},
		{profiles.Safari27MacOS(), "a=1|b=2|c=3"},
	} {
		c, pr := newPeer(t, tt.p, clockwork.NewRealClock())
		req := get(t, "/")
		req.Header.Set("Cookie", "b=2; a=1;c=3")
		result := roundTripAsync(c, req)
		h := pr.headers()
		var cookies []string
		for _, f := range h.Fields {
			if f.Name == "cookie" {
				cookies = append(cookies, f.Value)
			}
		}
		if got := strings.Join(cookies, "|"); got != tt.want {
			t.Errorf("%s: cookie fields %s, want %s", tt.p.Name, got, tt.want)
		}
		pr.respond(h.StreamID, 0, nil, true)
		<-result
	}
}

// Chrome also returns credit once 5 s have passed since its last
// WINDOW_UPDATE, however little is owed, connection first (captured in the
// slow-idle scenario).
func TestWindowUpdateInterval(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clock)
	result := roundTripAsync(c, get(t, "/slow"))
	h := pr.headers()
	if err := pr.fr.WriteHeaders(http2.HeadersFrameParam{StreamID: h.StreamID, BlockFragment: []byte{0x88}, EndHeaders: true}); err != nil {
		t.Fatal(err)
	}
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	read := func(n int) {
		t.Helper()
		if err := pr.fr.WriteData(h.StreamID, false, make([]byte, n)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(r.resp.Body, make([]byte, n)); err != nil {
			t.Fatal(err)
		}
	}
	read(1000) // far below both thresholds: no credit yet
	clock.Advance(5 * time.Second)
	read(500)
	// next() hides connection WINDOW_UPDATEs as preface frames; collect the
	// credit returns from the raw frames, past the preface's.
	var got []string
	for len(got) < 2 {
		var f http2.Frame
		select {
		case f = <-pr.frames:
		case <-time.After(5 * time.Second):
			t.Fatalf("no credit returned after 5 s; got %v", got)
		}
		if wu, ok := f.(*http2.WindowUpdateFrame); ok && wu.Increment != profiles.Chrome155MacOS().HTTP2.WindowUpdate {
			got = append(got, fmt.Sprintf("%d+%d", wu.StreamID, wu.Increment))
		}
	}
	if want := []string{"0+1500", fmt.Sprintf("%d+1500", h.StreamID)}; !slices.Equal(got, want) {
		t.Fatalf("credit returned as %v, want %v", got, want)
	}
	_ = r.resp.Body.Close()
}

// A Chrome request after 10 s without reading anything sends a PING after its
// HEADERS, counting from 1 (captured in the slow-idle scenario). One that is
// not answered within 10 s more closes the connection, and the request in
// flight fails without being retried elsewhere. Safari sends no PING.
func TestIdlePing(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clock)
	clock.Advance(11 * time.Second)
	first := roundTripAsync(c, get(t, "/1"))
	h := pr.headers()
	ping, ok := pr.next().(*http2.PingFrame)
	if !ok || ping.IsAck() || ping.Data != [8]byte{0, 0, 0, 0, 0, 0, 0, 1} {
		t.Fatalf("after HEADERS got %+v, want PING 0000000000000001", ping)
	}
	pr.respond(h.StreamID, 0, nil, true)
	if r := <-first; r.err != nil {
		t.Fatal(r.err)
	}
	if err := pr.fr.WritePing(true, ping.Data); err != nil {
		t.Fatal(err)
	}

	// Quiet again, and this time the PING is never answered.
	clock.Advance(11 * time.Second)
	second := roundTripAsync(c, get(t, "/2"))
	pr.headers()
	if ping, ok := pr.next().(*http2.PingFrame); !ok || ping.Data[7] != 2 {
		t.Fatalf("got %+v, want PING 0000000000000002", ping)
	}
	clock.Advance(10 * time.Second)
	r := <-second
	if r.err == nil || errors.Is(r.err, ErrConnUnusable) {
		t.Fatalf("in-flight request on a dead connection: %v, want a non-retryable error", r.err)
	}
	if c.CanTakeNewRequest() {
		t.Fatal("connection still takes requests after its PING went unanswered")
	}

	safari := clockwork.NewFakeClockAt(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	sc, spr := newPeer(t, profiles.Safari27MacOS(), safari)
	safari.Advance(30 * time.Second)
	res := roundTripAsync(sc, get(t, "/"))
	sh := spr.headers()
	spr.respond(sh.StreamID, 0, nil, true)
	<-res
	// The next frame Safari sends, if any, must not be a PING.
	_ = sc.Close()
	for f := range spr.frames {
		if _, isPing := f.(*http2.PingFrame); isPing {
			t.Fatal("Safari sent a PING")
		}
	}
}

// When the server closes the connection, a request already sent may have
// been processed: it fails with an error the engine does not retry, unlike a
// request that had not started (ErrConnUnusable).
func TestInFlightRequestNotRetriedOnClose(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/pay", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	inFlight := roundTripAsync(c, req)
	pr.headers()
	_ = pr.conn.Close()
	r := <-inFlight
	if r.err == nil || errors.Is(r.err, ErrConnUnusable) {
		t.Fatalf("in-flight POST on a closed connection: %v, want a non-retryable error", r.err)
	}
	if _, err := c.RoundTrip(get(t, "/next")); !errors.Is(err, ErrConnUnusable) {
		t.Fatalf("new request on the closed connection: %v, want ErrConnUnusable", err)
	}
}
