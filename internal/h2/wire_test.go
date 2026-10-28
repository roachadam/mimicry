package h2

import (
	"bytes"
	"encoding/binary"
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
	pings  uint64
}

// peerConfig sets up a client against a raw server peer.
type peerConfig struct {
	profile *profile.Profile
	clock   clockwork.Clock
	// wrap, if set, wraps the client's end of the pipe.
	wrap func(net.Conn) net.Conn
}

// newPeer connects a client for p to a raw server peer over net.Pipe. Frames
// the client sends arrive on peer.frames, preface excluded.
func newPeer(t *testing.T, p *profile.Profile, clock clockwork.Clock) (*Conn, *peer) {
	t.Helper()

	return newPeerWith(t, peerConfig{
		profile: p,
		clock:   clock,
	})
}

func newPeerWith(t *testing.T, cfg peerConfig) (*Conn, *peer) {
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

	conn := clientSide
	if cfg.wrap != nil {
		conn = cfg.wrap(clientSide)
	}

	c, err := New(conn, cfg.profile, cfg.clock)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = c.Close()
		_ = serverSide.Close()
	})

	return c, pr
}

// sync sends a PING and returns every frame the client sent before its ACK:
// once it returns, the client has handled every frame sent before.
func (p *peer) sync() []http2.Frame {
	p.t.Helper()

	p.pings++

	var data [8]byte
	binary.BigEndian.PutUint64(data[:], p.pings)

	if err := p.fr.WritePing(false, data); err != nil {
		p.t.Fatal(err)
	}

	var before []http2.Frame
	for {
		select {
		case f, ok := <-p.frames:
			if !ok {
				p.t.Fatal("client closed the connection")
			}

			if ping, isPing := f.(*http2.PingFrame); isPing && ping.IsAck() && ping.Data == data {
				return before
			}

			before = append(before, f)

		case <-time.After(5 * time.Second):
			p.t.Fatal("no PING ACK from the client")
		}
	}
}

// closed returns every frame the client sent until it closed the
// connection.
func (p *peer) closed() []http2.Frame {
	p.t.Helper()

	var frames []http2.Frame
	for {
		select {
		case f, ok := <-p.frames:
			if !ok {
				return frames
			}

			frames = append(frames, f)

		case <-time.After(5 * time.Second):
			p.t.Fatal("the client did not close the connection")
		}
	}
}

// headerFields sends a HEADERS frame carrying fields on stream.
func (p *peer) headerFields(stream uint32, end bool, fields ...xhpack.HeaderField) {
	p.t.Helper()
	p.buf.Reset()

	for _, f := range fields {
		_ = p.enc.WriteField(f)
	}

	if err := p.fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      stream,
		BlockFragment: p.buf.Bytes(),
		EndHeaders:    true,
		EndStream:     end,
	}); err != nil {
		p.t.Fatal(err)
	}
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
	c, pr := newPeer(t, profiles.Chrome(), clockwork.NewRealClock())

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

	if _, idle := c.IdleSince(); !idle {
		t.Fatal("streams still open after both ended")
	}
}

// A stream stops being a parent of Chrome's chained priorities as soon as
// the peer's END_STREAM is read, before the caller gets the response. The
// harness checks chaining against traces
// (TestChainedPrioritiesMatchBrowserTraces).
func TestEndedStreamLeavesChainBeforeDelivery(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome(), clockwork.NewRealClock())
	first := roundTripAsync(c, get(t, "/1"))
	h1 := pr.headers()

	var next *http2.MetaHeadersFrame

	c.onHeadersHook = func() {
		c.onHeadersHook = nil
		roundTripAsync(c, get(t, "/2"))

		// No t.Fatal off the test goroutine: a missing frame leaves next
		// nil.
		timeout := time.After(5 * time.Second)
		for next == nil {
			select {
			case f, ok := <-pr.frames:
				if !ok {
					return
				}

				next, _ = f.(*http2.MetaHeadersFrame)

			case <-timeout:
				return
			}
		}
	}

	pr.respond(h1.StreamID, 0, nil, true)

	if r := <-first; r.err != nil {
		t.Fatal(r.err)
	}

	switch {
	case next == nil:
		t.Fatal("no HEADERS for the second request")
	case !next.HasPriority() || next.Priority.StreamDep != 0 || !next.Priority.Exclusive:
		t.Fatalf("request sent once stream %d ended has priority %+v, want an exclusive dependency on stream 0", h1.StreamID, next.Priority)
	}
}

// Request bodies go out in DATA frames cut at the profile's cap from
// 65535-byte chunks, with END_STREAM on the last data frame, as captured.
func TestDataFrameCap(t *testing.T) {
	p := profiles.Chrome()
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

	var frames []string

	for {
		f := pr.next()
		d, ok := f.(*dataCopy)

		if !ok {
			continue
		}

		frame := strconv.Itoa(len(d.data))
		if d.end {
			frame += "+END"
		}

		frames = append(frames, frame)

		if d.end {
			break
		}
	}

	want := []string{"16375", "16375", "16375", "16375", "35", "4465+END"}
	if !slices.Equal(frames, want) {
		t.Fatalf("DATA frames %v, want %v", frames, want)
	}

	pr.respond(h.StreamID, 0, nil, true)
	<-result
}

// Chrome returns any owed credit 5 s after its last WINDOW_UPDATE,
// connection first, as in the slow-idle captures.
func TestWindowUpdateInterval(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	c, pr := newPeer(t, profiles.Chrome(), clock)
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

		if wu, ok := f.(*http2.WindowUpdateFrame); ok && wu.Increment != profiles.Chrome().HTTP2.WindowUpdate {
			got = append(got, fmt.Sprintf("%d+%d", wu.StreamID, wu.Increment))
		}
	}

	if want := []string{"0+1500", fmt.Sprintf("%d+1500", h.StreamID)}; !slices.Equal(got, want) {
		t.Fatalf("credit returned as %v, want %v", got, want)
	}

	_ = r.resp.Body.Close()
}

// A Chrome request after 10 s idle sends a counting PING after its HEADERS,
// and an unanswered PING closes the connection (slow-idle captures). Safari
// sends no PING.
func TestIdlePing(t *testing.T) {
	clock := clockwork.NewFakeClockAt(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	c, pr := newPeer(t, profiles.Chrome(), clock)
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

	// The client must have read the ACK before the clock moves on, or it
	// counts the ACK as recent traffic and rightly sends no PING.
	pr.sync()

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
	sc, spr := newPeer(t, profiles.Safari(), safari)
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

// When the server closes the connection, a request already sent fails
// with a non-retryable error, since it may have been processed.
func TestInFlightRequestNotRetriedOnClose(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome(), clockwork.NewRealClock())

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
