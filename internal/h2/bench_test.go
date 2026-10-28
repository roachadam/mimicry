package h2

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// scriptConn is a server that sends its SETTINGS, answers the first
// HEADERS with an empty 200 and counts the client's writes, each a TLS
// record on a real connection.
type scriptConn struct {
	net.Conn

	mu      sync.Mutex
	cond    *sync.Cond
	in      bytes.Buffer
	answer  []byte
	writes  int
	closed  bool
	settled bool
}

func newScriptConn(firstID uint32) *scriptConn {
	c := &scriptConn{
		answer: []byte{
			0, 0, 1, // length
			1,   // HEADERS
			0x5, // END_STREAM | END_HEADERS
			0, 0, 0, byte(firstID),
			0x88, // :status 200
		},
	}

	c.cond = sync.NewCond(&c.mu)
	// The server's SETTINGS, empty.
	c.in.Write([]byte{0, 0, 0, 4, 0, 0, 0, 0, 0})

	return c
}

func (c *scriptConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for c.in.Len() == 0 && !c.closed {
		c.cond.Wait()
	}

	if c.closed {
		return 0, io.EOF
	}

	return c.in.Read(p)
}

func (c *scriptConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.writes++

	// Answer once a HEADERS frame went out.
	for b := bytes.TrimPrefix(p, http2Preface); !c.settled && len(b) >= 9; {
		if b[3] == 1 {
			c.settled = true
			c.in.Write(c.answer)
			c.cond.Broadcast()
		}

		b = b[min(len(b), 9+(int(b[0])<<16|int(b[1])<<8|int(b[2]))):]
	}

	return len(p), nil
}

var http2Preface = []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")

func (c *scriptConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	c.cond.Broadcast()

	return nil
}

func (c *scriptConn) SetDeadline(time.Time) error {
	return nil
}

func (c *scriptConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *scriptConn) SetWriteDeadline(time.Time) error {
	return nil
}

// A new connection's cost up to its first response: New and one GET, the
// client's writes (TLS records) and allocations, per profile.
func BenchmarkOpen(b *testing.B) {
	for _, p := range []*profile.Profile{
		profiles.Chrome156MacOS(),
		profiles.Firefox158MacOS(),
		profiles.Safari26MacOS(),
	} {
		b.Run(p.Name, func(b *testing.B) {
			firstID := p.HTTP2.FirstStreamID
			if firstID == 0 {
				firstID = 1
			}

			clock := clockwork.NewRealClock()
			writes := 0

			b.ReportAllocs()

			for b.Loop() {
				conn := newScriptConn(firstID)

				c, err := New(conn, p, clock)
				if err != nil {
					b.Fatal(err)
				}

				req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, "https://example.com/", nil)
				if err != nil {
					b.Fatal(err)
				}

				resp, err := c.RoundTrip(req)
				if err != nil {
					b.Fatal(err)
				}

				_ = resp.Body.Close()
				_ = c.Close()
				<-c.Done()

				conn.mu.Lock()

				writes += conn.writes

				conn.mu.Unlock()
			}

			b.ReportMetric(float64(writes)/float64(b.N), "writes/op")
		})
	}
}
