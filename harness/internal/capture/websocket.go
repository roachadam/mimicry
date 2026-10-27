package capture

// WebSocket is one WebSocket a page opened, as the server saw it: where
// its handshake is recorded, what the server answered, and every frame of
// the socket in the order it crossed the wire.
type WebSocket struct {
	// Millis is when the handshake arrived, since the capture server
	// started serving the page: it orders a page's sockets.
	Millis int64 `json:"ms"`
	// Host is the connection's server name, or for a plain connection the
	// Host header's host; Path is the handshake's path.
	Host string `json:"host"`
	Path string `json:"path"`
	// Protocol is what carried the socket: "http/1.1" (an Upgrade, RFC
	// 6455) or "h2" (an extended CONNECT stream, RFC 8441). Plain is a
	// ws:// socket, on a connection without TLS.
	Protocol string `json:"protocol"`
	Plain    bool   `json:"plain,omitempty"`
	// Connection numbers the TLS connection in Page.Connections, from 1;
	// 0 for a plain connection, which has no entry there.
	Connection int `json:"connection,omitempty"`
	// ProxyConn is the ProxyRequest.Conn of the capture-proxy connection
	// that carried the socket; 0 for a direct socket.
	ProxyConn int `json:"proxy_conn,omitempty"`
	// Request is the handshake's index: in Page.H1 for http/1.1, in
	// Page.H2[Trace].Requests for h2 (-1 if that trace had already ended).
	// Stream is the h2 stream that carried it.
	Request int    `json:"request"`
	Trace   int    `json:"trace,omitempty"`
	Stream  uint32 `json:"stream,omitempty"`
	// Status is the server's answer: 101 (http/1.1) or 200 (h2).
	// Subprotocol and Extensions are the Sec-WebSocket-Protocol and
	// Sec-WebSocket-Extensions it answered with; the client's offers are
	// in the handshake's header lines or instructions.
	Status      int    `json:"status"`
	Subprotocol string `json:"subprotocol,omitempty"`
	Extensions  string `json:"extensions,omitempty"`
	// Frames are the client's frames and, for their order, the server's
	// (Server, without payload), as they crossed the wire.
	Frames []WSFrame `json:"frames"`
	// ClosedBy is the side that ended the transport (TCP connection or h2
	// stream) first: "client", "server", or "" if still open when the page
	// was taken. After a close handshake the server waits WSLinger before
	// ending it, so a client that closes on its own shows. ClosedMillis is
	// when, since the handshake.
	ClosedBy     string `json:"closed_by,omitempty"`
	ClosedMillis int64  `json:"closed_ms,omitempty"`
	// StreamEnd is how the client ended its h2 stream: "END_STREAM" or
	// "RST_STREAM <code>"; "" if it did not, or for http/1.1.
	StreamEnd string `json:"stream_end,omitempty"`
	// Error is why the server stopped reading a socket the client had not
	// closed: a frame it could not parse.
	Error string `json:"error,omitempty"`
}

// WebSocket frame opcodes (RFC 6455, section 5.2).
const (
	WSContinuation = 0x0
	WSText         = 0x1
	WSBinary       = 0x2
	WSClose        = 0x8
	WSPing         = 0x9
	WSPong         = 0xa
)

// WSFrame is one WebSocket frame: the client's as sent, or the server's
// (Server) for order.
type WSFrame struct {
	// Millis since the handshake arrived.
	Millis int64 `json:"ms"`
	// Server marks a frame the server sent; its payload is not kept.
	Server bool `json:"server,omitempty"`
	Fin    bool `json:"fin"`
	// RSV holds the RSV1, RSV2 and RSV3 bits as sent (0x4 is RSV1, which
	// permessage-deflate uses).
	RSV    uint8 `json:"rsv,omitempty"`
	Opcode uint8 `json:"opcode"`
	// Masked is the mask bit, and Mask the masking key it says follows
	// (hex; random by design).
	Masked bool   `json:"masked"`
	Mask   string `json:"mask,omitempty"`
	// LengthBits is the payload length's encoding as sent: 7 (in the
	// second byte), 16 (126 and two bytes) or 64 (127 and eight bytes).
	// Length is the payload length.
	LengthBits int    `json:"length_bits"`
	Length     uint64 `json:"length"`
	// Payload is the client's payload, unmasked, as on the wire (still
	// compressed when Compressed).
	Payload []byte `json:"payload,omitempty"`
	// Compressed marks every frame of a message whose first frame had
	// RSV1 set on a socket that negotiated permessage-deflate.
	Compressed bool `json:"compressed,omitempty"`
	// Inflated is a compressed message's payload after inflating it (RFC
	// 7692, with the context of the socket's earlier messages), on its
	// last frame; InflateError says why that failed.
	Inflated     []byte `json:"inflated,omitempty"`
	InflateError string `json:"inflate_error,omitempty"`
	// CloseCode and CloseReason are a close frame's status code (0 when
	// its payload is empty) and reason.
	CloseCode   uint16 `json:"close_code,omitempty"`
	CloseReason string `json:"close_reason,omitempty"`
}

// WSResult is what a page's script saw of one WebSocket step, as it
// reported it: whether the browser opened the socket at all (a refused
// one never reaches the server), and how it closed.
type WSResult struct {
	// Label names the step (server.WebSocketStep).
	Label string `json:"label"`
	// Open reports that onopen fired; Protocol and Extensions are the
	// socket's protocol and extensions attributes then.
	Open       bool   `json:"open"`
	Protocol   string `json:"protocol,omitempty"`
	Extensions string `json:"extensions,omitempty"`
	// Sent is how many messages the page sent, Echoes how many came back.
	Sent   int `json:"sent"`
	Echoes int `json:"echoes"`
	// Closed reports that onclose fired, with Code, Reason and Clean
	// (wasClean); TimedOut that the step ran out of time first.
	Closed   bool   `json:"closed"`
	Code     int    `json:"code,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Clean    bool   `json:"clean,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
	// Skipped reports that the page did not open the socket: the step
	// hangs this browser (server.WebSocketStep.Hangs).
	Skipped bool `json:"skipped,omitempty"`
	// Error is an exception the constructor threw (name: message), or
	// "error" when onerror fired.
	Error string `json:"error,omitempty"`
	// Host is the host the result was reported to, and Raw the report
	// itself when it did not decode.
	Host string `json:"host,omitempty"`
	Raw  string `json:"raw,omitempty"`
}
