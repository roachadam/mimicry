package h3

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"

	"github.com/roachadam/mimicry/internal/quic/quicwire"
	"github.com/roachadam/mimicry/profile"
)

// HTTP/3 frame types (RFC 9114, section 7.2; PRIORITY_UPDATE, RFC 9218).
const (
	frameData           = 0x00
	frameHeaders        = 0x01
	frameCancelPush     = 0x03
	frameSettings       = 0x04
	framePushPromise    = 0x05
	frameGoAway         = 0x07
	frameMaxPushID      = 0x0d
	framePriorityUpdate = 0xf0700
)

// Unidirectional stream types (RFC 9114, section 6.2; RFC 9204, section
// 4.2).
const (
	streamControl      = 0x00
	streamPush         = 0x01
	streamQPACKEncoder = 0x02
	streamQPACKDecoder = 0x03
)

// HTTP/3 and QPACK error codes (RFC 9114, section 8.1; RFC 9204, section
// 6).
const (
	codeNoError                  = 0x100
	codeGeneralProtocolError     = 0x101
	codeInternalError            = 0x102
	codeStreamCreationError      = 0x103
	codeClosedCriticalStream     = 0x104
	codeFrameUnexpected          = 0x105
	codeFrameError               = 0x106
	codeIDError                  = 0x108
	codeSettingsError            = 0x109
	codeMissingSettings          = 0x10a
	codeRequestRejected          = 0x10b
	codeRequestCancelled         = 0x10c
	codeMessageError             = 0x10e
	codeQPACKDecompressionFailed = 0x200
	codeQPACKEncoderStreamError  = 0x201
	codeQPACKDecoderStreamError  = 0x202
)

// connError is an error that ends the connection with an HTTP/3 code.
type connError struct {
	code uint64
	msg  string
}

func (e *connError) Error() string {
	return fmt.Sprintf("h3: connection error 0x%x: %s", e.code, e.msg)
}

// appendFrame appends a frame of type typ with payload.
func appendFrame(dst []byte, typ uint64, payload []byte) []byte {
	dst = quicwire.AppendVarint(dst, typ)
	dst = quicwire.AppendVarint(dst, uint64(len(payload)))
	return append(dst, payload...)
}

// maxVarint is the largest variable-length integer (RFC 9000, 16).
const maxVarint = 1<<62 - 1

// greaseID is a reserved identifier 0x1f * N + 0x21 (RFC 9114, sections
// 7.2.4.1, 7.2.8 and 6.2.3), N drawn as g says.
func greaseID(g *profile.H3Grease) uint64 {
	if g.IDBits > 0 && g.IDBits < 64 {
		return 0x1f*rand.Uint64N(1<<g.IDBits) + 0x21 //nolint:gosec // GREASE needs no secrecy
	}
	return 0x1f*rand.Uint64N((maxVarint-0x21)/0x1f+1) + 0x21 //nolint:gosec // GREASE needs no secrecy
}

// controlStream is the control stream's opening: its type, the SETTINGS
// frame (the profile's settings, then a GREASE setting where it sends
// one) and the GREASE frame where it sends one.
func controlStream(h *profile.HTTP3) []byte {
	var settings []byte
	for _, s := range h.Settings {
		settings = quicwire.AppendVarint(settings, s.ID)
		settings = quicwire.AppendVarint(settings, s.Value)
	}
	if g := h.GreaseSetting; g != nil {
		settings = quicwire.AppendVarint(settings, greaseID(g))
		value := uint64(0)
		if g.ValueBits > 0 && g.ValueBits < 63 {
			value = rand.Uint64N(1 << g.ValueBits) //nolint:gosec // GREASE needs no secrecy
		}
		settings = quicwire.AppendVarint(settings, value)
	}
	b := quicwire.AppendVarint(nil, streamControl)
	b = appendFrame(b, frameSettings, settings)
	if g := h.GreaseFrame; g != nil {
		payload := make([]byte, rand.IntN(g.MaxLength+1)) //nolint:gosec // GREASE needs no secrecy
		for i := range payload {
			payload[i] = byte(rand.Uint32()) //nolint:gosec // GREASE needs no secrecy
		}
		b = appendFrame(b, greaseID(g), payload)
	}
	return b
}

// priorityUpdate is a PRIORITY_UPDATE frame for request stream id with
// priority field value v (RFC 9218, section 7.1).
func priorityUpdate(id uint64, v string) []byte {
	payload := quicwire.AppendVarint(nil, id)
	payload = append(payload, v...)
	return appendFrame(nil, framePriorityUpdate, payload)
}

// readVarint reads a variable-length integer (RFC 9000, 16).
func readVarint(r io.ByteReader) (uint64, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	n := 1 << (b >> 6)
	v := uint64(b & 0x3f)
	for range n - 1 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, unexpectedEOF(err)
		}
		v = v<<8 | uint64(b)
	}
	return v, nil
}

// unexpectedEOF turns a clean end in the middle of something into
// io.ErrUnexpectedEOF.
func unexpectedEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// readFrameHeader reads a frame's type and length. A stream that ends
// before a frame starts returns io.EOF.
func readFrameHeader(r io.ByteReader) (typ, length uint64, err error) {
	if typ, err = readVarint(r); err != nil {
		return 0, 0, err
	}
	if length, err = readVarint(r); err != nil {
		return 0, 0, unexpectedEOF(err)
	}
	return typ, length, nil
}

// readPayload reads a frame payload of length bytes, at most limit.
func readPayload(r io.Reader, length, limit uint64) ([]byte, error) {
	if length > limit {
		return nil, &connError{
			code: codeFrameError,
			msg:  fmt.Sprintf("frame of %d bytes, above %d", length, limit),
		}
	}
	b := make([]byte, length)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, unexpectedEOF(err)
	}
	return b, nil
}

// skip discards n bytes of r.
func skip(r io.Reader, n uint64) error {
	_, err := io.CopyN(io.Discard, r, int64(min(n, maxVarint)))
	return unexpectedEOF(err)
}
