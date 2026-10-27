package h2

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http2"

	"github.com/roachadam/mimicry/profiles"
)

// goAwayGate holds the client's GOAWAY frame back until release is closed,
// closing writing as it starts.
type goAwayGate struct {
	net.Conn
	writing, release chan struct{}
}

func (g *goAwayGate) Write(b []byte) (int, error) {
	// A frame header's type is its fourth byte; the framer writes each
	// frame in one call.
	if len(b) >= 9 && http2.FrameType(b[3]) == http2.FrameGoAway {
		close(g.writing)
		<-g.release
	}

	return g.Conn.Write(b)
}

// A GOAWAY sent as the last stream in flight ends still goes out: the
// stream's end must not close the connection under the frame.
func TestGoAwayAsTheLastStreamEnds(t *testing.T) {
	gate := &goAwayGate{
		writing: make(chan struct{}),
		release: make(chan struct{}),
	}
	c, pr := newPeerWith(t, peerConfig{
		profile: profiles.Firefox(),
		clock:   clockwork.NewRealClock(),
		wrap: func(conn net.Conn) net.Conn {
			gate.Conn = conn
			return gate
		},
	})
	result := roundTripAsync(c, get(t, "/"))
	h := pr.headers()
	pr.respond(h.StreamID, 0, nil, false)
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	go c.GoAway()
	<-gate.writing
	// The stream ends while the GOAWAY is being written.
	if err := pr.fr.WriteData(h.StreamID, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r.resp.Body); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(200 * time.Millisecond):
	}
	close(gate.release)
	frames := pr.closed()
	if _, ok := find(frames, func(*http2.GoAwayFrame) bool { return true }); !ok {
		t.Fatalf("no GOAWAY before the connection closed: %d frames", len(frames))
	}
}
