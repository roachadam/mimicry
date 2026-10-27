package quictrace

import (
	"cmp"
	"math"
	"slices"

	"golang.org/x/net/http2/hpack"
)

// Rederive recomputes a Conn's client-side facts from its wire data rather
// than the server's view: transport parameters, handshake completion,
// H3 stream and request order, and each request's Blocked. Applying it on
// load keeps old captures consistent with current rules.
func (c *Conn) Rederive() {
	if b := c.ClientHelloBytes(); b != nil {
		if h, err := readHello(b); err == nil && h.transportParameters != nil {
			if tp, err := ParseTransportParameters(h.transportParameters); err == nil {
				c.TransportParameters = tp
			}
		}
	}
	if !c.HandshakeCompleted() {
		c.H3 = nil
		return
	}
	if c.H3 == nil {
		return
	}
	w := c.wire()
	pos := func(stream uint64) int {
		if s, ok := w.first[stream]; ok {
			return s
		}
		return math.MaxInt
	}
	slices.SortStableFunc(c.H3.Streams, func(a, b H3Stream) int {
		return cmp.Or(cmp.Compare(pos(a.ID), pos(b.ID)), cmp.Compare(a.ID, b.ID))
	})
	slices.SortStableFunc(c.H3.Requests, func(a, b H3Request) int {
		return cmp.Or(cmp.Compare(pos(a.Stream), pos(b.Stream)), cmp.Compare(a.Stream, b.Stream))
	})
	var ends []uint64 // encoder stream offset after each insert
	var encoder *uint64
	for i := range c.H3.Streams {
		s := &c.H3.Streams[i]
		if s.Type != StreamQPACKEncoder {
			continue
		}
		encoder = &s.ID
		end := uint64(len(AppendVarint(nil, s.Type)))
		for _, in := range s.Encoder {
			end += in.size()
			if in.Op != OpSetCapacity {
				ends = append(ends, end)
			}
		}
		break
	}
	for i := range c.H3.Requests {
		q := &c.H3.Requests[i]
		q.Blocked = false
		if q.Section.RequiredInsertCount == 0 {
			continue
		}
		arrived := uint64(len(ends))
		if at, ok := w.headersAt(q); ok {
			reached := uint64(0)
			if encoder != nil {
				reached = w.reachedBefore(*encoder, at)
			}
			n, _ := slices.BinarySearch(ends, reached+1)
			arrived = uint64(n)
		}
		q.Blocked = q.Section.RequiredInsertCount > arrived
	}
}

// HandshakeCompleted reports that the client finished its handshake: it
// sent CRYPTO data (its Finished) in a Handshake packet.
func (c *Conn) HandshakeCompleted() bool {
	i, _ := c.FinishedDatagram()
	return i >= 0
}

// FinishedDatagram is the index and µs offset of the client datagram
// carrying its Finished; -1 for none.
func (c *Conn) FinishedDatagram() (index int, micros int64) {
	for di, d := range c.Datagrams {
		for _, p := range d.Packets {
			if p.Type != PacketHandshake {
				continue
			}
			for _, f := range p.Frames {
				if f.Type == FrameCrypto {
					return di, d.Micros
				}
			}
		}
	}
	return -1, 0
}

// StreamStart is where a stream's first STREAM frame went: its datagram's
// index among the client's and its packet's type.
type StreamStart struct {
	Datagram   int
	PacketType string
}

// StreamStarts finds each stream's first STREAM frame on the client's
// wire.
func (c *Conn) StreamStarts() map[uint64]StreamStart {
	out := map[uint64]StreamStart{}
	for di, d := range c.Datagrams {
		for _, p := range d.Packets {
			for _, f := range p.Frames {
				if f.Type != FrameStream || f.Stream == nil {
					continue
				}
				if _, ok := out[*f.Stream]; !ok {
					out[*f.Stream] = StreamStart{
						Datagram:   di,
						PacketType: p.Type,
					}
				}
			}
		}
	}
	return out
}

// streamFrame is one STREAM frame and its position among every frame the
// client sent (datagram, packet and frame order).
type streamFrame struct {
	pos            int
	stream         uint64
	offset, length uint64
}

// wireFrames are a connection's STREAM frames in wire order, and the
// position of each stream's first.
type wireFrames struct {
	frames []streamFrame
	first  map[uint64]int
}

func (c *Conn) wire() wireFrames {
	w := wireFrames{
		first: map[uint64]int{},
	}
	pos := 0
	for _, d := range c.Datagrams {
		for _, p := range d.Packets {
			for _, f := range p.Frames {
				pos++
				if f.Type != FrameStream || f.Stream == nil {
					continue
				}
				w.frames = append(w.frames, streamFrame{
					pos:    pos,
					stream: *f.Stream,
					offset: f.Offset,
					length: f.Length,
				})
				if _, ok := w.first[*f.Stream]; !ok {
					w.first[*f.Stream] = pos
				}
			}
		}
	}
	return w
}

// headersAt is the position of the first STREAM frame carrying the start
// of q's first HEADERS frame.
func (w wireFrames) headersAt(q *H3Request) (int, bool) {
	offset := uint64(0)
	found := false
	for _, f := range q.Frames {
		if f.Type == FrameH3Headers {
			found = true
			break
		}
		offset += uint64(len(AppendVarint(AppendVarint(nil, f.Type), f.Length))) + f.Length
	}
	if !found {
		return 0, false
	}
	for _, f := range w.frames {
		if f.stream == q.Stream && f.offset <= offset && offset < f.offset+f.length {
			return f.pos, true
		}
	}
	return 0, false
}

// reachedBefore is the highest offset of stream sent in STREAM frames
// before position at.
func (w wireFrames) reachedBefore(stream uint64, at int) uint64 {
	reached := uint64(0)
	for _, f := range w.frames {
		if f.pos >= at {
			break
		}
		if f.stream == stream {
			reached = max(reached, f.offset+f.length)
		}
	}
	return reached
}

// size is the instruction's encoded length, with the shortest integer
// encodings and Huffman strings of hpack's (QPACK's) code.
func (in EncoderInstruction) size() uint64 {
	switch in.Op {
	case OpSetCapacity:
		return prefixIntSize(5, in.Capacity)
	case OpInsertNameRef:
		return prefixIntSize(6, in.Index) + stringSize(7, in.Value, in.ValueHuffman)
	case OpInsertLiteral:
		return stringSize(5, in.Name, in.NameHuffman) + stringSize(7, in.Value, in.ValueHuffman)
	}
	return prefixIntSize(5, in.Index)
}

func prefixIntSize(n uint, v uint64) uint64 {
	return uint64(len(appendPrefixInt(nil, 0, n, v)))
}

// stringSize is a string literal's length with an n-bit length prefix
// (the Huffman flag is the bit above it).
func stringSize(n uint, s string, huffman bool) uint64 {
	l := uint64(len(s))
	if huffman {
		l = hpack.HuffmanEncodeLength(s)
	}
	return prefixIntSize(n, l) + l
}
