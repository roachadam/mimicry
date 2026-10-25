package quictrace

import (
	"github.com/roachadam/mimicry/profile"
)

// Conn is everything a client sent on one QUIC connection: its datagrams
// as they arrived, packet by packet and frame by frame, the ClientHello its
// Initial packets carried, and, if the handshake completed, its HTTP/3.
type Conn struct {
	// Millis is when its first datagram arrived, since the capture server
	// started serving the page (the same clock as the page's other
	// connections).
	Millis int64 `json:"ms"`
	// Host is the ClientHello's server_name.
	Host string `json:"host"`
	// Version is the QUIC version of the client's first packet, hex.
	Version string `json:"version"`
	// ClientHello is the ClientHello message its Initial packets' CRYPTO
	// frames carried, reassembled, hex; Hello is it parsed (as a TCP
	// connection's) and HelloError why it could not be.
	ClientHello string       `json:"client_hello,omitempty"`
	Hello       *profile.TLS `json:"hello,omitempty"`
	HelloError  string       `json:"hello_error,omitempty"`
	// TransportParameters are the ClientHello's quic_transport_parameters
	// (extension 0x39), in the order sent.
	TransportParameters []TransportParameter `json:"transport_parameters,omitempty"`
	// Resumed reports a ClientHello offering an earlier session
	// (pre_shared_key); EarlyData that the client sent 0-RTT packets.
	Resumed   bool `json:"resumed,omitempty"`
	EarlyData bool `json:"early_data,omitempty"`
	// Datagrams are every UDP datagram the client sent on the connection,
	// in order of arrival.
	Datagrams []Datagram `json:"datagrams"`
	// H3 is what the client sent at the HTTP/3 layer; nil if the
	// handshake never completed (a client that rejected the certificate).
	H3 *H3 `json:"h3,omitempty"`
	// Close is how the connection ended, if it had by the time the page
	// was taken.
	Close *Close `json:"close,omitempty"`
}

// Datagram is one UDP datagram the client sent.
type Datagram struct {
	// Micros since the connection's first datagram.
	Micros int64 `json:"us"`
	Size   int   `json:"size"`
	// Packets are the QUIC packets coalesced in it.
	Packets []Packet `json:"packets"`
	// Trailing counts bytes after the last packet that start none (a
	// client padding the datagram rather than the packet).
	Trailing int `json:"trailing,omitempty"`
}

// Packet types.
const (
	PacketInitial   = "Initial"
	Packet0RTT      = "0-RTT"
	PacketHandshake = "Handshake"
	PacketRetry     = "Retry"
	Packet1RTT      = "1-RTT"
	PacketVN        = "VersionNegotiation"
)

// Packet is one QUIC packet: its header as sent, and its frames once its
// protection is removed. Connection ID values are random per connection;
// only their lengths are kept.
type Packet struct {
	Type string `json:"type"`
	// FirstByte is the header's first byte with header protection removed:
	// header form, fixed bit, long packet type or spin bit, reserved bits,
	// key phase, packet number length. Zero bits where the packet could not
	// be opened.
	FirstByte uint8 `json:"first_byte"`
	// Version of a long header packet, hex.
	Version    string `json:"version,omitempty"`
	DCIDLength int    `json:"dcid_length"`
	// SCIDLength, TokenLength (Initial packets) and Length (the long
	// header's Length field) are nil for packets without them.
	SCIDLength  *int `json:"scid_length,omitempty"`
	TokenLength *int `json:"token_length,omitempty"`
	Length      *int `json:"length,omitempty"`
	// PNLength and PN are the packet number's encoded length and full
	// value.
	PNLength int    `json:"pn_length,omitempty"`
	PN       uint64 `json:"pn"`
	// Size is the whole packet's, header and AEAD tag included.
	Size   int     `json:"size"`
	Frames []Frame `json:"frames,omitempty"`
	// Error is why the payload could not be read: no keys for its packet
	// number space, or failed authentication.
	Error string `json:"error,omitempty"`
}

// Frame types, as named in RFC 9000 section 19 and the extensions
// browsers use (DATAGRAM, RFC 9221; ACK_FREQUENCY and IMMEDIATE_ACK,
// draft-ietf-quic-ack-frequency).
const (
	FramePadding            = "PADDING"
	FramePing               = "PING"
	FrameAck                = "ACK"
	FrameResetStream        = "RESET_STREAM"
	FrameStopSending        = "STOP_SENDING"
	FrameCrypto             = "CRYPTO"
	FrameNewToken           = "NEW_TOKEN"
	FrameStream             = "STREAM"
	FrameMaxData            = "MAX_DATA"
	FrameMaxStreamData      = "MAX_STREAM_DATA"
	FrameMaxStreams         = "MAX_STREAMS"
	FrameDataBlocked        = "DATA_BLOCKED"
	FrameStreamDataBlocked  = "STREAM_DATA_BLOCKED"
	FrameStreamsBlocked     = "STREAMS_BLOCKED"
	FrameNewConnectionID    = "NEW_CONNECTION_ID"
	FrameRetireConnectionID = "RETIRE_CONNECTION_ID"
	FramePathChallenge      = "PATH_CHALLENGE"
	FramePathResponse       = "PATH_RESPONSE"
	FrameConnectionClose    = "CONNECTION_CLOSE"
	FrameHandshakeDone      = "HANDSHAKE_DONE"
	FrameDatagram           = "DATAGRAM"
	FrameAckFrequency       = "ACK_FREQUENCY"
	FrameImmediateAck       = "IMMEDIATE_ACK"
)

// Frame is one QUIC frame of a packet. Only the fields of its type are
// set.
type Frame struct {
	Type string `json:"type"`
	// Code is the frame type as sent: a STREAM frame's carries its OFF, LEN
	// and FIN bits, an ACK's whether it has ECN counts, a MAX_STREAMS or
	// STREAMS_BLOCKED frame's whether it is for bidirectional streams, a
	// CONNECTION_CLOSE's whether it is the application's.
	Code uint64 `json:"code"`
	// Count is how many PADDING bytes a run of them has (one Frame per
	// run).
	Count int `json:"count,omitempty"`
	// Stream of STREAM, RESET_STREAM, STOP_SENDING, MAX_STREAM_DATA and
	// STREAM_DATA_BLOCKED frames.
	Stream *uint64 `json:"stream,omitempty"`
	// Offset and Length of a CRYPTO or STREAM frame's data (Length also of
	// NEW_TOKEN's token and DATAGRAM's payload); Fin of a STREAM frame.
	Offset uint64 `json:"offset,omitempty"`
	Length uint64 `json:"length,omitempty"`
	Fin    bool   `json:"fin,omitempty"`
	// Largest, Delay (the ACK Delay field as sent, before scaling by
	// ack_delay_exponent), Ranges (each [smallest, largest], from the
	// largest down) and ECN (ECT0, ECT1, ECN-CE counts) of an ACK.
	Largest uint64      `json:"largest,omitempty"`
	Delay   uint64      `json:"delay,omitempty"`
	Ranges  [][2]uint64 `json:"ranges,omitempty"`
	ECN     []uint64    `json:"ecn,omitempty"`
	// Max is the limit of MAX_DATA, MAX_STREAM_DATA, MAX_STREAMS and of
	// the BLOCKED frames.
	Max uint64 `json:"max,omitempty"`
	// ErrorCode of RESET_STREAM, STOP_SENDING and CONNECTION_CLOSE;
	// FinalSize of RESET_STREAM; FrameType and Reason of CONNECTION_CLOSE.
	ErrorCode *uint64 `json:"error_code,omitempty"`
	FinalSize uint64  `json:"final_size,omitempty"`
	FrameType uint64  `json:"frame_type,omitempty"`
	Reason    string  `json:"reason,omitempty"`
	// Sequence, RetirePriorTo and CIDLength of NEW_CONNECTION_ID
	// (Sequence also of RETIRE_CONNECTION_ID and ACK_FREQUENCY).
	Sequence      uint64 `json:"seq,omitempty"`
	RetirePriorTo uint64 `json:"retire_prior_to,omitempty"`
	CIDLength     int    `json:"cid_length,omitempty"`
	// Data of PATH_CHALLENGE and PATH_RESPONSE, hex.
	Data string `json:"data,omitempty"`
	// Threshold (ack-eliciting threshold), MaxAckDelay (requested, in
	// microseconds) and Reordering threshold of ACK_FREQUENCY.
	Threshold   uint64 `json:"threshold,omitempty"`
	MaxAckDelay uint64 `json:"max_ack_delay,omitempty"`
	Reordering  uint64 `json:"reordering,omitempty"`
}

// TransportParameter is one QUIC transport parameter (RFC 9000, section
// 18) as the client sent it.
type TransportParameter struct {
	ID uint64 `json:"id"`
	// Name is the parameter's name, for those known; Grease marks a
	// reserved identifier (31 * N + 27).
	Name   string `json:"name,omitempty"`
	Grease bool   `json:"grease,omitempty"`
	Length int    `json:"length"`
	// Value of a parameter whose value is one variable-length integer.
	Value *uint64 `json:"value,omitempty"`
	// Hex of any other value, except connection IDs (random: their length
	// is kept).
	Hex string `json:"hex,omitempty"`
	// Versions of version_information (RFC 9368): the chosen version, then
	// the available ones, hex.
	Versions []string `json:"versions,omitempty"`
}

// Close is how a QUIC connection ended.
type Close struct {
	// By is "client" (its CONNECTION_CLOSE), "server", or "idle" (the idle
	// timeout expired; RFC 9000 closes silently).
	By string `json:"by"`
	// Millis since the server started serving the page.
	Millis int64 `json:"ms"`
	// Error is the close's error as the server's QUIC stack reported it.
	Error string `json:"error,omitempty"`
}

// H3 is what a client sent at the HTTP/3 layer of one connection (RFC
// 9114), as the server read it from each stream.
type H3 struct {
	// Streams are the client's unidirectional streams in the order they
	// arrived: control, QPACK encoder and decoder, and any other (GREASE)
	// types.
	Streams []H3Stream `json:"streams"`
	// Requests are its request streams in the order they arrived.
	Requests []H3Request `json:"requests"`
}

// Unidirectional stream types (RFC 9114, section 6.2; RFC 9204, section
// 4.2).
const (
	StreamControl      = 0x00
	StreamPush         = 0x01
	StreamQPACKEncoder = 0x02
	StreamQPACKDecoder = 0x03
)

// H3Stream is one unidirectional stream the client opened.
type H3Stream struct {
	// Millis is when it arrived, since the server started serving the
	// page.
	Millis int64  `json:"ms"`
	ID     uint64 `json:"id"`
	Type   uint64 `json:"type"`
	// Name is the type's name; Grease marks a reserved type (0x1f * N +
	// 0x21).
	Name   string `json:"name,omitempty"`
	Grease bool   `json:"grease,omitempty"`
	// Bytes is how much the stream carried after its type.
	Bytes int `json:"bytes"`
	// Frames of a control stream; Encoder and Decoder are a QPACK encoder
	// or decoder stream's instructions.
	Frames  []H3Frame            `json:"frames,omitempty"`
	Encoder []EncoderInstruction `json:"encoder,omitempty"`
	Decoder []DecoderInstruction `json:"decoder,omitempty"`
	// End is how the client ended it: "FIN" or "RESET <code>"; "" while
	// open.
	End string `json:"end,omitempty"`
	// Error is why the server stopped reading it: bytes it could not
	// parse.
	Error string `json:"error,omitempty"`
}

// HTTP/3 frame types (RFC 9114, section 7.2; PRIORITY_UPDATE, RFC 9218).
const (
	FrameH3Data                  = 0x00
	FrameH3Headers               = 0x01
	FrameH3CancelPush            = 0x03
	FrameH3Settings              = 0x04
	FrameH3PushPromise           = 0x05
	FrameH3GoAway                = 0x07
	FrameH3MaxPushID             = 0x0d
	FrameH3PriorityUpdateRequest = 0xf0700
	FrameH3PriorityUpdatePush    = 0xf0701
)

// H3Frame is one HTTP/3 frame.
type H3Frame struct {
	// Millis is when it was read, since the server started serving the
	// page.
	Millis int64  `json:"ms"`
	Type   uint64 `json:"type"`
	// Name is the type's name; Grease marks a reserved type (0x1f * N +
	// 0x21).
	Name   string `json:"name,omitempty"`
	Grease bool   `json:"grease,omitempty"`
	Length uint64 `json:"length"`
	// Settings of a SETTINGS frame, in the order sent.
	Settings []Setting `json:"settings,omitempty"`
	// ID of GOAWAY, MAX_PUSH_ID and CANCEL_PUSH, and the prioritized
	// element of PRIORITY_UPDATE, whose Priority Field Value is Priority.
	ID       *uint64 `json:"id,omitempty"`
	Priority string  `json:"priority,omitempty"`
}

// Setting is one HTTP/3 setting.
type Setting struct {
	ID     uint64 `json:"id"`
	Name   string `json:"name,omitempty"`
	Grease bool   `json:"grease,omitempty"`
	Value  uint64 `json:"value"`
}

// H3Request is one request stream.
type H3Request struct {
	// Millis is when the stream's first frame was read, since the server
	// started serving the page.
	Millis int64  `json:"ms"`
	Stream uint64 `json:"stream"`
	// Method and Path are the request's, for finding it.
	Method string `json:"method"`
	Path   string `json:"path"`
	// Frames are its frames in order (HEADERS, DATA, trailers, others).
	Frames []H3Frame `json:"frames"`
	// Block is the HEADERS frame's payload, the encoded field section,
	// hex; Section is it decoded.
	Block   string  `json:"block"`
	Section Section `json:"section"`
	// Blocked reports that the section referred to dynamic table entries
	// the encoder stream had not yet delivered, so decoding waited.
	Blocked bool `json:"blocked,omitempty"`
	// Trailers is a trailing HEADERS frame's section.
	Trailers *Section `json:"trailers,omitempty"`
	// Uploaded is how many DATA bytes the request carried.
	Uploaded int `json:"uploaded,omitempty"`
	// End is how the client ended the stream: "FIN" or "RESET <code>".
	End string `json:"end,omitempty"`
	// Error is why the server stopped reading it.
	Error string `json:"error,omitempty"`
}
