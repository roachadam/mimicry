package h2

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http2"
	xhpack "golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// trackedBody is a request body that records whether it was read or
// closed.
type trackedBody struct {
	r      io.Reader
	read   atomic.Bool
	closed chan struct{}
	once   sync.Once
}

func newTrackedBody(b string) *trackedBody {
	return &trackedBody{
		r:      strings.NewReader(b),
		closed: make(chan struct{}),
	}
}

func (b *trackedBody) Read(p []byte) (int, error) {
	b.read.Store(true)
	return b.r.Read(p)
}

func (b *trackedBody) Close() error {
	b.once.Do(func() {
		close(b.closed)
	})
	return nil
}

func (b *trackedBody) isClosed() bool {
	select {
	case <-b.closed:
		return true
	default:
		return false
	}
}

func (b *trackedBody) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-b.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("request body never closed")
	}
}

func post(t *testing.T, body io.ReadCloser, length int64) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.com/post", body)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = length
	return req
}

func waitDone(t *testing.T, c *Conn) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection not closed")
	}
}

// find returns the first frame of type T that match accepts.
func find[T http2.Frame](frames []http2.Frame, match func(T) bool) (T, bool) {
	for _, f := range frames {
		if v, ok := f.(T); ok && match(v) {
			return v, true
		}
	}
	var zero T
	return zero, false
}

func hasReset(frames []http2.Frame, stream uint32, code http2.ErrCode) bool {
	_, ok := find(frames, func(f *http2.RSTStreamFrame) bool {
		return f.StreamID == stream && f.ErrCode == code
	})
	return ok
}

func hasGoAway(frames []http2.Frame, code http2.ErrCode) bool {
	_, ok := find(frames, func(f *http2.GoAwayFrame) bool {
		return f.ErrCode == code
	})
	return ok
}

func streamErrCode(err error) (http2.ErrCode, bool) {
	var se http2.StreamError
	if !errors.As(err, &se) {
		return 0, false
	}
	return se.Code, true
}

func setting(p *profile.Profile, id http2.SettingID) uint32 {
	for _, s := range p.HTTP2.Settings {
		if http2.SettingID(s.ID) == id {
			return s.Value
		}
	}
	return 0
}

// A request cancelled after the read loop found its stream for the response
// HEADERS, and before it delivered them, fails with the context's error and
// leaves the read loop running.
func TestCancelAsResponseHeadersArrive(t *testing.T) {
	c, pr := newPeer(t, profiles.Safari27MacOS(), clockwork.NewRealClock())
	ctx, cancel := context.WithCancel(t.Context())
	result := roundTripAsync(c, get(t, "/").WithContext(ctx))
	h := pr.headers()
	var cancelled responseOrError
	c.onHeadersHook = func() {
		c.onHeadersHook = nil
		cancel()
		cancelled = <-result
	}
	pr.respond(h.StreamID, 0, nil, false)
	frames := pr.sync()
	if !errors.Is(cancelled.err, context.Canceled) {
		t.Fatalf("cancelled request returned %v, want context.Canceled", cancelled.err)
	}
	if !hasReset(frames, h.StreamID, http2.ErrCodeCancel) {
		t.Fatalf("no RST_STREAM(CANCEL) on stream %d in %v", h.StreamID, frames)
	}
	if !c.Idle() || !c.CanTakeNewRequest() {
		t.Fatal("the cancelled stream is still open or broke the connection")
	}
}

// Response bytes nobody reads, because the body was closed early or after
// the stream ended, go back to the connection window: otherwise a few
// abandoned downloads stall every later response.
func TestClosedBodyReturnsConnectionCredit(t *testing.T) {
	p := profiles.Safari27MacOS()
	c, pr := newPeer(t, p, clockwork.NewRealClock())
	window := int(setting(p, http2.SettingInitialWindowSize))
	chunk := make([]byte, 16384)
	bodies := make([]io.ReadCloser, 0, 3)
	for i := range 3 {
		result := roundTripAsync(c, get(t, "/big"))
		h := pr.headers()
		pr.respond(h.StreamID, window, nil, false)
		r := <-result
		if r.err != nil {
			t.Fatal(r.err)
		}
		for sent := 0; sent < window; sent += len(chunk) {
			// The last stream ends: its unread bytes outlive it.
			end := i == 2 && sent+len(chunk) == window
			if err := pr.fr.WriteData(h.StreamID, end, chunk); err != nil {
				t.Fatal(err)
			}
		}
		bodies = append(bodies, r.resp.Body)
	}
	pr.sync()
	for _, b := range bodies {
		_ = b.Close()
	}
	var credit int
	for _, f := range pr.sync() {
		if wu, ok := f.(*http2.WindowUpdateFrame); ok && wu.StreamID == 0 {
			credit += int(wu.Increment)
		}
	}
	if credit != 3*window {
		t.Fatalf("connection credit returned %d, want the %d bytes discarded", credit, 3*window)
	}
}

// Cancelling the request context ends a body read in progress and resets
// the stream.
func TestContextBoundsBodyRead(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	ctx, cancel := context.WithCancel(t.Context())
	result := roundTripAsync(c, get(t, "/").WithContext(ctx))
	h := pr.headers()
	pr.respond(h.StreamID, 10, []byte("hello"), false)
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	read := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(r.resp.Body)
		read <- err
	}()
	cancel()
	select {
	case err := <-read:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("body read returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("body read ignores the cancelled context")
	}
	if frames := pr.sync(); !hasReset(frames, h.StreamID, http2.ErrCodeCancel) {
		t.Fatalf("no RST_STREAM(CANCEL) on stream %d in %v", h.StreamID, frames)
	}
}

// A request waiting for the peer's MAX_CONCURRENT_STREAMS to leave room
// gives up when its context ends, without sending anything.
func TestContextBoundsWaitForStream(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	if err := pr.fr.WriteSettings(http2.Setting{
		ID:  http2.SettingMaxConcurrentStreams,
		Val: 1,
	}); err != nil {
		t.Fatal(err)
	}
	pr.sync()
	first := roundTripAsync(c, get(t, "/1"))
	h := pr.headers()
	for _, early := range []bool{true, false} {
		ctx, cancel := context.WithCancel(t.Context())
		if early {
			cancel()
		}
		waiting := roundTripAsync(c, get(t, "/2").WithContext(ctx))
		cancel()
		select {
		case r := <-waiting:
			if !errors.Is(r.err, context.Canceled) {
				t.Fatalf("queued request returned %v, want context.Canceled", r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("request queued for a stream ignores its context")
		}
	}
	pr.respond(h.StreamID, 0, nil, true)
	if r := <-first; r.err != nil {
		t.Fatal(r.err)
	}
	if _, sent := find(pr.sync(), func(*http2.MetaHeadersFrame) bool {
		return true
	}); sent {
		t.Fatal("a cancelled queued request was sent")
	}
}

// A body whose length net/http could not measure (ContentLength 0, Body
// set) is sent in DATA frames without a content-length, not dropped.
func TestUnknownLengthBody(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	req := newRequest(t, http.MethodPost, "https://example.com/post", io.NopCloser(strings.NewReader("payload")))
	if req.ContentLength != 0 || req.Body == nil {
		t.Fatalf("ContentLength %d: not the unknown-length case", req.ContentLength)
	}
	result := roundTripAsync(c, req)
	h := pr.headers()
	if h.StreamEnded() {
		t.Fatal("HEADERS ended the stream: the body was dropped")
	}
	for _, f := range h.Fields {
		if f.Name == "content-length" {
			t.Fatalf("content-length %q sent for a body of unknown length", f.Value)
		}
	}
	var frames []string
	for {
		d, ok := pr.next().(*dataCopy)
		if !ok {
			continue
		}
		frame := string(d.data)
		if d.end {
			frame += "+END"
		}
		frames = append(frames, frame)
		if d.end {
			break
		}
	}
	if want := []string{"payload", "+END"}; !slices.Equal(frames, want) {
		t.Fatalf("DATA frames %q, want %q", frames, want)
	}
	pr.respond(h.StreamID, 0, nil, true)
	if r := <-result; r.err != nil {
		t.Fatal(r.err)
	}
}

// A request that cannot be put on the wire as given fails before anything
// is sent, and its body is closed.
func TestInvalidRequestRejected(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(*http.Request)
	}{
		{
			name: "header value",
			edit: func(r *http.Request) {
				r.Header.Set("X-Note", "a\r\nX-Injected: yes")
			},
		},
		{
			name: "header name",
			edit: func(r *http.Request) {
				r.Header["Bad Name"] = []string{
					"x",
				}
			},
		},
		{
			name: "method",
			edit: func(r *http.Request) {
				r.Method = "GET /x HTTP/1.1\r\n"
			},
		},
		{
			name: "host",
			edit: func(r *http.Request) {
				r.Host = "example.com\r\nX-Injected: yes"
			},
		},
		{
			name: "path",
			edit: func(r *http.Request) {
				r.URL.Opaque = "/a\r\nb"
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
			body := newTrackedBody("x")
			req := post(t, body, 1)
			tt.edit(req)
			resp, err := c.RoundTrip(req)
			if err == nil {
				_ = resp.Body.Close()
			}
			if err == nil || errors.Is(err, ErrConnUnusable) {
				t.Fatalf("RoundTrip returned %v, want an invalid request error", err)
			}
			if !body.isClosed() {
				t.Fatal("request body not closed")
			}
			frames := pr.sync()
			if _, sent := find(frames, func(*http2.MetaHeadersFrame) bool {
				return true
			}); sent {
				t.Fatal("an invalid request was sent")
			}
			if !c.Idle() || !c.CanTakeNewRequest() {
				t.Fatal("the rejected request left a stream open or broke the connection")
			}
		})
	}
}

// A body longer or shorter than its ContentLength fails the request and
// resets the stream, never sending past the declared length.
func TestBodyLengthMismatch(t *testing.T) {
	for _, tt := range []struct {
		name   string
		body   string
		length int64
		want   error
		data   []string
	}{
		{
			name:   "longer",
			body:   "helloGET /smuggled HTTP/1.1",
			length: 5,
			want:   errBodyTooLong,
		},
		{
			name:   "shorter",
			body:   "hello",
			length: 10,
			want:   errBodyTooShort,
			data: []string{
				"hello",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
			body := newTrackedBody(tt.body)
			result := roundTripAsync(c, post(t, body, tt.length))
			h := pr.headers()
			if r := <-result; !errors.Is(r.err, tt.want) {
				t.Fatalf("RoundTrip returned %v, want %v", r.err, tt.want)
			}
			body.waitClosed(t)
			frames := pr.sync()
			var data []string
			for _, f := range frames {
				if d, ok := f.(*dataCopy); ok {
					data = append(data, string(d.data))
				}
			}
			if !slices.Equal(data, tt.data) {
				t.Fatalf("DATA sent %q, want %q", data, tt.data)
			}
			if !hasReset(frames, h.StreamID, http2.ErrCodeCancel) {
				t.Fatalf("no RST_STREAM(CANCEL) on stream %d in %v", h.StreamID, frames)
			}
		})
	}
}

// A peer that sends past the stream window we advertised gets that stream
// reset with FLOW_CONTROL_ERROR; the connection keeps working.
func TestStreamReceiveWindowEnforced(t *testing.T) {
	p := profiles.Safari27MacOS()
	c, pr := newPeer(t, p, clockwork.NewRealClock())
	window := int(setting(p, http2.SettingInitialWindowSize))
	result := roundTripAsync(c, get(t, "/"))
	h := pr.headers()
	pr.headerFields(h.StreamID, false, xhpack.HeaderField{
		Name:  ":status",
		Value: "200",
	})
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	chunk := make([]byte, 16384)
	for sent := 0; sent < window; sent += len(chunk) {
		if err := pr.fr.WriteData(h.StreamID, false, chunk); err != nil {
			t.Fatal(err)
		}
	}
	// One byte past the window.
	if err := pr.fr.WriteData(h.StreamID, false, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if frames := pr.sync(); !hasReset(frames, h.StreamID, http2.ErrCodeFlowControl) {
		t.Fatalf("no RST_STREAM(FLOW_CONTROL_ERROR) on stream %d in %v", h.StreamID, frames)
	}
	if _, err := io.ReadAll(r.resp.Body); err == nil {
		t.Fatal("body of the reset stream read without error")
	}
	if !c.CanTakeNewRequest() {
		t.Fatal("a stream's flow-control error broke the connection")
	}
}

// A peer that sends past the connection window we advertised gets GOAWAY
// with FLOW_CONTROL_ERROR.
func TestConnectionReceiveWindowEnforced(t *testing.T) {
	p := profiles.Safari27MacOS()
	// The connection window is the default 65535, smaller than a stream's.
	p.HTTP2.WindowUpdate = 0
	c, pr := newPeer(t, p, clockwork.NewRealClock())
	result := roundTripAsync(c, get(t, "/"))
	h := pr.headers()
	pr.headerFields(h.StreamID, false, xhpack.HeaderField{
		Name:  ":status",
		Value: "200",
	})
	if r := <-result; r.err != nil {
		t.Fatal(r.err)
	}
	for _, n := range []int{16384, 16384, 16384, 16383, 1} {
		if err := pr.fr.WriteData(h.StreamID, false, make([]byte, n)); err != nil {
			t.Fatal(err)
		}
	}
	if frames := pr.closed(); !hasGoAway(frames, http2.ErrCodeFlowControl) {
		t.Fatalf("no GOAWAY(FLOW_CONTROL_ERROR) in %v", frames)
	}
	waitDone(t, c)
}

// A stream error (a zero WINDOW_UPDATE on a stream, RFC 9113, 6.9) resets
// that stream only; the others and the connection carry on.
func TestStreamErrorResetsOnlyItsStream(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	first := roundTripAsync(c, get(t, "/1"))
	h1 := pr.headers()
	second := roundTripAsync(c, get(t, "/2"))
	h2f := pr.headers()
	pr.respond(h2f.StreamID, 5, []byte("hello"), true)
	r2 := <-second
	if r2.err != nil {
		t.Fatal(r2.err)
	}
	if err := pr.fr.WriteRawFrame(http2.FrameWindowUpdate, 0, h1.StreamID, []byte{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	r1 := <-first
	if code, ok := streamErrCode(r1.err); !ok || code != http2.ErrCodeProtocol {
		t.Fatalf("stream %d returned %v, want a PROTOCOL_ERROR stream error", h1.StreamID, r1.err)
	}
	if b, err := io.ReadAll(r2.resp.Body); err != nil || string(b) != "hello" {
		t.Fatalf("other stream's body %q, %v", b, err)
	}
	if frames := pr.sync(); !hasReset(frames, h1.StreamID, http2.ErrCodeProtocol) {
		t.Fatalf("no RST_STREAM(PROTOCOL_ERROR) on stream %d in %v", h1.StreamID, frames)
	}
	if !c.CanTakeNewRequest() {
		t.Fatal("a stream error broke the connection")
	}
}

// After GOAWAY, a stream past LastStreamID fails with ErrConnUnusable, no
// request starts, the streams the peer will process run to completion, and
// the connection closes after the last.
func TestGoAwayDrains(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	first := roundTripAsync(c, get(t, "/1"))
	h1 := pr.headers()
	second := roundTripAsync(c, get(t, "/2"))
	pr.headers()
	if err := pr.fr.WriteGoAway(h1.StreamID, http2.ErrCodeNo, nil); err != nil {
		t.Fatal(err)
	}
	if r := <-second; !errors.Is(r.err, ErrConnUnusable) {
		t.Fatalf("stream past LastStreamID returned %v, want ErrConnUnusable", r.err)
	}
	if c.CanTakeNewRequest() {
		t.Fatal("connection takes requests after GOAWAY")
	}
	if _, err := c.RoundTrip(get(t, "/3")); !errors.Is(err, ErrConnUnusable) {
		t.Fatalf("new request after GOAWAY returned %v, want ErrConnUnusable", err)
	}
	pr.sync()
	select {
	case <-c.Done():
		t.Fatal("connection closed with a stream the peer will still answer")
	default:
	}
	pr.respond(h1.StreamID, 2, []byte("ok"), true)
	r := <-first
	if r.err != nil {
		t.Fatal(r.err)
	}
	if b, err := io.ReadAll(r.resp.Body); err != nil || string(b) != "ok" {
		t.Fatalf("stream within LastStreamID read %q, %v", b, err)
	}
	pr.closed()
	waitDone(t, c)
}

// GOAWAY on a connection with nothing in flight closes it.
func TestGoAwayClosesIdleConnection(t *testing.T) {
	c, pr := newPeer(t, profiles.Firefox158MacOS(), clockwork.NewRealClock())
	if err := pr.fr.WriteGoAway(0, http2.ErrCodeNo, nil); err != nil {
		t.Fatal(err)
	}
	pr.closed()
	waitDone(t, c)
}

// A stream the peer resets fails with its error code and is not retried;
// one it refused (REFUSED_STREAM) was not processed, so it fails with
// ErrConnUnusable, and the connection, which then takes no new request,
// closes once idle.
func TestPeerResetsStream(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	result := roundTripAsync(c, get(t, "/"))
	h := pr.headers()
	if err := pr.fr.WriteRSTStream(h.StreamID, http2.ErrCodeInternal); err != nil {
		t.Fatal(err)
	}
	r := <-result
	if code, ok := streamErrCode(r.err); !ok || code != http2.ErrCodeInternal || errors.Is(r.err, ErrConnUnusable) {
		t.Fatalf("reset stream returned %v, want a non-retryable INTERNAL_ERROR", r.err)
	}
	if !c.CanTakeNewRequest() {
		t.Fatal("a reset stream broke the connection")
	}

	result = roundTripAsync(c, get(t, "/"))
	h = pr.headers()
	if err := pr.fr.WriteRSTStream(h.StreamID, http2.ErrCodeRefusedStream); err != nil {
		t.Fatal(err)
	}
	if r := <-result; !errors.Is(r.err, ErrConnUnusable) {
		t.Fatalf("refused stream returned %v, want ErrConnUnusable", r.err)
	}
	if c.CanTakeNewRequest() {
		t.Fatal("connection takes requests after refusing a stream")
	}
	pr.closed()
	waitDone(t, c)
}

// A refused request whose body was never read fails with ErrConnUnusable
// and leaves the body untouched for the retry; one refused after its body
// was read cannot be retried with it.
func TestRefusedRequestBody(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	result := roundTripAsync(c, post(t, newTrackedBody("hello"), 5))
	h := pr.headers()
	for {
		if d, ok := pr.next().(*dataCopy); ok && d.end {
			break
		}
	}
	if err := pr.fr.WriteRSTStream(h.StreamID, http2.ErrCodeRefusedStream); err != nil {
		t.Fatal(err)
	}
	if r := <-result; r.err == nil || errors.Is(r.err, ErrConnUnusable) {
		t.Fatalf("refused after its body was sent: %v, want a non-retryable error", r.err)
	}

	c, pr = newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
	inFlight := roundTripAsync(c, get(t, "/"))
	h = pr.headers()
	if err := pr.fr.WriteGoAway(h.StreamID, http2.ErrCodeNo, nil); err != nil {
		t.Fatal(err)
	}
	pr.sync()
	body := newTrackedBody("hello")
	if _, err := c.RoundTrip(post(t, body, 5)); !errors.Is(err, ErrConnUnusable) {
		t.Fatalf("request after GOAWAY returned %v, want ErrConnUnusable", err)
	}
	if body.read.Load() || body.isClosed() {
		t.Fatal("the body of a request that was not sent was read or closed")
	}
	pr.respond(h.StreamID, 0, nil, true)
	<-inFlight
}

// A WINDOW_UPDATE or SETTINGS change that takes a send window past 2^31-1
// is a FLOW_CONTROL_ERROR: for the connection (GOAWAY) or the stream
// (RST_STREAM), never a window wrapped negative.
func TestSendWindowOverflow(t *testing.T) {
	t.Run("connection", func(t *testing.T) {
		c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
		if err := pr.fr.WriteWindowUpdate(0, maxWindow); err != nil {
			t.Fatal(err)
		}
		if frames := pr.closed(); !hasGoAway(frames, http2.ErrCodeFlowControl) {
			t.Fatalf("no GOAWAY(FLOW_CONTROL_ERROR) in %v", frames)
		}
		waitDone(t, c)
	})
	t.Run("stream", func(t *testing.T) {
		c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
		result := roundTripAsync(c, get(t, "/"))
		h := pr.headers()
		if err := pr.fr.WriteWindowUpdate(h.StreamID, maxWindow); err != nil {
			t.Fatal(err)
		}
		if r := <-result; r.err == nil {
			t.Fatal("request on an overflowed stream succeeded")
		}
		if frames := pr.sync(); !hasReset(frames, h.StreamID, http2.ErrCodeFlowControl) {
			t.Fatalf("no RST_STREAM(FLOW_CONTROL_ERROR) on stream %d in %v", h.StreamID, frames)
		}
		if !c.CanTakeNewRequest() {
			t.Fatal("a stream's overflow broke the connection")
		}
	})
	t.Run("settings", func(t *testing.T) {
		c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
		result := roundTripAsync(c, get(t, "/"))
		h := pr.headers()
		// The stream's window reaches the maximum, then SETTINGS adds one.
		if err := pr.fr.WriteWindowUpdate(h.StreamID, maxWindow-defaultWindow); err != nil {
			t.Fatal(err)
		}
		if err := pr.fr.WriteSettings(http2.Setting{
			ID:  http2.SettingInitialWindowSize,
			Val: defaultWindow + 1,
		}); err != nil {
			t.Fatal(err)
		}
		if frames := pr.closed(); !hasGoAway(frames, http2.ErrCodeFlowControl) {
			t.Fatalf("no GOAWAY(FLOW_CONTROL_ERROR) in %v", frames)
		}
		<-result
		waitDone(t, c)
	})
}

// The peer is held to the limits we advertise: frames no larger than our
// SETTINGS_MAX_FRAME_SIZE (16384 by default), header lists no larger than
// the profile's SETTINGS_MAX_HEADER_LIST_SIZE (Chrome's 262144).
func TestReadLimitsAreAdvertised(t *testing.T) {
	t.Run("frame size", func(t *testing.T) {
		c, pr := newPeer(t, profiles.Chrome155MacOS(), clockwork.NewRealClock())
		result := roundTripAsync(c, get(t, "/"))
		h := pr.headers()
		pr.headerFields(h.StreamID, false, xhpack.HeaderField{
			Name:  ":status",
			Value: "200",
		})
		if r := <-result; r.err != nil {
			t.Fatal(r.err)
		}
		// The client stops reading at the header, so the write fails.
		_ = pr.fr.WriteRawFrame(http2.FrameData, 0, h.StreamID, make([]byte, defaultFrameSize+1))
		if frames := pr.closed(); !hasGoAway(frames, http2.ErrCodeFrameSize) {
			t.Fatalf("no GOAWAY(FRAME_SIZE_ERROR) in %v", frames)
		}
		waitDone(t, c)
	})
	t.Run("header list size", func(t *testing.T) {
		p := profiles.Chrome155MacOS()
		limit := int(setting(p, http2.SettingMaxHeaderListSize))
		c, pr := newPeer(t, p, clockwork.NewRealClock())
		result := roundTripAsync(c, get(t, "/"))
		h := pr.headers()
		// One indexed field repeated: a small block that decodes to more
		// than the limit.
		big := xhpack.HeaderField{
			Name:  "x-big",
			Value: strings.Repeat("v", 4000),
		}
		n := limit/int(big.Size()) + 1
		fields := make([]xhpack.HeaderField, 0, 1+n)
		fields = append(fields, xhpack.HeaderField{
			Name:  ":status",
			Value: "200",
		})
		for range n {
			fields = append(fields, big)
		}
		pr.headerFields(h.StreamID, true, fields...)
		if r := <-result; !errors.Is(r.err, errHeaderListSize) {
			t.Fatalf("oversized response returned %v, want %v", r.err, errHeaderListSize)
		}
		if frames := pr.sync(); !hasReset(frames, h.StreamID, http2.ErrCodeProtocol) {
			t.Fatalf("no RST_STREAM(PROTOCOL_ERROR) on stream %d in %v", h.StreamID, frames)
		}
		if !c.CanTakeNewRequest() {
			t.Fatal("an oversized header list broke the connection")
		}
	})
}

// The client answers the peer's PING with an ACK carrying the same data.
func TestPingAnswered(t *testing.T) {
	_, pr := newPeer(t, profiles.Safari27MacOS(), clockwork.NewRealClock())
	data := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	if err := pr.fr.WritePing(false, data); err != nil {
		t.Fatal(err)
	}
	if ping, ok := pr.next().(*http2.PingFrame); !ok || !ping.IsAck() || ping.Data != data {
		t.Fatalf("got %+v, want a PING ACK carrying %x", ping, data)
	}
}

// failingConn fails every write once fail is set.
type failingConn struct {
	net.Conn
	fail atomic.Bool
}

func (c *failingConn) Write(b []byte) (int, error) {
	if c.fail.Load() {
		return 0, errors.New("write failed")
	}
	return c.Conn.Write(b)
}

// A request whose HEADERS could not be written was not processed: it fails
// with ErrConnUnusable, its body untouched, and the connection, whose HPACK
// state the peer no longer shares, closes.
func TestHeadersWriteFailure(t *testing.T) {
	var fc *failingConn
	c, _ := newPeerWith(t, peerConfig{
		profile: profiles.Chrome155MacOS(),
		clock:   clockwork.NewRealClock(),
		wrap: func(conn net.Conn) net.Conn {
			fc = &failingConn{
				Conn: conn,
			}
			return fc
		},
	})
	fc.fail.Store(true)
	body := newTrackedBody("hello")
	if _, err := c.RoundTrip(post(t, body, 5)); !errors.Is(err, ErrConnUnusable) {
		t.Fatalf("RoundTrip returned %v, want ErrConnUnusable", err)
	}
	if body.read.Load() || body.isClosed() {
		t.Fatal("the body of a request that was not sent was read or closed")
	}
	waitDone(t, c)
	if c.CanTakeNewRequest() {
		t.Fatal("connection takes requests after a failed write")
	}
}
