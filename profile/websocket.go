package profile

import (
	"strings"
)

// WebSocket is how the browser speaks on a WebSocket once its handshake is
// sent (websocket and websocket-h2 captures). The handshake itself is the
// profile's websocket kind (KindWebSocket): its header lists, with the
// extended CONNECT's :protocol after the profile's pseudo_header_order,
// and its HTTP/2 weight. The HTTP/1.1-only connection a socket opens when
// the server allows no extended CONNECT says hello as TLS.WebSocketH1.
type WebSocket struct {
	// Extensions is the Sec-WebSocket-Extensions offer, exactly as sent.
	// Required.
	Extensions string `json:"extensions"`
	// Compression is how a message is compressed when the server accepts
	// permessage-deflate: CompressionZlib6. Empty: not measured, and
	// sending a message on a socket that negotiated it is an error.
	Compression string `json:"compression,omitempty"`
	// UncompressedMax is the longest message payload sent uncompressed
	// (RSV1 clear), outside the compression context, on a socket that
	// negotiated permessage-deflate (Safari: 5). CompressMin is the
	// shortest compressed. Both zero: every message is compressed. A
	// payload between the two is not measured, so sending one is an
	// error. Need Compression.
	UncompressedMax int `json:"uncompressed_max,omitempty"`
	CompressMin     int `json:"compress_min,omitempty"`
	// H2Data is how a socket over HTTP/2 writes its frames into DATA
	// frames, each at most HTTP2.MaxDataFrame long: H2DataTogether,
	// H2DataHeaderAlone or H2DataSplitControl. Required.
	H2Data string `json:"h2_data"`
	// CloseH1 is what the browser does with the HTTP/1.1 connection once
	// the close handshake is done: CloseAfterWait or CloseAtOnce. Empty:
	// not measured, and the connection is left for the server to close.
	CloseH1 string `json:"close_h1,omitempty"`
	// CloseWaitMillis is how long CloseAfterWait waits for the server to
	// close first (Chrome: 2000).
	CloseWaitMillis int `json:"close_wait_ms,omitempty"`
	// CloseH2 is how the browser ends the socket's HTTP/2 stream:
	// CloseEndStream or CloseResetStream. Empty: not measured, and the
	// stream is left for the server to end.
	CloseH2 string `json:"close_h2,omitempty"`
}

// Values of WebSocket.Compression.
const (
	// CompressionZlib6 is zlib's raw deflate at level 6 (window bits 15,
	// memLevel 8, default strategy), each message ended with a sync flush
	// whose 00 00 ff ff is cut, with the context kept across messages
	// (Chrome, Safari: byte for byte).
	CompressionZlib6 = "zlib6"
)

// Values of WebSocket.H2Data.
const (
	// H2DataTogether writes each WebSocket frame, header and payload,
	// into as few DATA frames as the cap allows (Chrome).
	H2DataTogether = "together"
	// H2DataHeaderAlone writes a frame's header (mask included) in a DATA
	// frame of its own when its payload does not fit with it, then the
	// payload in DATA frames of the cap (Firefox).
	H2DataHeaderAlone = "header_alone"
	// H2DataSplitControl writes frames as H2DataTogether, but its replies
	// to the server's control frames, the pong and the close reply, as
	// three DATA frames: the 2-byte header, the 4-byte mask, the payload
	// (Safari; a close it starts goes whole).
	H2DataSplitControl = "split_control"
)

// Values of WebSocket.CloseH1 and WebSocket.CloseH2.
const (
	// CloseAfterWait closes the TCP connection CloseWaitMillis after the
	// close handshake unless the server did first (Chrome).
	CloseAfterWait = "after_wait"
	// CloseAtOnce closes the TCP connection as soon as the close
	// handshake is done (Safari).
	CloseAtOnce = "at_once"
	// CloseEndStream sets END_STREAM on the DATA frame carrying the
	// browser's close frame, its own or its reply (Chrome).
	CloseEndStream = "end_stream"
	// CloseResetStream sends RST_STREAM NO_ERROR once the close handshake
	// is done (Safari).
	CloseResetStream = "rst_stream"
)

// WebSocketHello is how the ClientHello of a WebSocket's own HTTP/1.1
// connection differs from TLS's: the browser opens one per socket when the
// server allows no extended CONNECT. It resumes as TLS.Resumption says;
// Chrome's and Firefox's were all resumed in the captures, so the same
// changes on a full handshake are the profile's assumption.
type WebSocketHello struct {
	// ALPN replaces the ALPN extension's protocols (every browser:
	// http/1.1 only). Required.
	ALPN []string `json:"alpn"`
	// Drop are extensions it leaves out besides those resumption drops
	// (Chrome: ALPS; Firefox: compress_certificate).
	Drop []Code `json:"drop,omitempty"`
}

// checkWebSocket checks the websocket section: the offer, known values,
// and the websocket kind and TLS.WebSocketH1 it needs.
func (c *checker) checkWebSocket(p *Profile) {
	ws := p.WebSocket
	if ws == nil {
		return
	}
	const path = "websocket"
	switch {
	case ws.Extensions == "":
		c.add(path+".extensions", "is required")
	case strings.ContainsAny(ws.Extensions, "\r\n\x00"):
		c.add(path+".extensions", "holds CR, LF or NUL")
	}
	switch ws.Compression {
	case "":
		if ws.UncompressedMax != 0 || ws.CompressMin != 0 {
			c.add(path, "uncompressed_max and compress_min need compression")
		}
	case CompressionZlib6:
	default:
		c.add(path+".compression", "must be empty or %q", CompressionZlib6)
	}
	if (ws.UncompressedMax != 0 || ws.CompressMin != 0) && (ws.UncompressedMax <= 0 || ws.CompressMin <= ws.UncompressedMax) {
		c.add(path, "uncompressed_max and compress_min must be both zero, or 0 < uncompressed_max < compress_min")
	}
	if ws.H2Data != H2DataTogether && ws.H2Data != H2DataHeaderAlone && ws.H2Data != H2DataSplitControl {
		c.add(path+".h2_data", "must be %q, %q or %q", H2DataTogether, H2DataHeaderAlone, H2DataSplitControl)
	}
	switch ws.CloseH1 {
	case CloseAfterWait:
		if ws.CloseWaitMillis <= 0 {
			c.add(path+".close_wait_ms", "is required with %q", CloseAfterWait)
		}
	case "", CloseAtOnce:
		if ws.CloseWaitMillis != 0 {
			c.add(path+".close_wait_ms", "applies only with close_h1 %q", CloseAfterWait)
		}
	default:
		c.add(path+".close_h1", "must be empty, %q or %q", CloseAfterWait, CloseAtOnce)
	}
	if ws.CloseH2 != "" && ws.CloseH2 != CloseEndStream && ws.CloseH2 != CloseResetStream {
		c.add(path+".close_h2", "must be empty, %q or %q", CloseEndStream, CloseResetStream)
	}
	if p.Kinds[KindWebSocket] == nil {
		c.add("kinds."+KindWebSocket, "is required with websocket")
	}
	if p.TLS.WebSocketH1 == nil {
		c.add("tls.websocket_h1", "is required with websocket")
	}
}

// checkWebSocketHello checks TLS.WebSocketH1 against the hello it changes.
func (c *checker) checkWebSocketHello(t *TLS) {
	h := t.WebSocketH1
	if h == nil {
		return
	}
	const path = "tls.websocket_h1"
	if len(h.ALPN) == 0 {
		c.add(path+".alpn", "is required")
	}
	if t.extension(ExtALPN) == nil {
		c.add(path+".alpn", "needs the hello's ALPN extension")
	}
	seen := map[Code]bool{}
	for _, d := range h.Drop {
		switch {
		case seen[d]:
			c.add(path+".drop", "lists %s twice", d)
		case d == ExtALPN || d == ExtServerName:
			c.add(path+".drop", "must not drop %s", d)
		case t.extension(d) == nil:
			c.add(path+".drop", "%s is not in the hello", d)
		}
		seen[d] = true
	}
}
