package wire

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

// rawPeer is the other end of a Conn, speaking frames by hand over a
// net.Pipe, which holds no bytes: a peer that stops reading stops the
// Conn's writer at once.
type rawPeer struct {
	t    *testing.T
	conn net.Conn
	hb   [headerSize]byte
}

func newRawPeer(t *testing.T, cfg Config) (*Conn, *rawPeer) {
	t.Helper()

	a, b := net.Pipe()

	c, err := NewConn(a, cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = c.Close()
		_ = b.Close()

		<-c.Done()
	})

	// A stall fails the test instead of hanging it.
	if err := b.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	return c, &rawPeer{
		t:    t,
		conn: b,
	}
}

func (p *rawPeer) send(stream uint32, typ frameType, flags uint8, payload []byte) {
	p.t.Helper()

	if _, err := p.conn.Write(appendFrame(nil, stream, typ, flags, payload)); err != nil {
		p.t.Fatalf("sending %s %d: %v", typ, stream, err)
	}
}

func (p *rawPeer) recv() (header, []byte) {
	p.t.Helper()

	h, payload, err := readFrame(p.conn, &p.hb)
	if err != nil {
		p.t.Fatalf("receiving: %v", err)
	}

	return h, payload
}

// expect receives a frame and checks it.
func (p *rawPeer) expect(stream uint32, typ frameType, flags uint8, payload []byte) {
	p.t.Helper()

	h, got := p.recv()
	if h.stream != stream || h.typ != typ || h.flags != flags || !bytes.Equal(got, payload) {
		p.t.Fatalf("got %s %d flags %#x %x, want %s %d flags %#x %x", h.typ, h.stream, h.flags, got, typ, stream, flags, payload)
	}
}

// open opens a stream on c while the peer receives its CALL: Open waits
// until the frame is written, and a net.Pipe write until it is read.
func (p *rawPeer) open(ctx context.Context, c *Conn, payload string) *Stream {
	p.t.Helper()

	type opened struct {
		s   *Stream
		err error
	}

	ch := make(chan opened, 1)

	go func() {
		s, err := c.Open(ctx, []byte(payload), false)
		ch <- opened{
			s:   s,
			err: err,
		}
	}()

	h, got := p.recv()
	if h.typ != typeCall || string(got) != payload {
		p.t.Fatalf("got %s %d %q, want CALL %q", h.typ, h.stream, got, payload)
	}

	o := <-ch
	if o.err != nil {
		p.t.Fatal(o.err)
	}

	return o.s
}

// within receives from ch, failing the test after 10 s.
func within[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	select {
	case v := <-ch:
		return v
	case <-timer.C:
		t.Fatalf("10 s without %s", what)
	}

	panic("unreachable")
}

func newPair(t *testing.T, cfg Config) (sdk, core *Conn) {
	t.Helper()

	a, b := net.Pipe()

	cfg.Role = RoleSDK

	sdk, err := NewConn(a, cfg)
	if err != nil {
		t.Fatal(err)
	}

	cfg.Role = RoleCore

	core, err = NewConn(b, cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = sdk.Close()
		_ = core.Close()

		<-sdk.Done()
		<-core.Done()
	})

	return sdk, core
}

// waitFor polls cond: for state another goroutine reaches with no event to
// wait on.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("10 s without %s", what)
		}

		time.Sleep(time.Millisecond)
	}
}

// waitParked waits until a goroutine is parked in a select in fn, a
// function of this package.
func waitParked(t *testing.T, fn string) {
	t.Helper()

	prefix := "github.com/roachadam/mimicry/internal/rpc/wire." + fn + "("

	waitFor(t, "a goroutine waiting in "+fn, func() bool {
		buf := make([]byte, 1<<20)

		buf = buf[:runtime.Stack(buf, true)]

		for g := range strings.SplitSeq(string(buf), "\n\n") {
			lines := strings.SplitN(g, "\n", 3)
			if len(lines) >= 2 && strings.Contains(lines[0], "[select") && strings.HasPrefix(lines[1], prefix) {
				return true
			}
		}

		return false
	})
}

// idle reports whether c holds no stream.
func idle(c *Conn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.streams) == 0 && c.peerOpen == 0 && len(c.slots) == 0
}

// One stream nobody reads, or one sending a long body, does not hold up
// the others: credit bounds the first, and the writer takes frames in
// turn, one per stream at a time. A stream that overruns its credit is
// reset alone.
func TestNoStall(t *testing.T) {
	t.Run("unread stream", func(t *testing.T) {
		c, peer := newRawPeer(t, Config{
			Role:   RoleCore,
			Window: 8,
		})

		peer.send(1, typeCall, 0, []byte("slow"))
		peer.send(1, typeData, 0, []byte("abcdefgh"))

		slow, err := c.Accept(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		peer.send(3, typeCall, flagEnd, []byte("fast"))

		fast, err := c.Accept(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		go func() {
			_ = fast.WriteReply([]byte("ok"), true)
		}()

		peer.expect(3, typeReply, flagEnd, []byte("ok"))

		// Stream 1 has spent its credit: one byte more resets it, and
		// only it.
		peer.send(1, typeData, 0, []byte("i"))
		peer.expect(1, typeReset, 0, binary.BigEndian.AppendUint32(nil, uint32(CodeCreditOverrun)))

		if _, err := slow.Read(make([]byte, 16)); !errors.Is(err, ErrCreditOverrun) {
			t.Fatalf("the overrun stream's Read: %v, want ErrCreditOverrun", err)
		}

		peer.send(1, typeReset, 0, binary.BigEndian.AppendUint32(nil, uint32(CodeCancel)))
		peer.send(5, typeCall, flagEnd, []byte("after"))

		after, err := c.Accept(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		go func() {
			_ = after.WriteReply([]byte("ok"), true)
		}()

		peer.expect(5, typeReply, flagEnd, []byte("ok"))
		waitFor(t, "every stream freed", func() bool {
			return idle(c)
		})
	})

	t.Run("a peer that never reads", func(t *testing.T) {
		const limit = 2

		c, peer := newRawPeer(t, Config{
			Role:       RoleCore,
			MaxStreams: limit,
		})

		// The peer opens and resets streams and never reads the answering
		// RESETs. Each answer holds its stream's place until the writer
		// takes it, so the peer soon opens one stream too many, instead
		// of the answers (and their streams) piling up in the queue.
		go func() {
			for id := uint32(1); id < 2001; id += 2 {
				if _, err := peer.conn.Write(appendFrame(nil, id, typeCall, 0, make([]byte, 1024))); err != nil {
					return
				}

				if _, err := peer.conn.Write(appendFrame(nil, id, typeReset, 0, binary.BigEndian.AppendUint32(nil, uint32(CodeCancel)))); err != nil {
					return
				}
			}
		}()

		waitFor(t, "the connection failing", func() bool {
			return c.Err() != nil
		})

		if !errors.Is(c.Err(), ErrStreamLimit) {
			t.Fatalf("the connection failed with %v, want ErrStreamLimit", c.Err())
		}

		c.mu.Lock()

		last := c.lastPeer
		c.mu.Unlock()

		// One answer the writer took (and is stuck writing), one queued
		// per place in the limit.
		if last > 2*limit+1 {
			t.Fatalf("streams up to %d opened before the limit failed the connection", last)
		}
	})

	t.Run("long body takes turns", func(t *testing.T) {
		const frames = 16

		c, peer := newRawPeer(t, Config{
			Role:   RoleCore,
			Window: frames * MaxPayload,
		})

		peer.send(1, typeCall, flagEnd, []byte("download"))

		long, err := c.Accept(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		go func() {
			if err := long.WriteReply([]byte("head"), false); err != nil {
				return
			}

			if _, err := long.Write(make([]byte, frames*MaxPayload)); err != nil {
				return
			}

			_ = long.CloseWrite()
		}()

		peer.expect(1, typeReply, 0, []byte("head"))

		if h, _ := peer.recv(); h.typ != typeData || h.stream != 1 {
			t.Fatalf("got %s %d, want DATA 1", h.typ, h.stream)
		}

		// The peer stops reading, with the writer in the middle of the
		// body, and calls on stream 3; the reply is queued.
		peer.send(3, typeCall, flagEnd, []byte("small"))

		small, err := c.Accept(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		go func() {
			_ = small.WriteReply([]byte("ok"), true)
		}()

		// replySent is set as the reply is queued; the writer may take it
		// at once if stream 1's next frame is not queued yet.
		waitFor(t, "the reply queued", func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()

			return small.replySent
		})

		// At most the frame being written and the one queued before the
		// reply come first.
		before := 0

		for {
			h, _ := peer.recv()
			if h.stream == 3 {
				break
			}

			before++
		}

		if before > 2 {
			t.Fatalf("%d DATA frames of stream 1 went before stream 3's REPLY, want at most 2", before)
		}
	})
}

// A RESET from either side cancels the stream on both: the side that
// reset it fails with ErrCanceled, the other with the RemoteError, the
// accepted stream's context ends, and both forget the stream.
func TestReset(t *testing.T) {
	tests := []struct {
		name string
		// reset resets the call from one side; sdkErr and coreErr are
		// what each side's stream then fails with.
		reset   func(cancel context.CancelFunc, sdk, core *Stream)
		sdkErr  error
		coreErr error
	}{
		{
			name: "the SDK's context",
			reset: func(cancel context.CancelFunc, _, _ *Stream) {
				cancel()
			},
			sdkErr:  context.Canceled,
			coreErr: ErrCanceled,
		},
		{
			name: "the SDK's Close",
			reset: func(_ context.CancelFunc, sdk, _ *Stream) {
				_ = sdk.Close()
			},
			sdkErr:  ErrCanceled,
			coreErr: ErrCanceled,
		},
		{
			name: "the core's Close",
			reset: func(_ context.CancelFunc, _, core *Stream) {
				_ = core.Close()
			},
			sdkErr:  ErrCanceled,
			coreErr: ErrCanceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdk, core := newPair(t, Config{
				Window: 8,
			})

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			s, err := sdk.Open(ctx, []byte("upload"), false)
			if err != nil {
				t.Fatal(err)
			}

			cs, err := core.Accept(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if err := cs.WriteReply([]byte("head"), false); err != nil {
				t.Fatal(err)
			}

			// Each side blocked writing past its credit, the other not
			// reading.
			sdkDone := make(chan error, 1)

			go func() {
				_, err := s.Write(make([]byte, 16))
				sdkDone <- err
			}()

			coreDone := make(chan error, 1)

			go func() {
				_, err := cs.Write(make([]byte, 16))
				coreDone <- err
			}()

			waitParked(t, "(*Stream).wait")
			tt.reset(cancel, s, cs)

			for _, side := range []struct {
				name string
				err  error
				want error
				s    *Stream
			}{
				{"SDK", within(t, "the SDK's call returning", sdkDone), tt.sdkErr, s},
				{"core", within(t, "the core's call returning", coreDone), tt.coreErr, cs},
			} {
				if !errors.Is(side.err, side.want) || errors.Is(side.err, ErrConnClosed) {
					t.Errorf("the %s's blocked call: %v, want %v and not ErrConnClosed", side.name, side.err, side.want)
				}

				if _, err := side.s.Write([]byte("x")); err == nil {
					t.Errorf("the %s's Write after the reset succeeded", side.name)
				}

				select {
				case <-side.s.Context().Done():
				default:
					t.Errorf("the %s's stream context did not end", side.name)
				}
			}

			// Exactly one side holds the peer's RemoteError.
			var re *RemoteError
			if errors.As(context.Cause(s.Context()), &re) == errors.As(context.Cause(cs.Context()), &re) {
				t.Errorf("stream causes %v and %v: want one RemoteError", context.Cause(s.Context()), context.Cause(cs.Context()))
			}

			waitFor(t, "both sides forgetting the stream", func() bool {
				return idle(sdk) && idle(core)
			})
		})
	}
}

// Losing the connection fails every stream and every waiting call with
// the named error, frees them, and leaves no goroutine behind.
func TestConnLoss(t *testing.T) {
	tests := []struct {
		name string
		cut  func(c *Conn, peer *rawPeer)
		want error
		// abort is the RESET on stream 0 the peer receives, if any.
		abort *Code
	}{
		{
			name: "the peer closes",
			cut: func(_ *Conn, peer *rawPeer) {
				_ = peer.conn.Close()
			},
			want: ErrConnClosed,
		},
		{
			name: "this side closes",
			cut: func(c *Conn, _ *rawPeer) {
				_ = c.Close()
			},
			want: ErrConnClosed,
		},
		{
			name: "a protocol error",
			cut: func(_ *Conn, peer *rawPeer) {
				_, _ = peer.conn.Write(rawHeader(9, 7, 0, 0))
			},
			want:  ErrUnknownType,
			abort: new(CodeUnknownType),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := runtime.NumGoroutine()

			func() {
				a, b := net.Pipe()
				defer func() {
					_ = b.Close()
				}()

				c, err := NewConn(a, Config{
					Role:       RoleSDK,
					Window:     8,
					MaxStreams: 2,
				})
				if err != nil {
					t.Fatal(err)
				}

				peer := &rawPeer{
					t:    t,
					conn: b,
				}

				// The peer reads everything into frames, until the end.
				frames := make(chan header, 64)

				go func() {
					defer close(frames)

					var hb [headerSize]byte

					for {
						h, p, err := readFrame(b, &hb)
						if err != nil {
							return
						}

						if h.typ == typeReset && h.stream == 0 {
							h.length = binary.BigEndian.Uint32(p)
						}

						frames <- h
					}
				}()

				s1, err := c.Open(t.Context(), []byte("a"), false)
				if err != nil {
					t.Fatal(err)
				}

				s2, err := c.Open(t.Context(), []byte("b"), false)
				if err != nil {
					t.Fatal(err)
				}

				peer.send(s2.ID(), typeReply, 0, []byte("head"))

				calls := []func() error{
					func() error {
						_, err := s1.Reply()

						return err
					},
					func() error {
						_, err := io.Copy(io.Discard, s2)

						return err
					},
					func() error {
						_, err := s2.Write(make([]byte, 16))

						return err
					},
					func() error {
						_, err := c.Open(t.Context(), []byte("c"), true)

						return err
					},
					func() error {
						_, err := c.Accept(t.Context())

						return err
					},
				}
				errs := make(chan error, len(calls))

				for _, call := range calls {
					go func() {
						errs <- call()
					}()
				}

				waitParked(t, "(*Conn).acquireSlot")
				tt.cut(c, peer)

				for range calls {
					if err := within(t, "a waiting call returning", errs); !errors.Is(err, tt.want) || !errors.Is(err, ErrConnClosed) {
						t.Errorf("a waiting call returned %v, want %v and ErrConnClosed", err, tt.want)
					}
				}

				within(t, "the connection ending", c.Done())

				if !errors.Is(c.Err(), tt.want) {
					t.Errorf("Err() = %v, want %v", c.Err(), tt.want)
				}

				c.mu.Lock()

				held := len(c.streams)
				c.mu.Unlock()

				if held != 0 {
					t.Errorf("the connection still holds %d streams", held)
				}

				_ = b.Close()

				var aborted *Code

				for h := range frames {
					if h.typ == typeReset && h.stream == 0 {
						aborted = new(Code(h.length))
					}
				}

				switch {
				case tt.abort == nil && aborted != nil:
					t.Errorf("the peer got a RESET of the connection (%s)", *aborted)
				case tt.abort != nil && (aborted == nil || *aborted != *tt.abort):
					t.Errorf("the peer got RESET %v of the connection, want %s", aborted, *tt.abort)
				}
			}()

			waitFor(t, "the goroutines ending", func() bool {
				runtime.GC()

				return runtime.NumGoroutine() <= before
			})
		})
	}
}

// Credit and the stream limit hold a sender back until the peer frees
// them, and a waiting caller's context still ends its wait.
func TestBackpressure(t *testing.T) {
	t.Run("write waits for credit", func(t *testing.T) {
		c, peer := newRawPeer(t, Config{
			Role:   RoleSDK,
			Window: 8,
		})

		s := peer.open(t.Context(), c, "put")

		const total = 24

		done := make(chan error, 1)

		go func() {
			_, err := s.Write(make([]byte, total))
			done <- err
		}()

		// Never more than the credit granted, which the peer raises by 4
		// each time it is spent.
		got, granted := 0, 8

		for got < total {
			h, p := peer.recv()
			if h.typ != typeData {
				t.Fatalf("got %s, want DATA", h.typ)
			}

			got += len(p)
			if got > granted {
				t.Fatalf("%d bytes sent with %d of credit", got, granted)
			}

			if got == granted && got < total {
				granted += 4

				peer.send(1, typeWindow, 0, binary.BigEndian.AppendUint32(nil, 4))
			}
		}

		if err := within(t, "Write returning", done); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("open waits for a free stream", func(t *testing.T) {
		sdk, core := newPair(t, Config{
			MaxStreams: 1,
		})

		if _, err := sdk.Open(t.Context(), []byte("first"), true); err != nil {
			t.Fatal(err)
		}

		second := make(chan error, 1)

		go func() {
			_, err := sdk.Open(t.Context(), []byte("second"), true)
			second <- err
		}()

		waitParked(t, "(*Conn).acquireSlot")

		first, err := core.Accept(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if err := first.WriteReply([]byte("ok"), true); err != nil {
			t.Fatal(err)
		}

		if err := within(t, "the second Open returning", second); err != nil {
			t.Fatal(err)
		}

		s, err := core.Accept(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if s.ID() != 3 || string(s.Payload()) != "second" {
			t.Fatalf("accepted stream %d %q, want 3 \"second\"", s.ID(), s.Payload())
		}
	})

	t.Run("cancel ends a wait for credit", func(t *testing.T) {
		c, peer := newRawPeer(t, Config{
			Role:   RoleSDK,
			Window: 8,
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		s := peer.open(ctx, c, "put")

		done := make(chan error, 1)

		go func() {
			_, err := s.Write(make([]byte, 16))
			done <- err
		}()

		peer.expect(1, typeData, 0, make([]byte, 8))
		waitParked(t, "(*Stream).wait")
		cancel()

		if err := within(t, "Write returning", done); !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) {
			t.Fatalf("Write: %v, want ErrCanceled and context.Canceled", err)
		}

		peer.expect(1, typeReset, 0, binary.BigEndian.AppendUint32(nil, uint32(CodeCancel)))
	})
}
