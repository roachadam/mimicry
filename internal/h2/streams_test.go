package h2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http2"
	xhpack "golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

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

// A request cancelled while the read loop is delivering its response
// HEADERS fails with the context's error and leaves the read loop running.
func TestCancelAsResponseHeadersArrive(t *testing.T) {
	c, pr := newPeer(t, profiles.Safari(), clockwork.NewRealClock())
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

// Response bytes nobody reads go back to the connection window; otherwise
// a few abandoned downloads stall every later response.
func TestClosedBodyReturnsConnectionCredit(t *testing.T) {
	p := profiles.Safari()
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
	c, pr := newPeer(t, profiles.Chrome(), clockwork.NewRealClock())
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

// A body whose length net/http could not measure (ContentLength 0, Body
// set) is sent in DATA frames without a content-length, not dropped.
func TestUnknownLengthBody(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome(), clockwork.NewRealClock())
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

// A peer that sends past the stream window we advertised gets that stream
// reset with FLOW_CONTROL_ERROR; the connection keeps working.
func TestStreamReceiveWindowEnforced(t *testing.T) {
	p := profiles.Safari()
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

// A stream error (a zero WINDOW_UPDATE on a stream, RFC 9113, 6.9) resets
// that stream only; the others and the connection carry on.
func TestStreamErrorResetsOnlyItsStream(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome(), clockwork.NewRealClock())
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

// After GOAWAY, a stream past LastStreamID fails with ErrConnUnusable while
// the others run to completion, and then the connection closes.
func TestGoAwayDrains(t *testing.T) {
	c, pr := newPeer(t, profiles.Chrome(), clockwork.NewRealClock())
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
