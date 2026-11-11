package wire

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

// Role is the side of the connection a Conn speaks for. It fixes the ids
// the Conn opens streams with and the frame that opens them.
type Role uint8

const (
	// RoleSDK is the host language's side: it opens odd streams with
	// CALL, and accepts the core's EVENT streams and notices.
	RoleSDK Role = 1 + iota
	// RoleCore is mimicry-core: it opens even streams with EVENT (reverse
	// calls such as VerifyConnection), sends notices, and accepts CALL
	// streams.
	RoleCore
)

// The defaults both ends use unless they agreed on other values.
const (
	DefaultWindow     = 256 << 10
	DefaultMaxStreams = 256
)

// maxCredit is the most credit a direction of a stream may hold.
const maxCredit = 1<<31 - 1

// AbortGrace is how long the writer has, once the connection failed for
// any reason but Close, to write what it must (the frame it is writing,
// then a RESET on stream 0 after a protocol error) before rwc is closed
// under it: a peer that stopped reading gets no more, and Done closes.
const AbortGrace = time.Second

// poolMin is the payload from which a frame's buffer comes from bufPool.
const poolMin = 4 << 10

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, headerSize+MaxPayload)

		return &b
	},
}

// Config is one end of a connection.
type Config struct {
	Role Role
	// Window is the credit each direction of a stream starts with, and so
	// the most a stream buffers of the peer's DATA. Both ends must use the
	// same value; zero means DefaultWindow.
	Window int
	// MaxStreams is how many streams each side may have open at once:
	// Open waits for one of this side's to end, and a peer that opens more
	// fails the connection (ErrStreamLimit). Both ends must use the same
	// value; zero means DefaultMaxStreams.
	MaxStreams int
	// OnEvent receives the core's notices (EVENT on stream 0) on RoleSDK.
	// It runs on the goroutine reading the connection, which reads no
	// frame until it returns: hand the payload off and return, and make
	// no Conn or Stream call that can wait, since what it waits for may
	// be the next frame. Nil drops them.
	OnEvent func(payload []byte)
	// Clock times AbortGrace; nil means the real clock.
	Clock clockwork.Clock
}

// Conn is one end of a connection: streams multiplexed over an
// io.ReadWriteCloser, a goroutine reading it and one writing it. The
// writer takes frames first come, first served, and each stream has at
// most one of its caller's frames queued, so a frame waits for at most
// the one being written and one queued frame per other active stream: a
// long body takes turns with every other stream frame by frame.
type Conn struct {
	rwc io.ReadWriteCloser
	cfg Config

	// slots holds a token per stream this side has open.
	slots chan struct{}
	// wake tells the writer that wq has frames or the Conn failed.
	wake chan struct{}
	// accepted tells Accept that acceptQ has a stream.
	accepted chan struct{}
	// failed is closed when the Conn fails (err); writerDone when the
	// writer returned; done when both goroutines did.
	failed     chan struct{}
	writerDone chan struct{}
	done       chan struct{}

	closeOnce sync.Once
	closeErr  error
	// winBuf is the writer's, for WINDOW frames.
	winBuf [headerSize + 4]byte

	// mu guards the rest and every Stream's state.
	mu      sync.Mutex
	err     error
	streams map[uint32]*Stream
	// nextID is the next id this side opens; lastPeer the highest the
	// peer opened; peerOpen how many of the peer's are in streams.
	nextID   uint64
	lastPeer uint32
	peerOpen int
	acceptQ  []*Stream
	wq       []*outFrame
	// abort is the RESET on stream 0 the writer sends before closing,
	// after a protocol error.
	abort []byte
	// grace closes rwc AbortGrace after a failure, unless the writer
	// closed it first.
	grace clockwork.Timer
}

// outFrame is a frame waiting for the writer.
type outFrame struct {
	s   *Stream
	typ frameType
	// buf is the frame; nil for WINDOW, built from its stream's pending
	// credit when written. pooled is buf's from bufPool.
	buf    []byte
	pooled *[]byte
	// open marks a stream's opening frame, written even if the stream
	// was reset meanwhile (the RESET follows it).
	open bool
	// done, if set, receives the write's result.
	done chan error
}

// NewConn runs a connection over rwc. Close it when done; it closes rwc
// and fails every stream.
func NewConn(rwc io.ReadWriteCloser, cfg Config) (*Conn, error) {
	if cfg.Role != RoleSDK && cfg.Role != RoleCore {
		return nil, fmt.Errorf("wire: role %d is neither RoleSDK nor RoleCore", cfg.Role)
	}

	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
	}

	if cfg.Window < 1 || cfg.Window > maxCredit {
		return nil, fmt.Errorf("wire: window %d out of range", cfg.Window)
	}

	if cfg.MaxStreams == 0 {
		cfg.MaxStreams = DefaultMaxStreams
	}

	if cfg.MaxStreams < 1 {
		return nil, fmt.Errorf("wire: MaxStreams %d out of range", cfg.MaxStreams)
	}

	if cfg.Clock == nil {
		cfg.Clock = clockwork.NewRealClock()
	}

	c := &Conn{
		rwc:        rwc,
		cfg:        cfg,
		slots:      make(chan struct{}, cfg.MaxStreams),
		wake:       make(chan struct{}, 1),
		accepted:   make(chan struct{}, 1),
		failed:     make(chan struct{}),
		writerDone: make(chan struct{}),
		done:       make(chan struct{}),
		streams:    make(map[uint32]*Stream),
		nextID:     1,
	}
	if cfg.Role == RoleCore {
		c.nextID = 2
	}

	go c.writeLoop()
	go c.readLoop()

	return c, nil
}

// Open opens a stream with payload: a CALL on RoleSDK, an EVENT (a reverse
// call) on RoleCore. end says nothing follows in this direction. It waits
// for a free stream (MaxStreams) and for the frame to be written. The
// stream lives under ctx: if ctx ends first, the stream is reset (CANCEL).
func (c *Conn) Open(ctx context.Context, payload []byte, end bool) (*Stream, error) {
	if len(payload) > MaxPayload {
		return nil, fmt.Errorf("%w: a %d-byte payload", ErrFrameTooLarge, len(payload))
	}

	if err := c.acquireSlot(ctx); err != nil {
		return nil, err
	}

	c.mu.Lock()

	if c.err != nil || c.nextID > math.MaxUint32 {
		err := c.err
		if err == nil {
			err = errors.New("wire: stream ids exhausted")
		}

		<-c.slots
		c.mu.Unlock()

		return nil, err
	}

	id := uint32(c.nextID)

	c.nextID += 2

	s := c.newStream(ctx, id, true)

	c.streams[id] = s

	var flags uint8
	if end {
		flags = flagEnd
		s.sendEnded = true
	}

	typ := typeCall
	if c.cfg.Role == RoleCore {
		typ = typeEvent
	}

	f := newFrame(s, id, typ, flags, payload)

	f.open = true
	c.enqueueLocked(f)
	c.mu.Unlock()

	if err := s.await(f); err != nil {
		return nil, err
	}

	return s, nil
}

// acquireSlot takes a token from slots, waiting while this side has
// MaxStreams streams open.
func (c *Conn) acquireSlot(ctx context.Context) error {
	if ctx.Err() != nil {
		return canceled(ctx)
	}

	select {
	case c.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return canceled(ctx)
	case <-c.failed:
		return c.Err()
	}
}

// Accept returns the next stream the peer opened: CALLs on RoleCore,
// EVENT streams (reverse calls) on RoleSDK. Payload is its opening frame's;
// answer it with WriteReply. A stream the peer reset before it was
// accepted is never returned.
func (c *Conn) Accept(ctx context.Context) (*Stream, error) {
	for {
		c.mu.Lock()

		if len(c.acceptQ) > 0 {
			s := c.acceptQ[0]

			c.acceptQ[0] = nil
			c.acceptQ = c.acceptQ[1:]

			if len(c.acceptQ) > 0 {
				signal(c.accepted)
			}

			c.mu.Unlock()

			return s, nil
		}

		err := c.err
		c.mu.Unlock()

		if err != nil {
			return nil, err
		}

		select {
		case <-c.accepted:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-c.failed:
		}
	}
}

// Notify sends a notice, an EVENT on stream 0 that nothing answers, and
// waits until it is written. Only the core sends notices. If ctx ends
// first, Notify returns, and the notice may still be sent.
func (c *Conn) Notify(ctx context.Context, payload []byte) error {
	if c.cfg.Role != RoleCore {
		return errors.New("wire: only the core sends notices")
	}

	if len(payload) > MaxPayload {
		return fmt.Errorf("%w: a %d-byte payload", ErrFrameTooLarge, len(payload))
	}

	c.mu.Lock()

	if c.err != nil {
		err := c.err
		c.mu.Unlock()

		return err
	}

	f := newFrame(nil, 0, typeEvent, 0, payload)

	f.done = make(chan error, 1)
	c.enqueueLocked(f)
	c.mu.Unlock()

	select {
	case err := <-f.done:
		return err
	case <-ctx.Done():
		return canceled(ctx)
	case <-c.failed:
		return c.Err()
	}
}

// Close closes the connection: every stream fails with ErrConnClosed and
// rwc is closed, which must unblock its Read and Write. After any other
// failure the writer sends what it must (a RESET on stream 0 after a
// protocol error) and closes rwc itself; when a peer that stopped reading
// holds that write, rwc is closed under it AbortGrace after the failure.
func (c *Conn) Close() error {
	c.fail(ErrConnClosed, nil)

	return c.closeRWC()
}

// Err is why the connection failed, or nil while it runs. Every
// connection failure wraps ErrConnClosed and its cause: a protocol error's
// sentinel, ErrTruncated, a *RemoteError for the peer's RESET of the
// connection, or rwc's error. A stream failing alone (a reset, a credit
// overrun) never wraps ErrConnClosed.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.err
}

// Done is closed once the connection has failed and its goroutines have
// returned: at once after Close, at most AbortGrace after any other
// failure.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

func (c *Conn) closeRWC() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.rwc.Close()
	})

	return c.closeErr
}

// fail ends the connection with err, wrapped in ErrConnClosed: every
// stream fails, queued frames are dropped, and the writer sends abort (if
// any) and closes rwc, which closes anyway AbortGrace later. Only the
// first failure counts.
func (c *Conn) fail(err error, abort []byte) {
	if !errors.Is(err, ErrConnClosed) {
		err = fmt.Errorf("%w: %w", ErrConnClosed, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.err != nil {
		return
	}

	c.err = err
	c.abort = abort

	for _, s := range c.streams {
		if s.err == nil {
			s.err = err
		}

		s.freed = true
		s.released = true
		s.endLocked()
	}

	clear(c.streams)

	c.acceptQ = nil

	for _, f := range c.wq {
		f.release()
	}

	c.wq = nil

	close(c.failed)
	signal(c.wake)

	c.grace = c.cfg.Clock.AfterFunc(AbortGrace, func() {
		_ = c.closeRWC()
	})
}

func (c *Conn) newStream(parent context.Context, id uint32, local bool) *Stream {
	s := &Stream{
		c:          c,
		id:         id,
		local:      local,
		rcv:        make(chan struct{}, 1),
		snd:        make(chan struct{}, 1),
		done:       make(chan error, 1),
		recvCredit: c.cfg.Window,
		sendCredit: c.cfg.Window,
	}

	s.ctx, s.cancel = context.WithCancelCause(parent)

	if local {
		s.stop = context.AfterFunc(parent, func() {
			c.mu.Lock()
			c.resetLocked(s, CodeCancel, canceled(parent))
			c.mu.Unlock()
		})
	}

	return s
}

func newFrame(s *Stream, id uint32, typ frameType, flags uint8, payload []byte) *outFrame {
	f := &outFrame{
		s:   s,
		typ: typ,
	}
	if s != nil {
		f.done = s.done
	}

	if len(payload) >= poolMin {
		bp, ok := bufPool.Get().(*[]byte)
		if !ok {
			b := make([]byte, 0, headerSize+MaxPayload)

			bp = &b
		}

		f.pooled = bp
		f.buf = appendFrame((*bp)[:0], id, typ, flags, payload)
	} else {
		f.buf = appendFrame(make([]byte, 0, headerSize+len(payload)), id, typ, flags, payload)
	}

	return f
}

func resetFrame(id uint32, code Code) []byte {
	return appendFrame(make([]byte, 0, headerSize+4), id, typeReset, 0, binary.BigEndian.AppendUint32(nil, uint32(code)))
}

func (f *outFrame) release() {
	if f.pooled != nil {
		*f.pooled = f.buf[:0]
		bufPool.Put(f.pooled)

		f.pooled = nil
	}

	f.buf = nil
}

func (f *outFrame) finish(err error) {
	f.release()

	if f.done != nil {
		select {
		case f.done <- err:
		default:
		}
	}
}

func (c *Conn) enqueueLocked(f *outFrame) {
	c.wq = append(c.wq, f)
	signal(c.wake)
}

func (c *Conn) writeLoop() {
	defer close(c.writerDone)

	for {
		c.mu.Lock()

		for len(c.wq) == 0 && c.err == nil {
			c.mu.Unlock()
			<-c.wake
			c.mu.Lock()
		}

		if c.err != nil {
			abort := c.abort

			c.abort = nil
			c.mu.Unlock()

			if abort != nil {
				_, _ = c.rwc.Write(abort)
			}

			_ = c.closeRWC()

			c.mu.Lock()
			c.grace.Stop()
			c.mu.Unlock()

			return
		}

		f := c.wq[0]

		c.wq[0] = nil
		c.wq = c.wq[1:]

		buf, skip := c.prepareLocked(f)
		c.mu.Unlock()

		if buf == nil {
			f.finish(skip)

			continue
		}

		if _, err := c.rwc.Write(buf); err != nil {
			err = fmt.Errorf("%w: %w", ErrConnClosed, err)
			c.fail(err, nil)
			f.finish(err)

			continue
		}

		f.finish(nil)
	}
}

// prepareLocked is the bytes to write for f, or nil and the error its
// caller gets when it is not to be written: DATA and REPLY of a stream
// this side reset (its RESET follows), WINDOW once its direction ended.
func (c *Conn) prepareLocked(f *outFrame) ([]byte, error) {
	s := f.s

	switch {
	case s == nil || f.open:
		return f.buf, nil
	case f.typ == typeReset:
		// A stream holds its place in MaxStreams until its RESET leaves
		// the queue, so a peer that stops reading cannot make RESETs
		// pile up: at most one per stream it may have open.
		s.resetQueued = false

		if s.freed {
			c.releaseLocked(s)
		}

		return f.buf, nil

	case f.typ == typeWindow:
		s.windowQueued = false

		inc := s.pendingWindow

		s.pendingWindow = 0

		if inc == 0 || s.resetSent || s.recvEnded || s.freed {
			return nil, nil
		}

		return appendFrame(c.winBuf[:0], s.id, typeWindow, 0, binary.BigEndian.AppendUint32(c.winBuf[headerSize:headerSize], uint32(inc))), nil

	case s.resetSent:
		return nil, s.err
	}

	return f.buf, nil
}

func (c *Conn) readLoop() {
	defer func() {
		<-c.writerDone
		close(c.done)
	}()

	br := bufio.NewReaderSize(c.rwc, headerSize+MaxPayload)

	var hb [headerSize]byte

	for {
		h, p, err := readFrame(br, &hb)
		if err == nil {
			err = c.handle(h, p)
		}

		if err != nil {
			c.readFailed(err)

			return
		}
	}
}

// readFailed fails the connection with what ended reading: a clean end,
// a truncated frame, the peer's RESET of the connection, a protocol error
// (answered with a RESET on stream 0 naming it), or rwc's error.
func (c *Conn) readFailed(err error) {
	var remote *RemoteError

	switch {
	case errors.Is(err, io.EOF):
		c.fail(ErrConnClosed, nil)
	case errors.As(err, &remote), errors.Is(err, ErrTruncated), errors.Is(err, ErrConnClosed):
		c.fail(err, nil)
	default:
		if code, ok := codeOf(err); ok {
			c.fail(err, resetFrame(0, code))

			return
		}

		c.fail(fmt.Errorf("%w: %w", ErrConnClosed, err), nil)
	}
}

// peerOpenType is the frame that opens the peer's streams, CALL from the
// SDK or EVENT from the core, and their ids' parity.
func (c *Conn) peerOpenType() (frameType, uint32) {
	if c.cfg.Role == RoleCore {
		return typeCall, 1
	}

	return typeEvent, 0
}

// handle applies one frame the peer sent.
func (c *Conn) handle(h header, p []byte) error {
	if err := c.check(h, p); err != nil {
		return err
	}

	if h.stream == 0 {
		if h.typ == typeReset {
			return &RemoteError{
				Stream: 0,
				Code:   Code(binary.BigEndian.Uint32(p)),
			}
		}

		// A notice: check let EVENT through on RoleSDK only.
		if c.cfg.OnEvent != nil {
			c.cfg.OnEvent(p)
		}

		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.err != nil {
		return c.err
	}

	// A stream ended but still holding its place (its RESET queued) is
	// closed to the peer.
	if s := c.streams[h.stream]; s != nil && !s.freed {
		return c.handleStream(s, h, p)
	}

	return c.handleNew(h, p)
}

// check is what a frame must be wherever it comes: a type the peer's side
// sends, frames on stream 0 only EVENT and RESET, WINDOW and RESET
// payloads of 4 bytes and no END, a WINDOW of 1 to 2^31-1.
func (c *Conn) check(h header, p []byte) error {
	switch {
	case h.typ == typeCall && c.cfg.Role == RoleSDK:
		return fmt.Errorf("%w: CALL %d from the core", ErrBadFrame, h.stream)
	case h.typ == typeEvent && c.cfg.Role == RoleCore:
		return fmt.Errorf("%w: EVENT %d from the SDK", ErrBadFrame, h.stream)
	case h.stream == 0 && h.typ != typeEvent && h.typ != typeReset:
		return fmt.Errorf("%w: %s on stream 0", ErrBadFrame, h.typ)
	case h.stream == 0 && h.flags != 0:
		return fmt.Errorf("%w: END on %s 0", ErrBadFrame, h.typ)
	}

	if h.typ != typeWindow && h.typ != typeReset {
		return nil
	}

	if h.flags != 0 {
		return fmt.Errorf("%w: END on %s %d", ErrBadFrame, h.typ, h.stream)
	}

	if len(p) != 4 {
		return fmt.Errorf("%w: %s %d with a %d-byte payload", ErrBadFrame, h.typ, h.stream, len(p))
	}

	if inc := binary.BigEndian.Uint32(p); h.typ == typeWindow && (inc == 0 || inc > maxCredit) {
		return fmt.Errorf("%w: WINDOW %d of %d", ErrBadFrame, h.stream, inc)
	}

	return nil
}

// handleNew applies a frame on a stream not open: it opens one of the
// peer's, or it is a WINDOW or RESET in flight when the stream ended.
func (c *Conn) handleNew(h header, p []byte) error {
	open, parity := c.peerOpenType()
	peers := h.stream%2 == parity

	if h.typ == open {
		switch {
		case !peers:
			return fmt.Errorf("%w: %s %d", ErrWrongParity, h.typ, h.stream)
		case h.stream <= c.lastPeer:
			return fmt.Errorf("%w: %s %d reuses an id", ErrStreamClosed, h.typ, h.stream)
		case c.peerOpen >= c.cfg.MaxStreams:
			return fmt.Errorf("%w: %s %d with %d open", ErrStreamLimit, h.typ, h.stream, c.peerOpen)
		}

		s := c.newStream(context.Background(), h.stream, false)

		s.payload = p
		s.recvEnded = h.flags&flagEnd != 0
		c.lastPeer = h.stream
		c.peerOpen++

		c.streams[h.stream] = s
		c.acceptQ = append(c.acceptQ, s)
		signal(c.accepted)

		return nil
	}

	opened := uint64(h.stream) < c.nextID
	if peers {
		opened = h.stream <= c.lastPeer
	}

	switch {
	case !opened:
		return fmt.Errorf("%w: %s %d", ErrStreamNotOpen, h.typ, h.stream)
	case h.typ == typeWindow || h.typ == typeReset:
		return nil
	}

	return fmt.Errorf("%w: %s %d", ErrStreamClosed, h.typ, h.stream)
}

// handleStream applies a frame on an open stream, its errors in the
// precedence the README gives: the stream's state, then the order of its
// frames, then credit.
func (c *Conn) handleStream(s *Stream, h header, p []byte) error {
	end := h.flags&flagEnd != 0

	opening := h.typ == typeCall || h.typ == typeEvent

	if s.resetSent && !opening {
		// This side reset the stream: the peer's frames are in flight
		// until its END or RESET says it stopped.
		if h.typ == typeReset || end {
			s.recvEnded = true
			c.freeLocked(s)
		}

		return nil
	}

	switch h.typ {
	case typeCall, typeEvent:
		// check let only the peer's opening type through: it opens an
		// id already open, the peer's or this side's.
		if _, parity := c.peerOpenType(); h.stream%2 != parity {
			return fmt.Errorf("%w: %s %d", ErrWrongParity, h.typ, h.stream)
		}

		return fmt.Errorf("%w: %s %d reuses an id", ErrStreamClosed, h.typ, h.stream)

	case typeReply:
		switch {
		case s.recvEnded:
			return fmt.Errorf("%w: REPLY %d after END", ErrStreamClosed, h.stream)
		case !s.local:
			return fmt.Errorf("%w: REPLY %d from the stream's opener", ErrBadFrame, h.stream)
		case s.gotReply:
			return fmt.Errorf("%w: a second REPLY on %d", ErrBadFrame, h.stream)
		}

		s.gotReply = true
		s.reply = p
		s.recvEnded = end
		signal(s.rcv)
		c.maybeFreeLocked(s)

	case typeData:
		switch {
		case s.recvEnded:
			return fmt.Errorf("%w: DATA %d after END", ErrStreamClosed, h.stream)
		case s.local && !s.gotReply:
			return fmt.Errorf("%w: DATA %d before REPLY", ErrBadFrame, h.stream)
		}

		s.recvEnded = end

		if len(p) > s.recvCredit {
			c.resetLocked(s, CodeCreditOverrun, fmt.Errorf("%w: %d bytes on stream %d with %d of credit", ErrCreditOverrun, len(p), h.stream, s.recvCredit))

			return nil
		}

		s.recvCredit -= len(p)

		if len(p) > 0 {
			s.recv = append(s.recv, p)

			s.buffered += len(p)
		}

		signal(s.rcv)
		c.maybeFreeLocked(s)

	case typeWindow:
		if s.sendEnded {
			return nil
		}

		inc := int(binary.BigEndian.Uint32(p))
		if s.sendCredit > maxCredit-inc {
			return fmt.Errorf("%w: WINDOW %d takes the credit past 2^31-1", ErrBadFrame, h.stream)
		}

		s.sendCredit += inc
		signal(s.snd)

	case typeReset:
		s.err = &RemoteError{
			Stream: s.id,
			Code:   Code(binary.BigEndian.Uint32(p)),
		}
		s.recvEnded = true

		// Answer, so the peer knows this side stopped too, unless an END
		// already said so.
		if !s.sendEnded {
			s.sendEnded = true
			s.resetQueued = true
			c.enqueueLocked(&outFrame{
				s:   s,
				typ: typeReset,
				buf: resetFrame(s.id, CodeCancel),
			})
		}

		c.removeAcceptLocked(s)
		c.freeLocked(s)
	}

	return nil
}

// resetLocked resets s from this side with code, failing it with err. The
// stream stays until the peer's END or RESET shows it stopped sending.
func (c *Conn) resetLocked(s *Stream, code Code, err error) {
	if s.freed || s.resetSent {
		return
	}

	s.err = err
	s.resetSent = true
	s.sendEnded = true
	s.resetQueued = true
	c.enqueueLocked(&outFrame{
		s:   s,
		typ: typeReset,
		buf: resetFrame(s.id, code),
	})
	c.removeAcceptLocked(s)
	s.cancel(err)
	signal(s.rcv)
	signal(s.snd)

	if s.recvEnded {
		c.freeLocked(s)
	}
}

func (c *Conn) maybeFreeLocked(s *Stream) {
	if s.sendEnded && s.recvEnded && !s.resetSent {
		c.freeLocked(s)
	}
}

// freeLocked ends a stream: both sides stopped sending on it. Its caller
// may still read what it buffered, unless it failed. Its place in
// MaxStreams is released now, or once its RESET leaves the queue.
func (c *Conn) freeLocked(s *Stream) {
	if s.freed {
		return
	}

	s.freed = true

	if s.err != nil {
		s.recv = nil
		s.buffered = 0
	}

	s.endLocked()

	if !s.resetQueued {
		c.releaseLocked(s)
	}
}

// releaseLocked forgets an ended stream and frees its place in MaxStreams.
func (c *Conn) releaseLocked(s *Stream) {
	if s.released {
		return
	}

	s.released = true
	delete(c.streams, s.id)

	if s.local {
		<-c.slots
	} else {
		c.peerOpen--
	}
}

// removeAcceptLocked drops a stream the peer opened and this side never
// accepted: nothing will read its payload.
func (c *Conn) removeAcceptLocked(s *Stream) {
	if i := slices.Index(c.acceptQ, s); i >= 0 {
		c.acceptQ = slices.Delete(c.acceptQ, i, i+1)
		s.payload = nil
	}
}

// signal wakes a goroutine waiting on ch, a channel of one.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func canceled(ctx context.Context) error {
	return fmt.Errorf("%w: %w", ErrCanceled, context.Cause(ctx))
}
