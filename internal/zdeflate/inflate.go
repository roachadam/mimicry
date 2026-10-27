package zdeflate

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
)

// ErrTooLarge is a message that inflates to more than the limit given.
var ErrTooLarge = errors.New("zdeflate: inflated message too large")

// inflateTail follows each message into the inflater: the sync flush's
// 00 00 ff ff that permessage-deflate cuts, then an empty final stored
// block, so the stream ends where the message does. A message that ends
// in a final block of its own (RFC 7692, section 7.2.3.5) leaves the tail
// unread.
var inflateTail = []byte{
	0x00, 0x00, 0xff, 0xff,
	0x01, 0x00, 0x00, 0xff, 0xff,
}

// dictSize is the most a peer's message can refer back to: a 32 KiB window.
const dictSize = 1 << maxWBits

// Reader inflates the messages of one permessage-deflate socket, each with
// the last dictSize bytes of the earlier ones as its dictionary: all a peer
// that keeps its context can refer to. It is not safe for concurrent use.
type Reader struct {
	fr   io.ReadCloser
	src  bytes.Reader
	in   []byte
	dict []byte
}

// NewReader returns a Reader for a socket's first message.
func NewReader() *Reader {
	return &Reader{}
}

// Message returns the payload of one compressed message p (its frames'
// payloads joined), or ErrTooLarge if it inflates to more than limit
// bytes.
func (r *Reader) Message(p []byte, limit int) ([]byte, error) {
	r.in = append(append(r.in[:0], p...), inflateTail...)
	r.src.Reset(r.in)
	if r.fr == nil {
		r.fr = flate.NewReaderDict(&r.src, r.dict)
	} else if err := r.fr.(flate.Resetter).Reset(&r.src, r.dict); err != nil {
		return nil, fmt.Errorf("zdeflate: %w", err)
	}

	var out bytes.Buffer
	if _, err := out.ReadFrom(io.LimitReader(r.fr, int64(limit))); err != nil {
		return nil, fmt.Errorf("zdeflate: %w", err)
	}
	var one [1]byte
	switch n, err := io.ReadFull(r.fr, one[:]); {
	case n != 0:
		return nil, ErrTooLarge
	case !errors.Is(err, io.EOF):
		return nil, fmt.Errorf("zdeflate: %w", err)
	}

	msg := out.Bytes()
	if len(msg) >= dictSize {
		r.dict = append(r.dict[:0], msg[len(msg)-dictSize:]...)
	} else {
		if keep := dictSize - len(msg); len(r.dict) > keep {
			r.dict = r.dict[:copy(r.dict, r.dict[len(r.dict)-keep:])]
		}
		r.dict = append(r.dict, msg...)
	}
	return msg, nil
}
