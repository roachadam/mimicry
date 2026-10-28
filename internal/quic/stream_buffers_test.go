package quic

// mimicry: stream buffers go back to the pool once a stream is done with
// them (pipe.release, UPSTREAM.md change 9) and never while it still uses
// them. Race builds poison every buffer put back (pipe_poison_race.go), so a
// stream that read or sent from one after giving it up would show garbage.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"iter"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// How a stream in TestStreamBuffersReturned ends.
const (
	endEcho               = iota // the server echoes the request; both read to EOF
	endPingPong                  // the server echoes each chunk before the client sends the next
	endClientStopsReading        // the client reads half the echo, then CloseRead
	endServerResets              // the server sends half the echo, then RESET_STREAM
	endClientResets              // the client sends half its request, then RESET_STREAM
	endCount

	abortMark = 0xff // first byte of a stream the connection is aborted under
)

// Many streams at once over a link that loses packets, ending each of the
// ways above, with sizes on both sides of a buffer's 4 KB: every byte either
// side reads is what the other wrote. Then the connection is aborted under
// streams still writing, and under readers with data buffered, which still
// read it.
func TestStreamBuffersReturned(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	srvEP := newLossyEndpoint(t, serverSide, 1)
	cliEP := newLossyEndpoint(t, clientSide, 2)

	cli, err := cliEP.Dial(ctx, "udp", srvEP.LocalAddr().String(), makeTestConfig(&Config{}, clientSide))
	if err != nil {
		t.Fatal(err)
	}

	srv, err := srvEP.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var (
		served   sync.WaitGroup
		buffered sync.WaitGroup // abort streams the server has started reading
		aborted  = make(chan struct{})
	)

	served.Go(func() {
		for {
			s, err := srv.AcceptStream(ctx)
			if err != nil {
				return
			}

			served.Go(func() {
				if err := serveBufferStream(ctx, s, &buffered, aborted); err != nil {
					t.Errorf("server, stream %d: %v", s.ID(), err)
				}
			})
		}
	})

	sizes := []int{0, 1, 100, 1200, 4091, 4096, 4101, 8192, 12000, 70000}

	const (
		workers = 32
		streams = 600
	)

	var next atomic.Int64

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= streams || ctx.Err() != nil {
					return
				}

				size := sizes[i%len(sizes)]
				ending := (i / len(sizes)) % endCount

				if err := requestBufferStream(ctx, cli, ending, size); err != nil {
					t.Errorf("client, request %d (ending %d, %d bytes): %v", i, ending, size, err)
				}
			}
		})
	}

	wg.Wait()

	if t.Failed() {
		return
	}

	// The abort: the client writes until its writes fail; the server reads
	// the header and waits for its connection to end, then reads what was
	// buffered already.
	const aborting = 16

	buffered.Add(aborting)

	var writers sync.WaitGroup
	for range aborting {
		s, err := cli.NewStream(ctx)
		if err != nil {
			t.Fatal(err)
		}

		writers.Go(func() {
			data := abortPattern(s.ID())
			for off := 0; ; off = (off + 700) % len(data) {
				if _, err := s.Write(data[off : off+700]); err != nil {
					return
				}

				if off == 0 {
					_ = s.Flush()
				}
			}
		})
	}

	buffered.Wait()
	srv.Abort(nil)
	close(aborted)
	cli.Abort(nil)
	writers.Wait()
	served.Wait()
}

// requestBufferStream opens a stream, sends a 5-byte header (ending, size)
// and size bytes of the stream's pattern, and checks what comes back.
func requestBufferStream(ctx context.Context, cli *Conn, ending, size int) error {
	s, err := cli.NewStream(ctx)
	if err != nil {
		return err
	}

	s.SetReadContext(ctx)
	s.SetWriteContext(ctx)

	seed := int(s.ID())
	data := bufferPattern(s.ID(), size)

	var hdr [5]byte

	hdr[0] = byte(ending)
	binary.BigEndian.PutUint32(hdr[1:], uint32(size))

	if _, err := s.Write(hdr[:]); err != nil {
		return err
	}

	switch ending {
	case endClientResets:
		if err := writeChunked(s, data[:size/2], seed); err != nil {
			return err
		}

		if err := s.Flush(); err != nil {
			return err
		}

		if err := s.Reset(2); err != nil {
			return err
		}

		_ = s.Close()

		return nil

	case endPingPong:
		// After each echo the server has acknowledged everything sent, and
		// a chunk larger than a buffer left the free space after it for the
		// next one's fast path: that buffer must still be the stream's.
		echo := make([]byte, pingPongBig)

		for off, n := range pingPongChunks(s.ID(), size) {
			b := data[off : off+n]
			if err := writePingPong(s, b); err != nil {
				return err
			}

			if _, err := io.ReadFull(s, echo[:n]); err != nil {
				return fmt.Errorf("echo at %d: %w", off, err)
			}

			if !bytes.Equal(echo[:n], b) {
				return fmt.Errorf("echo at %d differs", off)
			}
		}

		if err := s.CloseWrite(); err != nil {
			return err
		}

		if n, err := s.Read(echo[:1]); n != 0 || !errors.Is(err, io.EOF) {
			return fmt.Errorf("after the echoes: %d, %w, want EOF", n, err)
		}

		return s.Close()
	}

	if err := writeChunked(s, data, seed); err != nil {
		return err
	}

	if err := s.CloseWrite(); err != nil {
		return err
	}

	switch ending {
	case endEcho:
		got, err := readChunked(s, seed)
		if err != nil {
			return err
		}

		if !bytes.Equal(got, data) {
			return fmt.Errorf("echo differs (%d bytes, want %d)", len(got), len(data))
		}

		return s.Close()

	case endClientStopsReading:
		got := make([]byte, size/2)
		if _, err := io.ReadFull(s, got); err != nil {
			return err
		}

		if !bytes.Equal(got, data[:size/2]) {
			return errors.New("first half of the echo differs")
		}

		_ = s.CloseRead()
		_ = s.Close()

		return nil

	default: // endServerResets
		got, err := readChunked(s, seed)

		var code StreamError
		if !errors.As(err, &code) || code != 1 {
			return fmt.Errorf("read: %w, want the server's reset (code 1)", err)
		}

		if len(got) > size/2 || !bytes.Equal(got, data[:len(got)]) {
			return fmt.Errorf("%d bytes before the reset differ (want at most %d of the echo)", len(got), size/2)
		}

		_ = s.Close()

		return nil
	}
}

// serveBufferStream answers a stream from requestBufferStream, or reads an
// abort stream's header, marks buffered and waits for aborted.
func serveBufferStream(ctx context.Context, s *Stream, buffered *sync.WaitGroup, aborted <-chan struct{}) error {
	s.SetReadContext(ctx)
	s.SetWriteContext(ctx)

	defer func() {
		_ = s.Close()
	}()

	seed := int(s.ID())

	var hdr [5]byte
	if _, err := io.ReadFull(s, hdr[:]); err != nil {
		// A reset may discard the header before it is sent.
		var code StreamError
		if errors.As(err, &code) && code == 2 {
			return nil
		}

		return fmt.Errorf("header: %w", err)
	}

	if hdr[0] == abortMark {
		return readAfterAbort(s, buffered, aborted)
	}

	ending := int(hdr[0])
	size := int(binary.BigEndian.Uint32(hdr[1:]))
	data := bufferPattern(s.ID(), size)

	if ending == endPingPong {
		b := make([]byte, pingPongBig)

		for off, n := range pingPongChunks(s.ID(), size) {
			if _, err := io.ReadFull(s, b[:n]); err != nil {
				return fmt.Errorf("chunk at %d: %w", off, err)
			}

			if !bytes.Equal(b[:n], data[off:off+n]) {
				return fmt.Errorf("chunk at %d differs", off)
			}

			if err := writePingPong(s, b[:n]); err != nil {
				return err
			}
		}

		if n, err := s.Read(b[:1]); n != 0 || !errors.Is(err, io.EOF) {
			return fmt.Errorf("after the chunks: %d, %w, want EOF", n, err)
		}

		return nil
	}

	got, err := readChunked(s, seed)
	if ending == endClientResets {
		var code StreamError
		if !errors.As(err, &code) || code != 2 {
			return fmt.Errorf("read: %w, want the client's reset (code 2)", err)
		}

		if len(got) > size/2 || !bytes.Equal(got, data[:len(got)]) {
			return fmt.Errorf("%d bytes before the reset differ", len(got))
		}

		return nil
	}

	if err != nil {
		return err
	}

	if !bytes.Equal(got, data) {
		return fmt.Errorf("request differs (%d bytes, want %d)", len(got), len(data))
	}

	switch ending {
	case endServerResets:
		if err := writeChunked(s, got[:size/2], seed); err != nil {
			return err
		}

		if err := s.Flush(); err != nil {
			return err
		}

		return s.Reset(1)

	case endClientStopsReading:
		// The client's STOP_SENDING may end the writes.
		_ = writeChunked(s, got, seed)

		return nil

	default:
		return writeChunked(s, got, seed)
	}
}

// readAfterAbort waits for the connection's abort, then reads what the
// stream had buffered: the pattern, until an error.
func readAfterAbort(s *Stream, buffered *sync.WaitGroup, aborted <-chan struct{}) error {
	buffered.Done()
	<-aborted

	got, err := readChunked(s, int(s.ID()))
	if err == nil {
		return errors.New("read to EOF after the abort")
	}

	data := abortPattern(s.ID())[5:]
	for len(got) > 0 {
		n := min(len(got), len(data))
		if !bytes.Equal(got[:n], data[:n]) {
			return fmt.Errorf("%d bytes read after the abort differ", len(got))
		}

		got = got[n:]
		data = abortPattern(s.ID())
	}

	return nil
}

// bufferPattern is a stream's n bytes: they differ between streams, and from
// the poison.
func bufferPattern(id int64, n int) []byte {
	b := make([]byte, n)
	for j := range b {
		b[j] = byte(uint64(id)*131 + uint64(j)*7 + uint64(j>>8))
	}

	return b
}

// abortPattern is what an abort stream writes, over and over: 700 KB
// beginning with abortMark.
func abortPattern(id int64) []byte {
	b := bufferPattern(id, 700<<10)

	b[0] = abortMark

	return b
}

// pingPongBig is the size of an endPingPong stream's large chunks: more than
// a buffer, so a Write of one takes the slow path.
const pingPongBig = 5000

// pingPongChunks yields the offsets and sizes of an endPingPong stream's
// chunks, large and small (300 to 1500 bytes) in turn.
func pingPongChunks(id int64, size int) iter.Seq2[int, int] {
	small := 300 + int(id)*41%1200

	return func(yield func(int, int) bool) {
		big := true
		for off := 0; off < size; {
			n := small
			if big {
				n = pingPongBig
			}

			n = min(n, size-off)
			if !yield(off, n) {
				return
			}

			off += n

			big = !big
		}
	}
}

// writePingPong writes an endPingPong chunk and flushes it, unless it is
// larger than a buffer: that Write takes the slow path, which flushes it
// (Write's autoFlushSize) and leaves the free space after it for the next
// Write's fast path.
func writePingPong(s *Stream, b []byte) error {
	if _, err := s.Write(b); err != nil {
		return err
	}

	if len(b) > 4096 {
		return nil
	}

	return s.Flush()
}

// writeChunked writes b in chunks whose size depends on seed, so writes
// take the fast path into the free space of a buffer and the slow one.
func writeChunked(s *Stream, b []byte, seed int) error {
	chunk := 1 + seed*37%3000
	for len(b) > 0 {
		n := min(chunk, len(b))
		if _, err := s.Write(b[:n]); err != nil {
			return err
		}

		b = b[n:]
	}

	return nil
}

// readChunked reads s to EOF in reads whose size depends on seed, so reads
// take the fast path from the buffer and the slow one. It returns what it
// read with the error that ended it (nil at EOF).
func readChunked(s *Stream, seed int) ([]byte, error) {
	buf := make([]byte, 1+seed*53%2000)

	var got []byte
	for {
		n, err := s.Read(buf)

		got = append(got, buf[:n]...)

		if errors.Is(err, io.EOF) {
			return got, nil
		}

		if err != nil {
			return got, err
		}
	}
}

// newLossyEndpoint is an endpoint on a local UDP socket that drops one
// datagram in 30 it sends, by a PRNG seeded with seed.
func newLossyEndpoint(t *testing.T, side connSide, seed uint64) *Endpoint {
	t.Helper()

	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	e, err := NewEndpoint(&lossyPacketConn{
		PacketConn: pc,
		rng:        rand.New(rand.NewPCG(seed, seed)), //nolint:gosec // seeded on purpose: the same losses every run
	}, makeTestConfig(&Config{}, side))
	if err != nil {
		_ = pc.Close()

		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = e.Close(canceledContext())
	})

	return e
}

// lossyPacketConn drops one datagram in 30 it writes.
type lossyPacketConn struct {
	net.PacketConn

	mu  sync.Mutex
	rng *rand.Rand
}

func (c *lossyPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.mu.Lock()

	drop := c.rng.IntN(30) == 0
	c.mu.Unlock()

	if drop {
		return len(b), nil
	}

	return c.PacketConn.WriteTo(b, addr)
}
