// Package quictrace records what a client sends over QUIC and HTTP/3 below
// the decoded requests: datagrams, packets, frames, the ClientHello,
// transport parameters, HTTP/3 frames and QPACK instructions.
//
// Initial packets are decrypted with keys derived from the client's DCID
// (RFC 9001 section 5.2); other packets with the server's TLS key log. The
// server's datagrams are recorded too, for scenarios the client reacts to.
package quictrace

import (
	"time"

	"github.com/roachadam/mimicry/profile"
)

// Conn is everything a client sent on one QUIC connection, plus what the
// server sent it (for scenarios such as version negotiation and Retry).
type Conn struct {
	// Millis is when its first datagram arrived, since the capture server
	// started serving the page.
	Millis int64 `json:"ms"`
	// Start is Millis on the recording process's clock; not stored.
	Start time.Time `json:"-"`
	// Addr is the client address it started on; not stored.
	Addr string `json:"-"`
	// Host is the ClientHello's server_name.
	Host string `json:"host"`
	// Version is the client's first packet's QUIC version, hex;
	// NegotiatedVersion is set only if the handshake finished in another
	// (RFC 9368).
	Version           string `json:"version"`
	NegotiatedVersion string `json:"negotiatedVersion,omitempty"`
	// ClientHello is the reassembled ClientHello, hex; Hello is it parsed
	// and HelloError why it could not be.
	ClientHello string       `json:"clientHello,omitempty"`
	Hello       *profile.TLS `json:"hello,omitempty"`
	HelloError  string       `json:"helloError,omitempty"`
	// TransportParameters are the ClientHello's quic_transport_parameters
	// (extension 0x39), in the order sent; ServerTransportParameters the
	// server's, from its EncryptedExtensions.
	TransportParameters       []TransportParameter `json:"transportParameters,omitempty"`
	ServerTransportParameters []TransportParameter `json:"serverTransportParameters,omitempty"`
	// Resumed reports a ClientHello offering an earlier session
	// (pre_shared_key); EarlyData that the client sent 0-RTT packets.
	Resumed   bool `json:"resumed,omitempty"`
	EarlyData bool `json:"earlyData,omitempty"`
	// Datagrams are every UDP datagram the client sent on the connection,
	// in order of arrival, from every address it used.
	Datagrams []Datagram `json:"datagrams"`
	// Server are the datagrams the server sent to the connection's
	// addresses. Its 1-RTT packets only have header protection removed.
	Server []Datagram `json:"server,omitempty"`
	// H3 is nil if the handshake never completed (no client Finished).
	H3 *H3 `json:"h3,omitempty"`
	// Close is how the connection ended, if it had by capture time.
	Close *Close `json:"close,omitempty"`
}

// Datagram is one UDP datagram the client (or, in Conn.Server, the server)
// sent.
type Datagram struct {
	// Micros since the connection's first client datagram.
	Micros int64 `json:"us"`
	Size   int   `json:"size"`
	// Path indexes the client addresses in order of first use; nonzero
	// after migration (RFC 9000, section 9).
	Path int `json:"path,omitempty"`
	// Packets are the QUIC packets coalesced in it.
	Packets []Packet `json:"packets"`
	// Trailing counts bytes after the last packet (padding outside packets).
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

// Packet is one QUIC packet: its header and, once unprotected, its frames.
// Connection IDs are random, so only their lengths are kept.
type Packet struct {
	Type string `json:"type"`
	// FirstByte is the header's first byte with header protection removed
	// (protected bits zero if the packet could not be opened).
	FirstByte uint8 `json:"firstByte"`
	// Version of a long header packet, hex.
	Version    string `json:"version,omitempty"`
	DCIDLength int    `json:"dcidLength"`
	// SCIDLength, TokenLength and Length are nil for packets without them.
	SCIDLength  *int `json:"scidLength,omitempty"`
	TokenLength *int `json:"tokenLength,omitempty"`
	Length      *int `json:"length,omitempty"`
	// Versions are a Version Negotiation packet's versions, hex.
	Versions []string `json:"versions,omitempty"`
	// PNLength and PN are the packet number's encoded length and value.
	PNLength int    `json:"pnLength,omitempty"`
	PN       uint64 `json:"pn"`
	// Size is the whole packet's, header and AEAD tag included.
	Size   int     `json:"size"`
	Frames []Frame `json:"frames,omitempty"`
	// Error is why the payload could not be read (no keys, failed
	// authentication, or a bad Retry integrity tag).
	Error string `json:"error,omitempty"`
}

// Frame types (RFC 9000 section 19; DATAGRAM, RFC 9221; ACK_FREQUENCY and
// IMMEDIATE_ACK, draft-ietf-quic-ack-frequency).
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

// Frame is one QUIC frame. Only the fields of its type are set.
type Frame struct {
	Type string `json:"type"`
	// Code is the frame type as sent, including flag bits (STREAM's
	// OFF/LEN/FIN, ACK's ECN, and so on).
	Code uint64 `json:"code"`
	// Count is the length of a PADDING run (one Frame per run).
	Count int `json:"count,omitempty"`
	// Stream is the stream ID of stream-scoped frames.
	Stream *uint64 `json:"stream,omitempty"`
	// Offset and Length of CRYPTO or STREAM data (Length also of NEW_TOKEN
	// and DATAGRAM); Fin of STREAM.
	Offset uint64 `json:"offset,omitempty"`
	Length uint64 `json:"length,omitempty"`
	Fin    bool   `json:"fin,omitempty"`
	// ACK fields. Delay is unscaled; Ranges are [smallest, largest] from
	// the largest down; ECN is ECT0, ECT1, ECN-CE.
	Largest uint64      `json:"largest,omitempty"`
	Delay   uint64      `json:"delay,omitempty"`
	Ranges  [][2]uint64 `json:"ranges,omitempty"`
	ECN     []uint64    `json:"ecn,omitempty"`
	// Max is the limit of MAX_* and *_BLOCKED frames.
	Max uint64 `json:"max,omitempty"`
	// ErrorCode of RESET_STREAM, STOP_SENDING and CONNECTION_CLOSE;
	// FinalSize of RESET_STREAM; FrameType and Reason of CONNECTION_CLOSE.
	ErrorCode *uint64 `json:"errorCode,omitempty"`
	FinalSize uint64  `json:"finalSize,omitempty"`
	FrameType uint64  `json:"frameType,omitempty"`
	Reason    string  `json:"reason,omitempty"`
	// NEW_CONNECTION_ID fields (Sequence also of RETIRE_CONNECTION_ID and
	// ACK_FREQUENCY).
	Sequence      uint64 `json:"seq,omitempty"`
	RetirePriorTo uint64 `json:"retirePriorTo,omitempty"`
	CIDLength     int    `json:"cidLength,omitempty"`
	// Data of PATH_CHALLENGE and PATH_RESPONSE, hex.
	Data string `json:"data,omitempty"`
	// ACK_FREQUENCY fields; MaxAckDelay is in microseconds.
	Threshold   uint64 `json:"threshold,omitempty"`
	MaxAckDelay uint64 `json:"maxAckDelay,omitempty"`
	Reordering  uint64 `json:"reordering,omitempty"`
}

// TransportParameter is one QUIC transport parameter (RFC 9000, section 18).
type TransportParameter struct {
	ID uint64 `json:"id"`
	// Grease marks a reserved identifier (31 * N + 27).
	Name   string `json:"name,omitempty"`
	Grease bool   `json:"grease,omitempty"`
	Length int    `json:"length"`
	// Value of a parameter whose value is one variable-length integer.
	Value *uint64 `json:"value,omitempty"`
	// Hex of any other value, except random connection IDs.
	Hex string `json:"hex,omitempty"`
	// Versions of version_information (RFC 9368): chosen, then available.
	Versions []string `json:"versions,omitempty"`
}

// Close is how a QUIC connection ended.
type Close struct {
	// By is "client", "server", or "idle" (idle timeout; RFC 9000 closes
	// silently).
	By string `json:"by"`
	// Millis since the server started serving the page.
	Millis int64 `json:"ms"`
	// Error is the close's error as the server's QUIC stack reported it.
	Error string `json:"error,omitempty"`
}

// H3 is what a client sent at the HTTP/3 layer (RFC 9114).
type H3 struct {
	// Streams are the client's unidirectional streams in wire order of
	// their first STREAM frame.
	Streams []H3Stream `json:"streams"`
	// Requests are its request streams in the same order.
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
	// Millis since the server started serving the page.
	Millis int64  `json:"ms"`
	ID     uint64 `json:"id"`
	Type   uint64 `json:"type"`
	// Grease marks a reserved type (0x1f * N + 0x21).
	Name   string `json:"name,omitempty"`
	Grease bool   `json:"grease,omitempty"`
	// Bytes is how much the stream carried after its type.
	Bytes int `json:"bytes"`
	// Frames of a control stream; Encoder and Decoder of QPACK streams.
	Frames  []H3Frame            `json:"frames,omitempty"`
	Encoder []EncoderInstruction `json:"encoder,omitempty"`
	Decoder []DecoderInstruction `json:"decoder,omitempty"`
	// End is "FIN" or "RESET <code>"; "" while open.
	End string `json:"end,omitempty"`
	// Error is why the server stopped reading it.
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
	// Millis since the server started serving the page.
	Millis int64  `json:"ms"`
	Type   uint64 `json:"type"`
	// Grease marks a reserved type (0x1f * N + 0x21).
	Name   string `json:"name,omitempty"`
	Grease bool   `json:"grease,omitempty"`
	Length uint64 `json:"length"`
	// Settings of a SETTINGS frame, in the order sent.
	Settings []Setting `json:"settings,omitempty"`
	// ID of GOAWAY, MAX_PUSH_ID, CANCEL_PUSH and PRIORITY_UPDATE; Priority
	// is PRIORITY_UPDATE's Priority Field Value.
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
	// Millis is when the stream's first frame was read.
	Millis int64  `json:"ms"`
	Stream uint64 `json:"stream"`
	// Method and Path identify the request.
	Method string `json:"method"`
	Path   string `json:"path"`
	// Frames are its frames in order (HEADERS, DATA, trailers, others).
	Frames []H3Frame `json:"frames"`
	// Block is the HEADERS field section, hex; Section is it decoded.
	Block   string  `json:"block"`
	Section Section `json:"section"`
	// Blocked reports that the section referenced dynamic table entries
	// whose inserts had not yet arrived on the encoder stream.
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
