package ws

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame opcodes (RFC 6455, section 5.2).
const (
	OpContinuation byte = 0x0
	OpText         byte = 0x1
	OpBinary       byte = 0x2
	OpClose        byte = 0x8
	OpPing         byte = 0x9
	OpPong         byte = 0xa
)

// rsv1 is the RSV1 bit of a frame's first byte: a compressed message
// (permessage-deflate, RFC 7692).
const rsv1 = 0x40

// maxControl is the largest control frame payload (RFC 6455, section
// 5.5).
const maxControl = 125

// Header is a frame's header as on the wire.
type Header struct {
	Fin bool
	// RSV holds RSV1 to RSV3 as the first byte carries them (0x40, 0x20,
	// 0x10).
	RSV    byte
	Opcode byte
	Masked bool
	Mask   [4]byte
	Length int64
}

// Errors reading a frame the peer may not send.
var (
	errReservedOpcode = errors.New("ws: reserved opcode")
	errControlFrame   = errors.New("ws: control frame fragmented or longer than 125 bytes")
	errLength         = errors.New("ws: frame length has its most significant bit set")
)

// AppendHeader appends h's encoding to b, its length in the shortest
// form, as every browser writes it (7, 16 or 64 bits).
func AppendHeader(b []byte, h Header) []byte {
	first := h.RSV&0x70 | h.Opcode&0xf
	if h.Fin {
		first |= 0x80
	}

	var mask byte
	if h.Masked {
		mask = 0x80
	}

	b = append(b, first)
	switch n := h.Length; {
	case n < 126:
		b = append(b, mask|byte(n))
	case n <= 0xffff:
		b = append(b, mask|126)
		b = binary.BigEndian.AppendUint16(b, uint16(n))

	default:
		b = append(b, mask|127)
		b = binary.BigEndian.AppendUint64(b, uint64(n))
	}

	if h.Masked {
		b = append(b, h.Mask[:]...)
	}

	return b
}

// ReadHeader reads a frame header from r. It returns io.EOF only when r
// ends before the frame starts. Reserved opcodes, fragmented or long
// control frames and lengths of 2^63 or more are errors (RFC 6455, 5.2 and
// 5.5); an overlong length form is accepted.
func ReadHeader(r io.Reader) (Header, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:2]); err != nil {
		return Header{}, err
	}

	h := Header{
		Fin:    b[0]&0x80 != 0,
		RSV:    b[0] & 0x70,
		Opcode: b[0] & 0xf,
		Masked: b[1]&0x80 != 0,
		Length: int64(b[1] & 0x7f),
	}
	switch h.Length {
	case 126:
		if _, err := io.ReadFull(r, b[:2]); err != nil {
			return h, unexpected(err)
		}

		h.Length = int64(binary.BigEndian.Uint16(b[:2]))

	case 127:
		if _, err := io.ReadFull(r, b[:8]); err != nil {
			return h, unexpected(err)
		}

		n := binary.BigEndian.Uint64(b[:8])
		if n>>63 != 0 {
			return h, errLength
		}

		h.Length = int64(n)
	}

	if h.Masked {
		if _, err := io.ReadFull(r, h.Mask[:]); err != nil {
			return h, unexpected(err)
		}
	}

	switch h.Opcode {
	case OpContinuation, OpText, OpBinary:
	case OpClose, OpPing, OpPong:
		if !h.Fin || h.Length > maxControl {
			return h, errControlFrame
		}

	default:
		return h, fmt.Errorf("%w %#x", errReservedOpcode, h.Opcode)
	}

	return h, nil
}

// unexpected turns an end of input inside a frame into
// io.ErrUnexpectedEOF.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}

	return err
}

// MaskBytes masks (or unmasks) payload p in place with key.
func MaskBytes(key [4]byte, p []byte) {
	for i := range p {
		p[i] ^= key[i&3]
	}
}
