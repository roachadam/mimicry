package wire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

var (
	errWriteEnded = errors.New("wire: write after END")
	errNoReply    = errors.New("wire: DATA before the REPLY")
)

// Stream is one call: its opening frame (CALL or EVENT), one REPLY, then
// DATA both ways, each direction ended by END. One goroutine may read while
// another writes. Close every stream: one left open holds its MaxStreams
// slot until both directions end.
type Stream struct {
	c       *Conn
	id      uint32
	local   bool
	payload []byte

	// ctx ends with the stream: a reset, the connection's end, or both
	// directions ended.
	ctx    context.Context
	cancel context.CancelCauseFunc
	// stop stops the AfterFunc resetting a local stream when Open's
	// context ends.
	stop func() bool

	// rcv and snd wake a waiting reader and writer; done receives the
	// result of the writer's frame (one at a time, under wmu).
	rcv  chan struct{}
	snd  chan struct{}
	done chan error
	rmu  sync.Mutex
	wmu  sync.Mutex

	// The rest is guarded by c.mu.
	err       error
	reply     []byte
	gotReply  bool
	replySent bool
	// recv is the peer's DATA not yet read, buffered bytes of it. The
	// credit granted the peer, recvCredit, plus buffered plus unreturned
	// (read but not yet granted back) is Window until END.
	recv          [][]byte
	buffered      int
	recvCredit    int
	unreturned    int
	pendingWindow int
	windowQueued  bool
	recvEnded     bool
	sendCredit    int
	sendEnded     bool
	resetSent     bool
	// resetQueued is a RESET of this side's in the write queue; freed an
	// ended stream; released one forgotten, its MaxStreams place free.
	resetQueued bool
	freed       bool
	released    bool
}

// ID is the stream's id: odd if the SDK opened it, even if the core did.
func (s *Stream) ID() uint32 {
	return s.id
}

// Payload is the opening frame's payload of a stream the peer opened.
func (s *Stream) Payload() []byte {
	return s.payload
}

// Context ends when the stream does: reset by either side (its cause the
// stream's error), failed with the connection, or both directions ended.
func (s *Stream) Context() context.Context {
	return s.ctx
}

// Reply waits for the REPLY to a stream this side opened and returns its
// payload.
func (s *Stream) Reply() ([]byte, error) {
	if !s.local {
		return nil, errors.New("wire: Reply on a stream the peer opened")
	}

	c := s.c

	for {
		c.mu.Lock()

		if s.gotReply {
			c.mu.Unlock()

			return s.reply, nil
		}

		if err := s.err; err != nil {
			c.mu.Unlock()

			return nil, err
		}

		c.mu.Unlock()
		s.wait(s.rcv)
	}
}

// WriteReply answers a stream the peer opened. end says no DATA follows.
func (s *Stream) WriteReply(payload []byte, end bool) error {
	if s.local {
		return errors.New("wire: WriteReply on a stream this side opened")
	}

	if len(payload) > MaxPayload {
		return fmt.Errorf("%w: a %d-byte payload", ErrFrameTooLarge, len(payload))
	}

	s.wmu.Lock()
	defer s.wmu.Unlock()

	c := s.c
	c.mu.Lock()

	switch {
	case s.err != nil:
		err := s.err
		c.mu.Unlock()

		return err

	case s.replySent:
		c.mu.Unlock()

		return errors.New("wire: a second REPLY")
	}

	s.replySent = true

	var flags uint8
	if end {
		flags = flagEnd
		s.sendEnded = true
	}

	f := newFrame(s, s.id, typeReply, flags, payload)
	c.enqueueLocked(f)
	c.maybeFreeLocked(s)
	c.mu.Unlock()

	return s.await(f)
}

// Read reads the peer's DATA; io.EOF after its END. Bytes read are granted
// back to the peer as credit once they reach half the window.
func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	s.rmu.Lock()
	defer s.rmu.Unlock()

	c := s.c

	for {
		c.mu.Lock()

		switch {
		case s.err != nil:
			err := s.err
			c.mu.Unlock()

			return 0, err

		case s.buffered > 0:
			n := s.takeLocked(p)
			c.mu.Unlock()

			return n, nil

		case s.recvEnded:
			c.mu.Unlock()

			return 0, io.EOF
		}

		c.mu.Unlock()
		s.wait(s.rcv)
	}
}

// takeLocked copies buffered DATA into p and grants the bytes read back
// once they reach half the window (rounded up), unless END came.
func (s *Stream) takeLocked(p []byte) int {
	n := 0

	for n < len(p) && len(s.recv) > 0 {
		k := copy(p[n:], s.recv[0])

		n += k

		if k == len(s.recv[0]) {
			s.recv[0] = nil
			s.recv = s.recv[1:]
		} else {
			s.recv[0] = s.recv[0][k:]
		}
	}

	s.buffered -= n

	s.unreturned += n

	if s.recvEnded || s.unreturned < (s.c.cfg.Window+1)/2 {
		return n
	}

	s.recvCredit += s.unreturned
	s.pendingWindow += s.unreturned

	s.unreturned = 0

	if !s.windowQueued {
		s.windowQueued = true
		s.c.enqueueLocked(&outFrame{
			s:   s,
			typ: typeWindow,
		})
	}

	return n
}

// Write sends p as DATA, in frames of at most MaxPayload, each waiting for
// credit and for its turn at the writer.
func (s *Stream) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	c := s.c
	n := 0

	for len(p) > 0 {
		c.mu.Lock()

		if err := s.sendErrLocked(); err != nil {
			c.mu.Unlock()

			return n, err
		}

		if s.sendCredit == 0 {
			c.mu.Unlock()
			s.wait(s.snd)

			continue
		}

		k := min(len(p), s.sendCredit, MaxPayload)

		s.sendCredit -= k

		f := newFrame(s, s.id, typeData, 0, p[:k])
		c.enqueueLocked(f)
		c.mu.Unlock()

		if err := s.await(f); err != nil {
			return n, err
		}

		n += k

		p = p[k:]
	}

	return n, nil
}

// CloseWrite ends this side's DATA (END).
func (s *Stream) CloseWrite() error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	c := s.c
	c.mu.Lock()

	if err := s.sendErrLocked(); err != nil {
		c.mu.Unlock()

		return err
	}

	f := newFrame(s, s.id, typeData, flagEnd, nil)

	s.sendEnded = true

	c.enqueueLocked(f)
	c.maybeFreeLocked(s)
	c.mu.Unlock()

	return s.await(f)
}

// Close ends the stream. If either direction is still open it resets the
// stream (CANCEL) so the peer stops; a stream both sides ended needs
// nothing. It is Reset(CodeCancel).
func (s *Stream) Close() error {
	return s.Reset(CodeCancel)
}

// Reset resets the stream with code if either direction is still open:
// the peer's side fails with a *RemoteError of that code, this side's with
// ErrCanceled.
func (s *Stream) Reset(code Code) error {
	c := s.c
	c.mu.Lock()
	c.resetLocked(s, code, fmt.Errorf("%w: stream %d reset with %s", ErrCanceled, s.id, code))
	c.mu.Unlock()

	return nil
}

func (s *Stream) sendErrLocked() error {
	switch {
	case s.err != nil:
		return s.err
	case s.sendEnded:
		return errWriteEnded
	case !s.local && !s.replySent:
		return errNoReply
	}

	return nil
}

// wait waits for ch or the stream's end. When Open's context ended and its
// AfterFunc has yet to run, it resets the stream itself, so the caller's
// next look at the stream sees why.
func (s *Stream) wait(ch chan struct{}) {
	select {
	case <-ch:
	case <-s.ctx.Done():
		s.c.mu.Lock()
		s.c.resetLocked(s, CodeCancel, canceled(s.ctx))
		s.c.mu.Unlock()
	}
}

// await waits until f is written or dropped, or the stream ends first; f
// is the caller's copy, so returning early is safe. A stream that ended
// normally still writes f, so its result is awaited.
func (s *Stream) await(f *outFrame) error {
	select {
	case err := <-f.done:
		return err
	case <-s.ctx.Done():
	}

	c := s.c
	c.mu.Lock()
	c.resetLocked(s, CodeCancel, canceled(s.ctx))

	err := s.err
	c.mu.Unlock()

	if err != nil {
		return err
	}

	select {
	case err := <-f.done:
		return err
	case <-c.failed:
		return c.Err()
	}
}

// endLocked frees what waits on the stream once it ended or failed.
func (s *Stream) endLocked() {
	if s.stop != nil {
		s.stop()
	}

	s.cancel(s.err)
	signal(s.rcv)
	signal(s.snd)
}
