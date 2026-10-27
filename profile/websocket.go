package profile

import (
	"strings"
)

// WebSocket is how the browser speaks on a WebSocket once its handshake is
// sent. The handshake itself is the websocket kind (KindWebSocket); the
// HTTP/1.1-only connection opened when the server allows no extended
// CONNECT says hello as TLS.WebSocketH1.
type WebSocket struct {
	// Extensions is the Sec-WebSocket-Extensions offer, exactly as sent.
	// Required.
	Extensions string `json:"extensions"`
	// Compression is the deflate implementation (level 6) used once the
	// server accepts permessage-deflate: CompressionChromiumZlib6,
	// CompressionZlib6 or CompressionZlibRS6. Required.
	Compression string `json:"compression"`
	// Uncompressed is which messages the browser sends uncompressed (RSV1
	// clear) on such a socket: UncompressedNever,
	// UncompressedEmptyOrLonger or UncompressedEmptyOrNotShorter.
	// Required.
	Uncompressed string `json:"uncompressed"`
	// WindowBits8 is what the browser does when the server answers
	// client_max_window_bits=8, which zlib cannot compress with:
	// WindowBits8As9 or WindowBits8Fail. Required.
	WindowBits8 string `json:"window_bits_8"`
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

// Values of WebSocket.Compression. Each is the library's raw deflate at
// level 6, memLevel 8 and the default strategy, each message ended with a
// sync flush whose 00 00 ff ff is cut, byte for byte.
const (
	// CompressionChromiumZlib6 is Chromium's fork of zlib, whose hash
	// finds different matches than zlib's (Chrome).
	CompressionChromiumZlib6 = "chromium_zlib6"
	// CompressionZlib6 is zlib (Safari).
	CompressionZlib6 = "zlib6"
	// CompressionZlibRS6 is zlib-rs, a port of zlib-ng (Firefox).
	CompressionZlibRS6 = "zlib_rs6"
)

// Values of WebSocket.Uncompressed.
const (
	// UncompressedNever compresses every message, an empty one as the
	// single byte 00 (Chrome).
	UncompressedNever = "never"
	// UncompressedEmptyOrLonger sends an empty message uncompressed, and,
	// with client_no_context_takeover, one whose compressed form is
	// longer than it (Firefox).
	UncompressedEmptyOrLonger = "empty_or_longer"
	// UncompressedEmptyOrNotShorter sends an empty message uncompressed,
	// and one whose compressed form is not shorter than it, after which
	// the compression context starts over (Safari).
	UncompressedEmptyOrNotShorter = "empty_or_not_shorter"
)

// Values of WebSocket.WindowBits8.
const (
	// WindowBits8As9 compresses with a 9-bit window (Chrome).
	WindowBits8As9 = "as_9"
	// WindowBits8Fail fails the socket once its handshake is done: the
	// transport ends at once, as CloseH1 and CloseH2 end it, with
	// nothing sent (Safari).
	WindowBits8Fail = "fail"
)

// Values of WebSocket.H2Data.
const (
	// H2DataTogether writes each WebSocket frame, header and payload,
	// into as few DATA frames as the cap allows (Chrome).
	H2DataTogether = "together"
	// H2DataHeaderAlone writes a frame's header (mask included) in a DATA
	// frame of its own when its payload exceeds 1000 bytes (Firefox's
	// kCopyBreak), then the payload.
	H2DataHeaderAlone = "header_alone"
	// H2DataSplitControl is H2DataTogether, except replies to control
	// frames go as three DATA frames: header, mask, payload (Safari).
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
// connection differs from TLS's. It resumes as TLS.Resumption says; only
// resumed ones were captured, so a full handshake is assumed alike.
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
	if ws.Compression != CompressionChromiumZlib6 && ws.Compression != CompressionZlib6 && ws.Compression != CompressionZlibRS6 {
		c.add(path+".compression", "must be %q, %q or %q", CompressionChromiumZlib6, CompressionZlib6, CompressionZlibRS6)
	}
	if ws.Uncompressed != UncompressedNever && ws.Uncompressed != UncompressedEmptyOrLonger && ws.Uncompressed != UncompressedEmptyOrNotShorter {
		c.add(path+".uncompressed", "must be %q, %q or %q", UncompressedNever, UncompressedEmptyOrLonger, UncompressedEmptyOrNotShorter)
	}
	if ws.WindowBits8 != WindowBits8As9 && ws.WindowBits8 != WindowBits8Fail {
		c.add(path+".window_bits_8", "must be %q or %q", WindowBits8As9, WindowBits8Fail)
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
