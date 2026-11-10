// Package wire is the framed, multiplexed protocol between mimicry-core and
// the language SDKs (decision 0069): frames, streams, per-stream credit,
// RESET and EVENT over any io.ReadWriteCloser. CALL, REPLY and EVENT
// payloads are opaque bytes here; the messages in them belong to the layer
// above. The rules, written for the SDKs, and the test vectors every
// implementation replays are in proto/testdata/wire/.
//
// Nothing grows without bound. A stream buffers at most Config.Window bytes
// of the peer's DATA (the credit it granted); each side has at most
// Config.MaxStreams streams open; the write queue holds at most one frame
// per stream from its caller plus one WINDOW and one RESET per stream; a
// frame carries at most MaxPayload bytes. No timer runs here: deadlines are
// the callers' contexts.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxPayload is the most a frame carries.
const MaxPayload = 64 << 10

// headerSize is a frame header: stream u32, type u8, flags u8, length u32,
// big-endian.
const headerSize = 10

type frameType uint8

const (
	typeCall frameType = 1 + iota
	typeReply
	typeData
	typeWindow
	typeReset
	typeEvent
)

func (t frameType) String() string {
	switch t {
	case typeCall:
		return "CALL"
	case typeReply:
		return "REPLY"
	case typeData:
		return "DATA"
	case typeWindow:
		return "WINDOW"
	case typeReset:
		return "RESET"
	case typeEvent:
		return "EVENT"
	}

	return fmt.Sprintf("type %d", uint8(t))
}

// flagEnd marks a direction's last frame. No other flag exists.
const flagEnd uint8 = 0x01

type header struct {
	stream uint32
	typ    frameType
	flags  uint8
	length uint32
}

// appendFrame appends a frame of payload to b.
func appendFrame(b []byte, stream uint32, typ frameType, flags uint8, payload []byte) []byte {
	b = binary.BigEndian.AppendUint32(b, stream)
	b = append(b, byte(typ), flags)
	b = binary.BigEndian.AppendUint32(b, uint32(len(payload)))

	return append(b, payload...)
}

// readFrame reads one frame from r into a fresh payload. It checks the
// header (type, then flags, then length) before reading the payload, so an
// oversized frame allocates nothing. io.EOF is a clean end between frames;
// ErrTruncated an end inside one.
func readFrame(r io.Reader, hb *[headerSize]byte) (header, []byte, error) {
	if _, err := io.ReadFull(r, hb[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return header{}, nil, fmt.Errorf("%w: %d-byte header cut short", ErrTruncated, headerSize)
		}

		return header{}, nil, err
	}

	h := header{
		stream: binary.BigEndian.Uint32(hb[0:4]),
		typ:    frameType(hb[4]),
		flags:  hb[5],
		length: binary.BigEndian.Uint32(hb[6:10]),
	}
	if h.typ < typeCall || h.typ > typeEvent {
		return h, nil, fmt.Errorf("%w: %d on stream %d", ErrUnknownType, hb[4], h.stream)
	}

	if h.flags&^flagEnd != 0 {
		return h, nil, fmt.Errorf("%w: %#02x on %s %d", ErrUnknownFlags, h.flags, h.typ, h.stream)
	}

	if h.length > MaxPayload {
		return h, nil, fmt.Errorf("%w: %s %d is %d bytes", ErrFrameTooLarge, h.typ, h.stream, h.length)
	}

	payload := make([]byte, h.length)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return h, nil, fmt.Errorf("%w: %s %d's %d-byte payload cut short", ErrTruncated, h.typ, h.stream, h.length)
		}

		return h, nil, err
	}

	return h, payload, nil
}

// Code is why a stream or the connection was reset. A RESET frame carries
// one as a big-endian u32; each has a sentinel error.
type Code uint32

// The codes. CANCEL and CREDIT_OVERRUN reset a stream; the others reset
// the connection (a RESET on stream 0) after a protocol violation.
const (
	CodeCancel Code = iota
	CodeStreamLimit
	CodeCreditOverrun
	CodeFrameTooLarge
	CodeUnknownType
	CodeUnknownFlags
	CodeStreamClosed
	CodeStreamNotOpen
	CodeWrongParity
	CodeBadFrame
)

// The errors a stream or connection fails with. Those with a Code are the
// protocol's; the rest are local. A failure the peer reported is a
// *RemoteError wrapping the code's error.
var (
	// ErrCanceled is a stream reset because its caller gave up (Close,
	// or the context of Open).
	ErrCanceled = errors.New("wire: stream canceled")
	// ErrStreamLimit is a peer opening more streams than MaxStreams.
	ErrStreamLimit = errors.New("wire: too many streams open")
	// ErrCreditOverrun is DATA beyond the credit granted: the stream is
	// reset, the connection survives.
	ErrCreditOverrun = errors.New("wire: DATA beyond the stream's credit")
	// ErrFrameTooLarge is a frame longer than MaxPayload.
	ErrFrameTooLarge = errors.New("wire: frame too large")
	// ErrUnknownType is a frame type the protocol has not defined.
	ErrUnknownType = errors.New("wire: unknown frame type")
	// ErrUnknownFlags is a flag bit other than END.
	ErrUnknownFlags = errors.New("wire: unknown frame flags")
	// ErrStreamClosed is a frame on a stream, or a direction of one, that
	// has ended, or an opening frame reusing an id.
	ErrStreamClosed = errors.New("wire: frame on a closed stream")
	// ErrStreamNotOpen is a frame on a stream never opened.
	ErrStreamNotOpen = errors.New("wire: frame on a stream never opened")
	// ErrWrongParity is a stream opened with the other side's parity.
	ErrWrongParity = errors.New("wire: stream id of the wrong parity")
	// ErrBadFrame is a frame the protocol does not allow where it came:
	// a type the sender's side never sends, a second REPLY, DATA before
	// REPLY, a malformed WINDOW or RESET, anything but EVENT or RESET on
	// stream 0.
	ErrBadFrame = errors.New("wire: frame not allowed here")
	// ErrTruncated is a connection that ended inside a frame.
	ErrTruncated = errors.New("wire: connection ended inside a frame")
	// ErrConnClosed is a connection closed, by either side, or failed
	// reading or writing.
	ErrConnClosed = errors.New("wire: connection closed")
)

var codeErrs = [...]error{
	CodeCancel:        ErrCanceled,
	CodeStreamLimit:   ErrStreamLimit,
	CodeCreditOverrun: ErrCreditOverrun,
	CodeFrameTooLarge: ErrFrameTooLarge,
	CodeUnknownType:   ErrUnknownType,
	CodeUnknownFlags:  ErrUnknownFlags,
	CodeStreamClosed:  ErrStreamClosed,
	CodeStreamNotOpen: ErrStreamNotOpen,
	CodeWrongParity:   ErrWrongParity,
	CodeBadFrame:      ErrBadFrame,
}

var codeNames = [...]string{
	CodeCancel:        "CANCEL",
	CodeStreamLimit:   "STREAM_LIMIT",
	CodeCreditOverrun: "CREDIT_OVERRUN",
	CodeFrameTooLarge: "FRAME_TOO_LARGE",
	CodeUnknownType:   "UNKNOWN_TYPE",
	CodeUnknownFlags:  "UNKNOWN_FLAGS",
	CodeStreamClosed:  "STREAM_CLOSED",
	CodeStreamNotOpen: "STREAM_NOT_OPEN",
	CodeWrongParity:   "WRONG_PARITY",
	CodeBadFrame:      "BAD_FRAME",
}

func (c Code) String() string {
	if int(c) < len(codeNames) {
		return codeNames[c]
	}

	return fmt.Sprintf("CODE_%d", uint32(c))
}

// err is the code's sentinel error, or nil for a code this side does not
// know.
func (c Code) err() error {
	if int(c) < len(codeErrs) {
		return codeErrs[c]
	}

	return nil
}

// codeOf is the code of a protocol error, false for any other error.
func codeOf(err error) (Code, bool) {
	for c, e := range codeErrs {
		if errors.Is(err, e) {
			return Code(c), true
		}
	}

	return 0, false
}

// RemoteError is a RESET the peer sent: of a stream, or of the whole
// connection (Stream 0). It unwraps to its code's error.
type RemoteError struct {
	Stream uint32
	Code   Code
}

func (e *RemoteError) Error() string {
	if e.Stream == 0 {
		return "wire: the peer reset the connection: " + e.Code.String()
	}

	return fmt.Sprintf("wire: the peer reset stream %d: %s", e.Stream, e.Code)
}

func (e *RemoteError) Unwrap() error {
	return e.Code.err()
}
