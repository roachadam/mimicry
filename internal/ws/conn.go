// Package ws is the WebSocket protocol (RFC 6455) after the handshake, as
// the profile's browser speaks it: framing, permessage-deflate through
// internal/zdeflate, HTTP/2 DATA packing, pings and the close handshake.
// It also checks a socket's URL and subprotocols as the WebSocket
// constructor does, makes the handshake's key, and checks the server's
// answer (status, accept key, subprotocol, extensions).
//
// Every client message is one final frame, masked with a fresh key, its
// length in the shortest form. The transport underneath (an HTTP/1.1
// connection or an HTTP/2 stream) is the caller's, behind Transport.
package ws

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/internal/zdeflate"
	"github.com/roachadam/mimicry/profile"
)

// Transport carries a socket's frames: an HTTP/1.1 connection after its
// 101, or an HTTP/2 extended CONNECT stream (RFC 8441).
type Transport interface {
	// Read reads what the server sent; an error once it ended the
	// transport (io.EOF: closed the connection, or ended its stream).
	io.Reader
	// Send writes one frame as parts: over HTTP/2 each part goes in DATA
	// frames of its own (cut at the DATA cap and the flow-control
	// windows), the last with END_STREAM when end; over HTTP/1.1 they go
	// together. A failed Send may have written part of the frame.
	Send(ctx context.Context, parts [][]byte, end bool) error
	// Close ends the transport now: it closes the TCP connection, or
	// resets the stream (RST_STREAM NO_ERROR) unless both sides ended it.
	Close() error
	// Release frees the transport once the server ended it, sending
	// nothing more.
	Release()
}

// Limits on what the server sends, so a hostile one cannot make the
// socket buffer without bound. Browsers' own limits were not measured.
const (
	// MaxMessage bounds a message's payload, inflated or not.
	MaxMessage = 64 << 20
	// queued is how many messages wait for ReadMessage before the socket
	// stops reading (and answering pings) until one is taken.
	queued = 64
)

// Timeouts no capture measured: how long to wait for the server's close
// frame after ours, and for the server to end the transport where the
// browser leaves that to it.
const (
	closeTimeout = 10 * time.Second
	serverLinger = 10 * time.Second
)

// Config is how a socket speaks: the profile's browser, on HTTP/2 or
// HTTP/1.1, with the extensions the server accepted.
type Config struct {
	Profile *profile.WebSocket
	// Name is the profile's, for errors.
	Name string
	// H2 is set for an HTTP/2 stream; MaxData is then the most a DATA
	// frame carries (the profile's cap, or the peer's frame size).
	H2      bool
	MaxData int
	// Deflate is permessage-deflate as accepted, or nil.
	Deflate *Deflate
	Clock   clockwork.Clock
	// Rand draws masking keys (crypto/rand).
	Rand io.Reader
}

// CloseError is the close frame that ended a socket: the server's, or
// its echo of ours. Code is 1005 for a close frame without one.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("websocket closed: %d %q", e.Code, e.Reason)
}

// ErrClosing is a message written after the socket sent its close frame.
var ErrClosing = errors.New("ws: the socket is closing")

// errProtocol is a frame the server may not send (RFC 6455); the socket
// fails without a close handshake (what browsers then send is not
// measured).
var errProtocol = errors.New("ws: protocol error")

// Message is one data message the server sent.
type Message struct {
	Opcode  byte
	Payload []byte
}

// Conn is one WebSocket after its handshake, framed, compressed and closed
// as the profile's browser does. A goroutine reads the server's frames from
// the start, so pings are answered even without ReadMessage.
type Conn struct {
	t   Transport
	cfg Config
	br  *bufio.Reader

	// wmu serializes frames on the wire and guards deflater.
	wmu      sync.Mutex
	deflater *Compressor
	inflater *zdeflate.Reader

	msgs chan Message
	// readErr is why ReadMessage returns no more: set before msgs closes.
	readErr error

	mu sync.Mutex
	// sentClose and gotClose track the close handshake; closing is closed
	// once both happened or the transport failed. stop closes with
	// sentClose: browsers drop messages that arrive after it.
	sentClose bool
	gotClose  bool
	stop      chan struct{}
	closing   chan struct{}
	closeOnce sync.Once
	// ended is closed once the transport has ended.
	ended chan struct{}
	// abort, set once our close frame went out, ends the transport if
	// the server's close never comes.
	abort clockwork.Timer
	// failed is set once a write may have broken the framing.
	failed error
}

// markSent records that our close frame is going out (or a reply to the
// server's). c.mu must be held.
func (c *Conn) markSent() {
	if !c.sentClose {
		c.sentClose = true
		close(c.stop)
	}
}

// New runs a socket on t. r reads what follows the handshake: an HTTP/1.1
// connection's buffered reader, which may already hold server frames, or t.
// It fails, sending nothing, where the browser fails the socket (a
// compression window it cannot use); the caller ends t.
func New(t Transport, r io.Reader, cfg Config) (*Conn, error) {
	c := &Conn{
		t:       t,
		cfg:     cfg,
		br:      bufio.NewReader(r),
		msgs:    make(chan Message, queued),
		stop:    make(chan struct{}),
		closing: make(chan struct{}),
		ended:   make(chan struct{}),
	}
	if d := cfg.Deflate; d != nil {
		w, err := NewCompressor(cfg.Profile, d)
		if err != nil {
			return nil, fmt.Errorf("ws: profile %s fails the socket: %w", cfg.Name, err)
		}

		c.deflater = w
		c.inflater = zdeflate.NewReader()
	}

	go c.readLoop()

	return c, nil
}

// ReadMessage returns the next data message, or once the socket closed a
// *CloseError (the close handshake) or the error that ended it.
func (c *Conn) ReadMessage(ctx context.Context) (Message, error) {
	select {
	case m, ok := <-c.msgs:
		if !ok {
			return Message{}, c.readErr
		}

		return m, nil

	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

// WriteMessage sends p as one message of opcode (text or binary), in a
// single frame with its length in the shortest form, compressed where the
// profile's browser compresses it.
func (c *Conn) WriteMessage(ctx context.Context, opcode byte, p []byte) error {
	if opcode != OpText && opcode != OpBinary {
		return fmt.Errorf("ws: opcode %#x is not a data message", opcode)
	}

	if opcode == OpText && !utf8.Valid(p) {
		return errors.New("ws: a text message must be UTF-8")
	}

	c.wmu.Lock()
	defer c.wmu.Unlock()

	c.mu.Lock()

	closing := c.sentClose || c.gotClose
	c.mu.Unlock()

	if closing {
		return ErrClosing
	}

	var rsv byte

	payload := p

	if c.deflater != nil {
		var compressed bool
		if payload, compressed = c.deflater.Message(p); compressed {
			rsv = rsv1
		}
	}

	return c.writeFrame(ctx, opcode, rsv, payload, false, false)
}

// writeFrame masks payload with a fresh key and sends it as one final
// frame, packed as the browser packs it. reply marks an answer to the
// server (a pong, a close reply); end ends our side of an HTTP/2 stream.
// wmu must be held.
func (c *Conn) writeFrame(ctx context.Context, opcode, rsv byte, payload []byte, reply, end bool) error {
	if c.failed != nil {
		return c.failed
	}

	h := Header{
		Fin:    true,
		RSV:    rsv,
		Opcode: opcode,
		Masked: true,
		Length: int64(len(payload)),
	}
	if _, err := io.ReadFull(c.cfg.Rand, h.Mask[:]); err != nil {
		return err
	}

	frame := AppendHeader(make([]byte, 0, 14+len(payload)), h)
	head := len(frame)

	frame = append(frame, payload...)
	MaskBytes(h.Mask, frame[head:])

	if err := c.t.Send(ctx, c.parts(frame, head, opcode, reply), end); err != nil {
		// Part of the frame may be on the wire: nothing can follow it.
		c.failed = fmt.Errorf("ws: writing a frame failed: %w", err)
		_ = c.t.Close()

		return err
	}

	return nil
}

// parts cuts frame (its header, mask included, head bytes long) the way
// the profile's browser writes it into DATA frames over HTTP/2.
func (c *Conn) parts(frame []byte, head int, opcode byte, reply bool) [][]byte {
	if !c.cfg.H2 {
		return [][]byte{frame}
	}

	switch c.cfg.Profile.H2Data {
	case profile.H2DataHeaderAlone:
		// Firefox writes a payload over kCopyBreak apart from its header.
		if len(frame)-head > headerAloneAfter {
			return [][]byte{frame[:head], frame[head:]}
		}

	case profile.H2DataSplitControl:
		if reply && opcode >= OpClose {
			// Control frames have a 2-byte header and a 4-byte mask.
			parts := [][]byte{frame[:2], frame[2:6]}
			if len(frame) > 6 {
				parts = append(parts, frame[6:])
			}

			return parts
		}
	}

	return [][]byte{frame}
}

// headerAloneAfter is the longest payload H2DataHeaderAlone writes with
// its header (Firefox's WebSocketChannel kCopyBreak).
const headerAloneAfter = 1000

// closeEnds reports whether a close frame ends the client's side of an
// HTTP/2 stream (CloseEndStream).
func (c *Conn) closeEnds() bool {
	return c.cfg.H2 && c.cfg.Profile.CloseH2 == profile.CloseEndStream
}

// Close starts the close handshake, unless started, and returns once it is
// done and, where the browser's close event waits for it, the transport
// has ended. A server whose close never comes has the transport ended
// after closeTimeout.
func (c *Conn) Close(code int, reason string) error {
	if code != 1000 && (code < 3000 || code > 4999) {
		return fmt.Errorf("ws: close code %d: browsers send 1000 or 3000 to 4999", code)
	}

	if len(reason) > maxControl-2 || !utf8.ValidString(reason) {
		return errors.New("ws: a close reason is at most 123 bytes of UTF-8")
	}

	c.wmu.Lock()
	c.mu.Lock()

	send := !c.sentClose && !c.gotClose
	c.markSent()
	c.mu.Unlock()

	var err error
	if send {
		payload := binary.BigEndian.AppendUint16(nil, uint16(code))

		payload = append(payload, reason...)

		err = c.writeFrame(context.Background(), OpClose, 0, payload, false, c.closeEnds())
		if err == nil {
			c.mu.Lock()

			if !c.gotClose {
				c.abort = c.cfg.Clock.AfterFunc(closeTimeout, func() {
					_ = c.t.Close()
				})
			}

			c.mu.Unlock()
		}
	}

	c.wmu.Unlock()

	if err != nil {
		return err
	}

	<-c.closing

	if c.waitsForEnd() {
		<-c.ended
	}

	if _, ok := errors.AsType[*CloseError](c.readErr); !ok {
		// The transport ended without the server's close frame.
		return c.readErr
	}

	return nil
}

// waitsForEnd reports whether the browser's close event waits for the
// transport to end: where it ends it itself, and Chrome over HTTP/2, whose
// stream stays open until the server ends it. See docs/concepts/websockets.md.
func (c *Conn) waitsForEnd() bool {
	if c.cfg.H2 {
		return c.cfg.Profile.CloseH2 != ""
	}

	return c.cfg.Profile.CloseH1 != ""
}

// readLoop reads the server's frames until the transport ends, answering
// pings and the server's close, and ends the transport once the close
// handshake is done.
func (c *Conn) readLoop() {
	err := c.read()
	if ce, ok := errors.AsType[*CloseError](err); ok {
		// The close handshake is done: end the transport as the browser
		// does, reading on to see whether the server ends it first.
		c.handshakeDone(ce)

		err = c.drain()
	} else {
		c.handshakeDone(err)
	}

	if errors.Is(err, io.EOF) {
		c.t.Release()
	} else {
		_ = c.t.Close()
	}

	c.mu.Lock()

	if c.abort != nil {
		c.abort.Stop()
	}

	c.mu.Unlock()
	close(c.ended)
}

// handshakeDone ends ReadMessage with err and releases Close.
func (c *Conn) handshakeDone(err error) {
	c.closeOnce.Do(func() {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}

		c.readErr = err
		close(c.msgs)
		close(c.closing)
	})
}

// drain ends the transport as the browser does after the close handshake:
// at once, or after giving the server a while to end it first. It returns
// io.EOF if the server ended it.
func (c *Conn) drain() error {
	wait := serverLinger
	ws := c.cfg.Profile

	switch {
	case c.cfg.H2 && ws.CloseH2 == profile.CloseResetStream, !c.cfg.H2 && ws.CloseH1 == profile.CloseAtOnce:
		wait = 0
	case !c.cfg.H2 && ws.CloseH1 == profile.CloseAfterWait:
		wait = time.Duration(ws.CloseWaitMillis) * time.Millisecond
	}

	if wait == 0 {
		_ = c.t.Close()
		return nil
	}

	t := c.cfg.Clock.AfterFunc(wait, func() {
		_ = c.t.Close()
	})

	defer t.Stop()

	_, err := io.Copy(io.Discard, c.br)
	if err == nil && t.Stop() {
		// The server ended the transport before the wait was over.
		return io.EOF
	}

	return err
}

// read reads frames until the close handshake is done (a *CloseError) or
// the transport fails.
func (c *Conn) read() error {
	var (
		message    bytes.Buffer
		inMessage  bool
		opcode     byte
		compressed bool
	)
	for {
		h, err := ReadHeader(c.br)
		if err != nil {
			return err
		}

		if h.Masked {
			return fmt.Errorf("%w: a masked frame from the server", errProtocol)
		}

		data := h.Opcode < OpClose
		switch {
		case h.RSV&^rsv1 != 0, h.RSV != 0 && (c.cfg.Deflate == nil || h.Opcode != OpText && h.Opcode != OpBinary):
			return fmt.Errorf("%w: reserved bits %#x on opcode %#x", errProtocol, h.RSV, h.Opcode)
		case h.Opcode == OpContinuation && !inMessage:
			return fmt.Errorf("%w: a continuation outside a message", errProtocol)
		case data && h.Opcode != OpContinuation && inMessage:
			return fmt.Errorf("%w: a new message inside a fragmented one", errProtocol)
		case data && int64(message.Len())+h.Length > MaxMessage:
			return fmt.Errorf("ws: a message of more than %d bytes", MaxMessage)
		}

		if !data {
			payload := make([]byte, h.Length)
			if _, err := io.ReadFull(c.br, payload); err != nil {
				return unexpected(err)
			}

			if done, err := c.control(h.Opcode, payload); done || err != nil {
				return err
			}

			continue
		}

		if h.Opcode != OpContinuation {
			inMessage, opcode, compressed = true, h.Opcode, h.RSV&rsv1 != 0

			message.Reset()
		}

		if _, err := io.CopyN(&message, c.br, h.Length); err != nil {
			return unexpected(err)
		}

		if !h.Fin {
			continue
		}

		inMessage = false

		payload := bytes.Clone(message.Bytes())

		if compressed {
			if c.cfg.Deflate.ServerNoContextTakeover {
				c.inflater = zdeflate.NewReader()
			}

			if payload, err = c.inflater.Message(payload, MaxMessage); err != nil {
				return err
			}
		}

		select {
		case <-c.stop:
			// Our close went out: browsers deliver nothing after it.
			continue
		default:
		}

		select {
		case <-c.stop:
		case c.msgs <- Message{
			Opcode:  opcode,
			Payload: payload,
		}:
		}
	}
}

// control answers a ping with its payload. A close completes the close
// handshake (done), echoed first with the server's code and reason unless
// ours was sent, as every browser does.
func (c *Conn) control(opcode byte, payload []byte) (done bool, err error) {
	switch opcode {
	case OpPing:
		c.wmu.Lock()
		defer c.wmu.Unlock()

		c.mu.Lock()

		closing := c.sentClose
		c.mu.Unlock()

		if closing {
			return false, nil
		}

		return false, c.writeFrame(context.Background(), OpPong, 0, payload, true, false)

	case OpClose:
		ce := &CloseError{
			Code: 1005,
		}
		switch {
		case len(payload) == 1:
			return false, fmt.Errorf("%w: a 1-byte close frame", errProtocol)
		case len(payload) >= 2:
			ce.Code = int(binary.BigEndian.Uint16(payload))
			ce.Reason = string(payload[2:])

			if !utf8.ValidString(ce.Reason) {
				return false, fmt.Errorf("%w: a close reason that is not UTF-8", errProtocol)
			}
		}

		c.wmu.Lock()
		defer c.wmu.Unlock()

		c.mu.Lock()

		reply := !c.sentClose
		c.markSent()

		c.gotClose = true
		if c.abort != nil {
			c.abort.Stop()
		}

		c.mu.Unlock()

		if reply {
			if err := c.writeFrame(context.Background(), OpClose, 0, payload, true, c.closeEnds()); err != nil {
				return false, err
			}
		}

		return true, ce
	}

	// A pong: nothing to do.
	return false, nil
}
