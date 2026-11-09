package quic

// mimicry: DATAGRAM frames (RFC 9221) on a client connection that asks for
// them (DialConfig.Datagrams): the HTTP/3 datagrams of a CONNECT-UDP proxy
// (RFC 9297, RFC 9298). Not in upstream; see UPSTREAM.md.

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roachadam/mimicry/internal/quic/quicwire"
)

const (
	// maxDatagramQueue bounds the bytes SendDatagram queues for the
	// loop; a datagram beyond it is dropped, as a full socket buffer
	// drops one.
	maxDatagramQueue = 1 << 20
	// datagramInbox is how many received datagrams wait for
	// ReceiveDatagram; more are dropped.
	datagramInbox = 1024
	// datagramFrameOverhead is a DATAGRAM frame's type and a two-byte
	// length (0x31): what the frame adds to its payload.
	datagramFrameOverhead = 3
	// datagramPacketOverhead is the most a 1-RTT packet adds around its
	// frames: the first byte, the longest connection ID, the longest
	// packet number and the AEAD tag. A payload within the datagram size
	// less this and datagramFrameOverhead fits any packet with nothing
	// else in it.
	datagramPacketOverhead = 1 + maxConnIDLen + 4 + aeadOverhead
)

// ErrNoDatagrams is the error of SendDatagram and ReceiveDatagram on a
// connection that carries no DATAGRAM frames: it did not ask for them
// (DialConfig.Datagrams), or the peer announced none
// (max_datagram_frame_size).
var ErrNoDatagrams = errors.New("quic: the connection carries no DATAGRAM frames")

// errDatagramTooLarge is a datagram over MaxDatagramSize.
var errDatagramTooLarge = errors.New("quic: datagram larger than the connection carries")

// clientDatagrams are a connection's DATAGRAM frames, to send and received.
type clientDatagrams struct {
	// peerMax is the payload limit the peer's max_datagram_frame_size
	// sets, once its transport parameters arrived; zero: it takes none.
	peerMax atomic.Int64
	// mu guards out, the datagrams SendDatagram queued, and their size.
	mu       sync.Mutex
	out      [][]byte
	outBytes int
	// in holds the datagrams received for ReceiveDatagram.
	in chan []byte
}

func newClientDatagrams() *clientDatagrams {
	return &clientDatagrams{
		in: make(chan []byte, datagramInbox),
	}
}

// MaxDatagramSize is the largest payload SendDatagram sends: what fits a
// packet of the profile's datagram size with nothing else in it, within
// the peer's max_datagram_frame_size. Zero until the peer's transport
// parameters arrive, and on a connection that carries no DATAGRAM frames.
func (c *Conn) MaxDatagramSize() int {
	d := c.client.datagrams
	if d == nil {
		return 0
	}

	return int(min(d.peerMax.Load(), int64(c.client.p.DatagramSize-datagramPacketOverhead-datagramFrameOverhead)))
}

// SendDatagram queues b to go in a DATAGRAM frame of the next 1-RTT
// packet with room for it, under congestion control, never sent again
// when lost (RFC 9221, section 5). A datagram the queue has no room for is
// dropped without an error, as a full socket drops one. b may be reused
// once it returns.
func (c *Conn) SendDatagram(b []byte) error {
	d := c.client.datagrams

	switch limit := c.MaxDatagramSize(); {
	case limit == 0:
		return ErrNoDatagrams
	case len(b) > limit:
		return errDatagramTooLarge
	}

	select {
	case <-c.lifetime.donec:
		return errConnClosed
	default:
	}

	d.mu.Lock()

	if d.outBytes+len(b) > maxDatagramQueue {
		d.mu.Unlock()

		return nil
	}

	d.out = append(d.out, bytes.Clone(b))

	d.outBytes += len(b)
	d.mu.Unlock()

	c.wake()

	return nil
}

// ReceiveDatagram returns the payload of the next DATAGRAM frame the peer
// sent, waiting for one until ctx ends or the connection does.
func (c *Conn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	d := c.client.datagrams
	if d == nil {
		return nil, ErrNoDatagrams
	}

	select {
	case b := <-d.in:
		return b, nil
	case <-c.lifetime.donec:
		return nil, errConnClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// clientPeerDatagrams records the peer's max_datagram_frame_size (zero:
// none) as the largest payload its frames may carry.
func (c *Conn) clientPeerDatagrams(maxFrame int64) {
	if d := c.client.datagrams; d != nil && maxFrame > datagramFrameOverhead {
		d.peerMax.Store(maxFrame - datagramFrameOverhead)
	}
}

// clientReceivedDatagram hands the payload of a DATAGRAM frame of size
// bytes to ReceiveDatagram, or drops it when the connection did not ask
// for them or the inbox is full. A frame larger than the
// max_datagram_frame_size the client announced ends the connection with
// PROTOCOL_VIOLATION (RFC 9221, section 3).
func (c *Conn) clientReceivedDatagram(now time.Time, size int, b []byte) {
	for _, tp := range c.client.p.TransportParameters {
		if tp.ID == paramMaxDatagramFrameSize && tp.Value != nil && uint64(size) > *tp.Value {
			c.abort(now, localTransportError{
				code:   errProtocolViolation,
				reason: "DATAGRAM frame larger than max_datagram_frame_size",
			})

			return
		}
	}

	d := c.client.datagrams
	if d == nil {
		return
	}

	select {
	case d.in <- bytes.Clone(b):
	default:
	}
}

// appendDatagramFrames appends the queued datagrams that fit the packet,
// in order, to a 1-RTT packet. It reports whether one is left waiting for
// a packet with more room: the caller then sends this one even if it holds
// only an ACK, so the next one has the room.
func (c *Conn) appendDatagramFrames() (waiting bool) {
	d := c.client.datagrams
	if d == nil || !c.keysAppData.canWrite() {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	sent := 0

	for _, b := range d.out {
		if !c.w.appendDatagramFrame(b) {
			waiting = true

			break
		}

		sent++

		d.outBytes -= len(b)
	}

	clear(d.out[:sent])

	d.out = d.out[:copy(d.out, d.out[sent:])]

	return waiting
}

// appendDatagramFrame appends a DATAGRAM frame with a length carrying b.
// It is ack-eliciting but never retransmitted, so nothing of it is
// recorded for loss.
func (w *packetWriter) appendDatagramFrame(b []byte) (added bool) {
	if w.avail() < 1+quicwire.SizeVarint(uint64(len(b)))+len(b) {
		return false
	}

	w.b = append(w.b, frameTypeDatagramWithLen)
	w.b = quicwire.AppendVarintBytes(w.b, b)
	w.sent.markAckEliciting()

	return true
}
