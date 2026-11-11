package convert

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/roachadam/mimicry/internal/rpc/pb"
)

// A WebSocket's stream carries its messages as records inside DATA, both
// ways: a type byte, the payload's length (u32, big-endian), the payload.
// END ends a direction, not a message, so a message's end must be in the
// bytes; records ignore DATA frame boundaries.
const (
	// RecordEnd is the core's last record on a WebSocket's stream, before
	// its END: the socket has closed, and the payload is the pb.Error
	// ReadMessage returns from then on (CODE_CLOSE after a close
	// handshake). The SDK never sends it.
	RecordEnd byte = 0
	// RecordText and RecordBinary are a message, typed by its
	// pb.MessageType, which is mimicry.MessageType (TextMessage,
	// BinaryMessage).
	RecordText   = byte(pb.MessageType_MESSAGE_TYPE_TEXT)
	RecordBinary = byte(pb.MessageType_MESSAGE_TYPE_BINARY)
)

// recordHeader is a record's type and length.
const recordHeader = 5

// maxRecord bounds a record's payload, both ways: mimicry's own bound on
// a message the server sends (internal/ws.MaxMessage), so a length the
// SDK sends fails before anything is allocated for it.
const maxRecord = 64 << 20

// WriteRecord writes one record to w in a single Write.
func WriteRecord(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > maxRecord {
		return Invalid("a %d-byte WebSocket message, over %d", len(payload), maxRecord)
	}

	buf := make([]byte, recordHeader+len(payload))

	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:], uint32(len(payload)))
	copy(buf[recordHeader:], payload)

	_, err := w.Write(buf)

	return err
}

// ReadRecord reads one record from r: io.EOF when r ends between
// records, io.ErrUnexpectedEOF inside one, an *InvalidError for a length
// over the bound.
func ReadRecord(r io.Reader) (typ byte, payload []byte, err error) {
	var h [recordHeader]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}

	n := binary.BigEndian.Uint32(h[1:])
	if n > maxRecord {
		return 0, nil, Invalid("a %d-byte WebSocket message, over %d", n, maxRecord)
	}

	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}

		return 0, nil, err
	}

	return h[0], payload, nil
}
