package h2

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http2"
	xhpack "golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// A CONNECT tunnel's deadlines behave as a socket's, against a raw peer and
// a fake clock, so that nothing above it stalls (no capture reaches them):
// a read deadline already passed fails at once with a timeout, yet what is
// buffered is read first; one that passes interrupts a blocked read, and a
// cleared one lets it wait for data; a write deadline interrupts a write
// blocked on flow control (HTTP/1.1's Usable, a WebSocket's send). Close
// resets the stream with the tunnel's code.
func TestTunnelDeadlines(t *testing.T) {
	clock := clockwork.NewFakeClock()
	c, pr := newPeer(t, profiles.ChromeMacOS().Spec(), clock)

	type result struct {
		conn net.Conn
		err  error
	}

	opened := make(chan result, 1)

	go func() {
		resp, tc, err := c.Tunnel(t.Context(), "site.test:443", []profile.Header{
			{
				Name:  "user-agent",
				Value: "test",
			},
		}, 146, "CANCEL")
		if err == nil && tc == nil {
			err = errors.New("no tunnel: " + resp.Status)
		}

		opened <- result{
			conn: tc,
			err:  err,
		}
	}()

	h := pr.headers()
	if h.StreamEnded() {
		t.Error("the CONNECT ended its stream")
	}

	pr.headerFields(h.StreamID, false, xhpack.HeaderField{
		Name:  ":status",
		Value: "200",
	})

	r := <-opened
	if r.err != nil {
		t.Fatal(r.err)
	}

	tc := r.conn
	buf := make([]byte, 16)

	timedOut := func(err error) bool {
		var ne net.Error

		return errors.Is(err, os.ErrDeadlineExceeded) && errors.As(err, &ne) && ne.Timeout()
	}

	// Passed: at once.
	_ = tc.SetReadDeadline(clock.Now().Add(-time.Second))
	if _, err := tc.Read(buf); !timedOut(err) {
		t.Fatalf("read past its deadline: %v, want a timeout", err)
	}

	// Buffered data first.
	if err := pr.fr.WriteData(h.StreamID, false, []byte("hi")); err != nil {
		t.Fatal(err)
	}

	pr.sync()

	if n, err := tc.Read(buf); err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("read %q, %v past its deadline with data buffered, want %q", buf[:n], err, "hi")
	}

	// Passing while blocked, then cleared.
	_ = tc.SetReadDeadline(clock.Now().Add(time.Second))

	reads := make(chan error, 1)

	go func() {
		_, err := tc.Read(buf)
		reads <- err
	}()

	if err := clock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}

	clock.Advance(time.Second)

	if err := <-reads; !timedOut(err) {
		t.Fatalf("blocked read as its deadline passed: %v, want a timeout", err)
	}

	_ = tc.SetReadDeadline(time.Time{})

	go func() {
		_, err := tc.Read(buf)
		reads <- err
	}()

	if err := pr.fr.WriteData(h.StreamID, false, []byte("x")); err != nil {
		t.Fatal(err)
	}

	if err := <-reads; err != nil {
		t.Fatalf("read with the deadline cleared: %v", err)
	}

	// A write past the stream's window blocks until its deadline passes.
	writes := make(chan error, 1)

	go func() {
		_, err := tc.Write(make([]byte, 1<<20))
		writes <- err
	}()

	// Its first DATA went out within the window; nothing is credited.
	for {
		if _, ok := pr.next().(*dataCopy); ok {
			break
		}
	}

	_ = tc.SetWriteDeadline(clock.Now())

	select {
	case err := <-writes:
		if !timedOut(err) {
			t.Fatalf("blocked write as its deadline passed: %v, want a timeout", err)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("the blocked write outlived its deadline")
	}

	if err := tc.Close(); err != nil {
		t.Fatal(err)
	}

	if !hasReset(pr.sync(), h.StreamID, http2.ErrCodeCancel) {
		t.Error("Close sent no RST_STREAM CANCEL")
	}
}
