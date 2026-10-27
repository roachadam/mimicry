package quictrace

import (
	"encoding/hex"
	"errors"
	"fmt"
)

// varint reads a QUIC variable-length integer (RFC 9000, section 16) from
// the start of b, returning it and its length; n is 0 if b is too short.
func varint(b []byte) (v uint64, n int) {
	if len(b) == 0 {
		return 0, 0
	}

	n = 1 << (b[0] >> 6)
	if len(b) < n {
		return 0, 0
	}

	v = uint64(b[0] & 0x3f)
	for _, c := range b[1:n] {
		v = v<<8 | uint64(c)
	}

	return v, n
}

// reader reads a frame's fields.
type reader struct {
	b   []byte
	err error
}

var errTruncated = errors.New("truncated")

func (r *reader) varint() uint64 {
	if r.err != nil {
		return 0
	}

	v, n := varint(r.b)
	if n == 0 {
		r.err = errTruncated
		return 0
	}

	r.b = r.b[n:]

	return v
}

func (r *reader) bytes(n uint64) []byte {
	if r.err != nil {
		return nil
	}

	if uint64(len(r.b)) < n {
		r.err = errTruncated
		return nil
	}

	out := r.b[:n]

	r.b = r.b[n:]

	return out
}

func (r *reader) byte() byte {
	b := r.bytes(1)
	if b == nil {
		return 0
	}

	return b[0]
}

// cryptoData is the data of a CRYPTO frame, for reassembly.
type cryptoData struct {
	offset uint64
	data   []byte
}

// parseFrames reads a packet's frames. It returns those read before any
// error, and the CRYPTO frames' data.
func parseFrames(payload []byte) ([]Frame, []cryptoData, error) {
	var (
		frames []Frame
		crypto []cryptoData
	)

	r := &reader{
		b: payload,
	}

	for len(r.b) > 0 {
		code := r.varint()
		if r.err != nil {
			return frames, crypto, r.err
		}

		f := Frame{
			Code: code,
		}
		switch {
		case code == 0x00:
			n := 1
			for len(r.b) > 0 && r.b[0] == 0 {
				r.b = r.b[1:]
				n++
			}

			f.Type, f.Count = FramePadding, n

		case code == 0x01:
			f.Type = FramePing
		case code == 0x02 || code == 0x03:
			f.Type = FrameAck
			f.Largest = r.varint()
			f.Delay = r.varint()

			count := r.varint()
			first := r.varint()

			if r.err != nil || first > f.Largest {
				return frames, crypto, fmt.Errorf("ACK: bad first range")
			}

			largest := f.Largest
			smallest := largest - first

			f.Ranges = append(f.Ranges, [2]uint64{smallest, largest})

			for range count {
				gap, length := r.varint(), r.varint()
				if r.err != nil || gap+2 > smallest || gap+2+length > smallest {
					return frames, crypto, fmt.Errorf("ACK: bad range")
				}

				largest = smallest - gap - 2
				smallest = largest - length
				f.Ranges = append(f.Ranges, [2]uint64{smallest, largest})
			}

			if code == 0x03 {
				f.ECN = []uint64{r.varint(), r.varint(), r.varint()}
			}

		case code == 0x04:
			f.Type = FrameResetStream
			f.Stream, f.ErrorCode = new(r.varint()), new(r.varint())
			f.FinalSize = r.varint()

		case code == 0x05:
			f.Type = FrameStopSending
			f.Stream, f.ErrorCode = new(r.varint()), new(r.varint())

		case code == 0x06:
			f.Type = FrameCrypto
			f.Offset = r.varint()
			f.Length = r.varint()

			if data := r.bytes(f.Length); r.err == nil {
				crypto = append(crypto, cryptoData{
					offset: f.Offset,
					data:   data,
				})
			}

		case code == 0x07:
			f.Type = FrameNewToken
			f.Length = r.varint()
			r.bytes(f.Length)

		case code >= 0x08 && code <= 0x0f:
			f.Type = FrameStream
			f.Stream = new(r.varint())

			if code&0x04 != 0 {
				f.Offset = r.varint()
			}

			if code&0x02 != 0 {
				f.Length = r.varint()
			} else {
				f.Length = uint64(len(r.b))
			}

			f.Fin = code&0x01 != 0
			r.bytes(f.Length)

		case code == 0x10:
			f.Type, f.Max = FrameMaxData, r.varint()
		case code == 0x11:
			f.Type = FrameMaxStreamData
			f.Stream, f.Max = new(r.varint()), r.varint()

		case code == 0x12 || code == 0x13:
			f.Type, f.Max = FrameMaxStreams, r.varint()
		case code == 0x14:
			f.Type, f.Max = FrameDataBlocked, r.varint()
		case code == 0x15:
			f.Type = FrameStreamDataBlocked
			f.Stream, f.Max = new(r.varint()), r.varint()

		case code == 0x16 || code == 0x17:
			f.Type, f.Max = FrameStreamsBlocked, r.varint()
		case code == 0x18:
			f.Type = FrameNewConnectionID
			f.Sequence = r.varint()
			f.RetirePriorTo = r.varint()

			cidLen := r.byte()

			f.CIDLength = int(cidLen)
			r.bytes(uint64(cidLen))
			r.bytes(16)

		case code == 0x19:
			f.Type, f.Sequence = FrameRetireConnectionID, r.varint()
		case code == 0x1a || code == 0x1b:
			f.Type = FramePathChallenge
			if code == 0x1b {
				f.Type = FramePathResponse
			}

			f.Data = hex.EncodeToString(r.bytes(8))

		case code == 0x1c || code == 0x1d:
			f.Type = FrameConnectionClose
			f.ErrorCode = new(r.varint())

			if code == 0x1c {
				f.FrameType = r.varint()
			}

			f.Reason = string(r.bytes(r.varint()))

		case code == 0x1e:
			f.Type = FrameHandshakeDone
		case code == 0x1f:
			f.Type = FrameImmediateAck
		case code == 0x30 || code == 0x31:
			f.Type = FrameDatagram
			if code == 0x31 {
				f.Length = r.varint()
			} else {
				f.Length = uint64(len(r.b))
			}

			r.bytes(f.Length)

		case code == 0xaf:
			f.Type = FrameAckFrequency
			f.Sequence = r.varint()
			f.Threshold = r.varint()
			f.MaxAckDelay = r.varint()
			f.Reordering = r.varint()

		default:
			return frames, crypto, fmt.Errorf("unknown frame type %#x", code)
		}

		if r.err != nil {
			return frames, crypto, fmt.Errorf("%s: %w", f.Type, r.err)
		}

		frames = append(frames, f)
	}

	return frames, crypto, nil
}
