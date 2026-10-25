package quictrace

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// IsGreaseH3 reports a reserved HTTP/3 stream type, frame type or setting
// identifier (0x1f * N + 0x21; RFC 9114, sections 6.2.3, 7.2.8 and 7.2.4.1).
func IsGreaseH3(v uint64) bool {
	return v >= 0x21 && (v-0x21)%0x1f == 0
}

// StreamTypeName names a unidirectional stream type.
func StreamTypeName(t uint64) string {
	switch t {
	case StreamControl:
		return "control"
	case StreamPush:
		return "push"
	case StreamQPACKEncoder:
		return "qpack_encoder"
	case StreamQPACKDecoder:
		return "qpack_decoder"
	case 0x54:
		return "webtransport"
	}
	return ""
}

// FrameTypeName names an HTTP/3 frame type.
func FrameTypeName(t uint64) string {
	switch t {
	case FrameH3Data:
		return "DATA"
	case FrameH3Headers:
		return "HEADERS"
	case FrameH3CancelPush:
		return "CANCEL_PUSH"
	case FrameH3Settings:
		return "SETTINGS"
	case FrameH3PushPromise:
		return "PUSH_PROMISE"
	case FrameH3GoAway:
		return "GOAWAY"
	case 0x0c:
		return "ORIGIN"
	case FrameH3MaxPushID:
		return "MAX_PUSH_ID"
	case FrameH3PriorityUpdateRequest:
		return "PRIORITY_UPDATE"
	case FrameH3PriorityUpdatePush:
		return "PRIORITY_UPDATE_PUSH"
	}
	return ""
}

// HTTP/3 settings (RFC 9114 section 7.2.4.1, RFC 9204 section 5, RFC 9220,
// RFC 9297, draft-ietf-webtrans-http3).
const (
	SettingQPACKMaxTableCapacity = 0x01
	SettingMaxFieldSectionSize   = 0x06
	SettingQPACKBlockedStreams   = 0x07
	SettingEnableConnectProtocol = 0x08
	SettingH3Datagram            = 0x33
)

// SettingName names an HTTP/3 setting.
func SettingName(id uint64) string {
	switch id {
	case SettingQPACKMaxTableCapacity:
		return "QPACK_MAX_TABLE_CAPACITY"
	case SettingMaxFieldSectionSize:
		return "MAX_FIELD_SECTION_SIZE"
	case SettingQPACKBlockedStreams:
		return "QPACK_BLOCKED_STREAMS"
	case SettingEnableConnectProtocol:
		return "ENABLE_CONNECT_PROTOCOL"
	case SettingH3Datagram:
		return "H3_DATAGRAM"
	case 0xffd277:
		return "H3_DATAGRAM_DRAFT04"
	case 0x2b603742:
		return "ENABLE_WEBTRANSPORT"
	case 0xc671706a:
		return "WEBTRANSPORT_MAX_SESSIONS"
	}
	return ""
}

// ReadVarint reads a QUIC variable-length integer from r.
func ReadVarint(r io.ByteReader) (uint64, error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	n := 1 << (first >> 6)
	v := uint64(first & 0x3f)
	for range n - 1 {
		b, err := r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, err
		}
		v = v<<8 | uint64(b)
	}
	return v, nil
}

// AppendVarint appends v as a QUIC variable-length integer, in its
// shortest form.
func AppendVarint(dst []byte, v uint64) []byte {
	switch {
	case v < 1<<6:
		return append(dst, byte(v))
	case v < 1<<14:
		return append(dst, 0x40|byte(v>>8), byte(v)) //nolint:gosec // one byte at a time
	case v < 1<<30:
		return append(dst, 0x80|byte(v>>24), byte(v>>16), byte(v>>8), byte(v)) //nolint:gosec // one byte at a time
	}
	return append(dst, 0xc0|byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v)) //nolint:gosec // one byte at a time
}

// AppendFrame appends an HTTP/3 frame.
func AppendFrame(dst []byte, typ uint64, payload []byte) []byte {
	dst = AppendVarint(dst, typ)
	dst = AppendVarint(dst, uint64(len(payload)))
	return append(dst, payload...)
}

// maxFramePayload bounds the frames ReadFrame reads whole; larger DATA
// frames are skipped rather than kept.
const maxFramePayload = 1 << 20

// ReadFrame reads one HTTP/3 frame's type and length from r, then its
// payload: whole, except a DATA frame's, which is read and discarded (nil
// payload) since only its length matters. io.EOF means the stream ended
// between frames.
func ReadFrame(r *bufio.Reader) (typ, length uint64, payload []byte, err error) {
	if typ, err = ReadVarint(r); err != nil {
		return 0, 0, nil, err
	}
	if length, err = ReadVarint(r); err != nil {
		return 0, 0, nil, unexpected(err)
	}
	if typ == FrameH3Data || length > maxFramePayload {
		if _, err := r.Discard(int(min(length, 1<<62))); err != nil { //nolint:gosec // bounded
			return 0, 0, nil, unexpected(err)
		}
		return typ, length, nil, nil
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, 0, nil, unexpected(err)
	}
	return typ, length, payload, nil
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// ParseSettings reads a SETTINGS frame's payload.
func ParseSettings(payload []byte) ([]Setting, error) {
	var out []Setting
	r := &reader{
		b: payload,
	}
	for len(r.b) > 0 {
		id, value := r.varint(), r.varint()
		if r.err != nil {
			return out, fmt.Errorf("SETTINGS: %w", r.err)
		}
		out = append(out, Setting{
			ID:     id,
			Name:   SettingName(id),
			Grease: IsGreaseH3(id),
			Value:  value,
		})
	}
	return out, nil
}

// ParseControlFrame fills in what f's payload says, for the frame types
// whose payload is recorded: SETTINGS, GOAWAY, MAX_PUSH_ID, CANCEL_PUSH,
// PRIORITY_UPDATE.
func ParseControlFrame(f *H3Frame, payload []byte) error {
	switch f.Type {
	case FrameH3Settings:
		s, err := ParseSettings(payload)
		f.Settings = s
		return err
	case FrameH3GoAway, FrameH3MaxPushID, FrameH3CancelPush:
		v, n := varint(payload)
		if n == 0 || n != len(payload) {
			return fmt.Errorf("%s: malformed", FrameTypeName(f.Type))
		}
		f.ID = new(v)
	case FrameH3PriorityUpdateRequest, FrameH3PriorityUpdatePush:
		v, n := varint(payload)
		if n == 0 {
			return errors.New("PRIORITY_UPDATE: malformed")
		}
		f.ID, f.Priority = new(v), string(payload[n:])
	}
	return nil
}

// NewFrame is a frame of typ and length, named.
func NewFrame(millis int64, typ, length uint64) H3Frame {
	return H3Frame{
		Millis: millis,
		Type:   typ,
		Name:   FrameTypeName(typ),
		Grease: IsGreaseH3(typ),
		Length: length,
	}
}
