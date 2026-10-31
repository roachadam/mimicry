package h2

import (
	"errors"
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

// A server's GOAWAY gets the profile's reply (http2.goawayReply) before the
// connection closes, idle, once its last stream ends, or after a
// REFUSED_STREAM on another stream stopped new ones; none without.
func TestGoAwayReply(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reply    string
		inFlight bool
		refused  bool
	}{
		{
			name:  "idle",
			reply: "NO_ERROR",
		},
		{
			name:     "stream in flight",
			reply:    "NO_ERROR",
			inFlight: true,
		},
		{
			name:     "after REFUSED_STREAM",
			reply:    "NO_ERROR",
			inFlight: true,
			refused:  true,
		},
		{
			name: "no reply",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := *profiles.Chrome()
			h2 := *p.HTTP2

			h2.GoAwayReply = tc.reply
			p.HTTP2 = &h2

			c, pr := newPeer(t, &p, clockwork.NewRealClock())

			var (
				result <-chan responseOrError
				last   uint32
			)

			if tc.inFlight {
				result = roundTripAsync(c, get(t, "/"))
				last = pr.headers().StreamID
			}

			var frames []http2.Frame
			if tc.refused {
				result := roundTripAsync(c, get(t, "/"))
				id := pr.headers().StreamID

				if err := pr.fr.WriteRSTStream(id, http2.ErrCodeRefusedStream); err != nil {
					t.Fatal(err)
				}

				if r := <-result; !errors.Is(r.err, ErrConnUnusable) {
					t.Fatalf("refused stream returned %v, want ErrConnUnusable", r.err)
				}

				frames = pr.sync()
			}

			if err := pr.fr.WriteGoAway(last, http2.ErrCodeNo, nil); err != nil {
				t.Fatal(err)
			}

			if tc.inFlight {
				frames = pr.sync()
				pr.respond(last, 0, nil, true)

				if r := <-result; r.err != nil {
					t.Fatal(r.err)
				}
			}

			frames = append(frames, pr.closed()...)

			got, ok := find(frames, func(*http2.GoAwayFrame) bool { return true })

			switch {
			case tc.reply == "" && ok:
				t.Fatalf("sent GOAWAY(%v) with no reply in the profile", got.ErrCode)
			case tc.reply != "" && !ok:
				t.Fatalf("no GOAWAY before the connection closed: %d frames", len(frames))
			case ok && (got.ErrCode != http2.ErrCodeNo || got.LastStreamID != 0):
				t.Fatalf("replied GOAWAY(%v, last stream %d), want NO_ERROR, 0", got.ErrCode, got.LastStreamID)
			}
		})
	}
}
