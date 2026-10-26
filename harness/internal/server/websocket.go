package server

import (
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/sha1" //nolint:gosec // RFC 6455's accept key is defined with SHA-1
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// WSLinger is how long the server waits, once a WebSocket's close
// handshake has started, for the client to end the transport (close the
// TCP connection, or end its HTTP/2 stream) before it ends it itself. RFC
// 6455 has the server close TCP first; waiting shows whether a client
// closes on its own, and when.
const WSLinger = 3 * time.Second

// wsGUID is RFC 6455's key suffix (section 1.3).
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsMaxPayload bounds a frame's payload, so a frame header announcing
// gigabytes fails instead of allocating them.
const wsMaxPayload = 16 << 20

// wsAcceptKey is the Sec-WebSocket-Accept value for a Sec-WebSocket-Key.
func wsAcceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID)) //nolint:gosec // RFC 6455, section 4.2.2
	return base64.StdEncoding.EncodeToString(h[:])
}

// wsAccept is how a scenario accepts a WebSocket handshake and runs the
// socket. Every text and binary message is echoed back, uncompressed; a
// ping is answered with a pong, a close with a close carrying its code.
type wsAccept struct {
	// subprotocol and extensions are answered as Sec-WebSocket-Protocol
	// and Sec-WebSocket-Extensions ("" for none); extensions is
	// "permessage-deflate", with any parameters, when the server accepts
	// compression. clientNoContext: it answered
	// client_no_context_takeover, so each client message is inflated on
	// its own.
	subprotocol     string
	extensions      string
	clientNoContext bool
	// ping sends a ping right after the handshake, to record the pong.
	ping bool
	// serverClose sends a close (1001) after the first echo: the server
	// closes first.
	serverClose bool
}

// wsServerPing is the payload of the server's ping.
const wsServerPing = "mimicry"

// The close the server starts (wsAccept.serverClose).
const (
	wsServerCloseCode   = 1001
	wsServerCloseReason = "going away"
)

// wsFrame is one frame as read: its header fields as sent and its payload
// unmasked.
type wsFrame struct {
	fin    bool
	rsv    uint8
	opcode uint8
	masked bool
	mask   [4]byte
	// lengthBits is the payload length's encoding: 7, 16 or 64.
	lengthBits int
	payload    []byte
}

// readWSFrame reads one frame (RFC 6455, section 5.2). It returns io.EOF
// only when r ends before the frame starts.
func readWSFrame(r io.Reader) (wsFrame, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return wsFrame{}, err
	}
	f := wsFrame{
		fin:        h[0]&0x80 != 0,
		rsv:        h[0] >> 4 & 0x7,
		opcode:     h[0] & 0xf,
		masked:     h[1]&0x80 != 0,
		lengthBits: 7,
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return f, noEOF(err)
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
		f.lengthBits = 16
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return f, noEOF(err)
		}
		n = binary.BigEndian.Uint64(b[:])
		f.lengthBits = 64
	}
	if n > wsMaxPayload {
		return f, fmt.Errorf("websocket frame of %d bytes, more than %d", n, wsMaxPayload)
	}
	if f.masked {
		if _, err := io.ReadFull(r, f.mask[:]); err != nil {
			return f, noEOF(err)
		}
	}
	f.payload = make([]byte, n)
	if _, err := io.ReadFull(r, f.payload); err != nil {
		return f, noEOF(err)
	}
	if f.masked {
		for i := range f.payload {
			f.payload[i] ^= f.mask[i%4]
		}
	}
	return f, nil
}

// noEOF turns an end of input inside a frame into io.ErrUnexpectedEOF.
func noEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// appendWSFrame appends a final, unmasked frame, as a server sends it,
// with its length in the shortest form.
func appendWSFrame(b []byte, opcode uint8, payload []byte) []byte {
	b = append(b, 0x80|opcode)
	switch n := len(payload); {
	case n < 126:
		b = append(b, byte(n))
	case n <= 0xffff:
		b = append(b, 126)
		b = binary.BigEndian.AppendUint16(b, uint16(n))
	default:
		b = append(b, 127)
		b = binary.BigEndian.AppendUint64(b, uint64(n))
	}
	return append(b, payload...)
}

// wsLengthBits is the shortest length encoding of n bytes.
func wsLengthBits(n int) int {
	switch {
	case n < 126:
		return 7
	case n <= 0xffff:
		return 16
	}
	return 64
}

// wsInflater inflates a socket's permessage-deflate messages (RFC 7692)
// with context takeover: each message continues the LZ77 window of the
// ones before, so the last 32 KiB inflated prime the next.
type wsInflater struct {
	window []byte
}

// inflate inflates one message's payload (its frames' payloads joined).
func (z *wsInflater) inflate(compressed []byte) ([]byte, error) {
	// The sender removed the 0x00 0x00 0xff 0xff that ended its flush
	// (RFC 7692, section 7.2.2).
	in := append(slices.Clip(compressed), 0x00, 0x00, 0xff, 0xff)
	fr := flate.NewReaderDict(bytes.NewReader(in), z.window)
	out, err := io.ReadAll(fr)
	// A flushed message is not a finished stream: the reader runs out of
	// input after the empty stored block.
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return out, err
	}
	z.window = append(z.window, out...)
	if over := len(z.window) - 32<<10; over > 0 {
		z.window = slices.Clone(z.window[over:])
	}
	return out, nil
}

// wsSession runs one accepted WebSocket over its transport and records
// it.
type wsSession struct {
	rec    *wsRecorder
	accept *wsAccept
	// write sends bytes to the client; end ends the transport: closes the
	// TCP connection (HTTP/1.1) or ends the stream (HTTP/2). Only the
	// reading goroutine writes; end may be called from the linger timer.
	write func([]byte) error
	end   func() error

	inflater wsInflater
	once     sync.Once
	linger   *time.Timer
}

// errWSStreamReset is an HTTP/2 WebSocket stream the client reset.
var errWSStreamReset = errors.New("stream reset by the client")

// run reads the client's frames from r until the transport ends,
// answering them as the socket's wsAccept says.
func (w *wsSession) run(r io.Reader) error {
	defer w.stopLinger()
	if w.accept.ping {
		if err := w.send(capture.WSPing, []byte(wsServerPing)); err != nil {
			return err
		}
	}
	br := bufio.NewReader(r)
	var (
		message    []byte
		msgOpcode  uint8
		compressed bool
		echoes     int
		closeSent  bool
	)
	for {
		f, err := readWSFrame(br)
		if err != nil {
			if clientEnded(err) {
				w.rec.closed("client")
				if errors.Is(err, io.ErrUnexpectedEOF) {
					w.rec.fail(errors.New("the transport ended inside a frame"))
				}
				return nil
			}
			if !isClosed(err) {
				w.rec.fail(err)
			}
			return err
		}
		cf := capture.WSFrame{
			Fin:        f.fin,
			RSV:        f.rsv,
			Opcode:     f.opcode,
			Masked:     f.masked,
			LengthBits: f.lengthBits,
			Length:     uint64(len(f.payload)),
			Payload:    f.payload,
		}
		if f.masked {
			cf.Mask = hex.EncodeToString(f.mask[:])
		}
		data := f.opcode < capture.WSClose
		switch f.opcode {
		case capture.WSContinuation:
			cf.Compressed = compressed
		case capture.WSText, capture.WSBinary:
			msgOpcode = f.opcode
			message = message[:0]
			compressed = w.accept.extensions != "" && f.rsv&0x4 != 0
			cf.Compressed = compressed
		case capture.WSClose:
			if len(f.payload) >= 2 {
				cf.CloseCode = binary.BigEndian.Uint16(f.payload)
				cf.CloseReason = string(f.payload[2:])
			}
		}
		var echo []byte
		if data {
			message = append(message, f.payload...)
			if f.fin {
				echo = message
				if compressed {
					if w.accept.clientNoContext {
						w.inflater.window = nil
					}
					out, err := w.inflater.inflate(message)
					if err != nil {
						cf.InflateError = err.Error()
					} else {
						cf.Inflated = out
						echo = out
					}
				}
			}
		}
		w.rec.frame(cf)
		switch {
		case data && f.fin:
			if err := w.send(msgOpcode, echo); err != nil {
				return err
			}
			echoes++
			if w.accept.serverClose && echoes == 1 && !closeSent {
				closeSent = true
				payload := binary.BigEndian.AppendUint16(nil, wsServerCloseCode)
				if err := w.send(capture.WSClose, append(payload, wsServerCloseReason...)); err != nil {
					return err
				}
				w.startLinger()
			}
		case f.opcode == capture.WSPing:
			if err := w.send(capture.WSPong, f.payload); err != nil {
				return err
			}
		case f.opcode == capture.WSClose:
			if !closeSent {
				closeSent = true
				var payload []byte
				if cf.CloseCode != 0 {
					payload = binary.BigEndian.AppendUint16(nil, cf.CloseCode)
				}
				if err := w.send(capture.WSClose, payload); err != nil {
					return err
				}
			}
			w.startLinger()
		}
	}
}

// send writes a server frame and records it, without its payload.
func (w *wsSession) send(opcode uint8, payload []byte) error {
	if err := w.write(appendWSFrame(nil, opcode, payload)); err != nil {
		return err
	}
	w.rec.frame(capture.WSFrame{
		Server:     true,
		Fin:        true,
		Opcode:     opcode,
		LengthBits: wsLengthBits(len(payload)),
		Length:     uint64(len(payload)),
	})
	return nil
}

// startLinger ends the transport WSLinger from the first call, unless the
// client ends it first.
func (w *wsSession) startLinger() {
	w.once.Do(func() {
		w.linger = time.AfterFunc(WSLinger, func() {
			w.rec.closed("server")
			_ = w.end()
		})
	})
}

func (w *wsSession) stopLinger() {
	w.once.Do(func() {})
	if w.linger != nil {
		w.linger.Stop()
	}
}

// clientEnded reports a read error that means the client ended the
// transport: it closed or reset the TCP connection, or ended or reset its
// HTTP/2 stream.
func clientEnded(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, errWSStreamReset) ||
		errors.Is(err, syscall.ECONNRESET)
}

// serveWebSocketH1 answers an HTTP/1.1 handshake a scenario accepted with
// 101 and runs the socket on conn; br holds what the client sent after the
// handshake. ws says where the handshake is recorded.
func serveWebSocketH1(conn net.Conn, br *bufio.Reader, a *wsAccept, key string, ws capture.WebSocket, log *pageLog) error {
	ws.Status = 101
	ws.Subprotocol = a.subprotocol
	ws.Extensions = a.extensions
	rec := log.openWebSocket(ws)
	var b strings.Builder
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ")
	b.WriteString(wsAcceptKey(key))
	b.WriteString("\r\n")
	if a.subprotocol != "" {
		b.WriteString("Sec-WebSocket-Protocol: " + a.subprotocol + "\r\n")
	}
	if a.extensions != "" {
		b.WriteString("Sec-WebSocket-Extensions: " + a.extensions + "\r\n")
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return err
	}
	s := &wsSession{
		rec:    rec,
		accept: a,
		write: func(p []byte) error {
			_, err := conn.Write(p)
			return err
		},
		end: conn.Close,
	}
	err := s.run(br)
	if isClosed(err) || errors.Is(err, syscall.ECONNRESET) {
		return nil
	}
	return err
}

// wsResultPath is where a websocket page reports what its script saw of
// each step: label and r (the result as JSON) in the query.
const wsResultPath = "/w/result"

// noteWSResult records a websocket page's report of a step; connHost is
// the connection's server name ("" for a plain connection).
func (sc *Scenario) noteWSResult(log *pageLog, r request, connHost string) {
	if _, ok := sc.multi.(wsPage); !ok {
		return
	}
	path, query, _ := strings.Cut(r.path, "?")
	if path != wsResultPath {
		return
	}
	host := connHost
	if host == "" {
		host, _ = splitAuthority(r.host)
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		log.result(capture.WSResult{
			Host: host,
			Raw:  query,
		})
		return
	}
	var res capture.WSResult
	dec := json.NewDecoder(strings.NewReader(q.Get("r")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		res = capture.WSResult{
			Raw: q.Get("r"),
		}
	}
	res.Label = q.Get("label")
	res.Host = host
	log.result(res)
}

// joinValues joins a repeated header's values, as one line listing them
// would.
func joinValues(have, value string) string {
	if have == "" {
		return value
	}
	return have + ", " + value
}

// h2WebSocket is an extended CONNECT stream (RFC 8441) carrying a
// WebSocket: its DATA, and its recorder.
type h2WebSocket struct {
	in  *wsBuffer
	rec *wsRecorder
}

// wsBuffer carries a stream's DATA from the connection's read loop to its
// socket. Writing never blocks: the read loop must go on reading the
// WINDOW_UPDATEs the socket's echoes may wait for.
type wsBuffer struct {
	mu   sync.Mutex
	cond *sync.Cond
	data []byte
	// err is why the stream ended, once it did.
	err error
}

func newWSBuffer() *wsBuffer {
	b := &wsBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// write appends a DATA frame's payload (which the framer reuses, so it is
// copied).
func (b *wsBuffer) write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.data = append(b.data, p...)
		b.cond.Broadcast()
	}
}

// end ends the stream with err, which Read returns once the data before it
// is read. Only the first end counts.
func (b *wsBuffer) end(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = err
		b.cond.Broadcast()
	}
}

// reset reports a stream the client reset.
func (b *wsBuffer) reset() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return errors.Is(b.err, errWSStreamReset)
}

func (b *wsBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.data) == 0 && b.err == nil {
		b.cond.Wait()
	}
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	return 0, b.err
}

// onConnect answers an extended CONNECT. Without the scenario advertising
// SETTINGS_ENABLE_CONNECT_PROTOCOL, a :protocol makes the request
// malformed (RFC 8441, section 4), so the stream is reset; otherwise a
// handshake the scenario accepts is answered 200 and its socket runs on
// the stream, from a goroutine of its own (senders), and any other is
// answered as the scenario says.
func (s *h2conn) onConnect(id uint32, st *request, index int, senders *sync.WaitGroup) error {
	delete(s.streams, id)
	s.log.request(s.c)
	if v, _ := s.scenario.settingFor(s.host, http2.SettingEnableConnectProtocol); v != 1 {
		s.mu.Lock()
		delete(s.sendWindows, id)
		s.mu.Unlock()
		s.wmu.Lock()
		defer s.wmu.Unlock()
		return s.fr.WriteRSTStream(id, http2.ErrCodeProtocol)
	}
	r := s.scenario.answer(*st)
	a := r.websocket
	if a == nil {
		senders.Go(func() {
			if err := s.send(id, r); err != nil {
				s.mu.Lock()
				s.closed = true
				s.cond.Broadcast()
				s.mu.Unlock()
			}
		})
		return nil
	}
	ws := &h2WebSocket{
		in: newWSBuffer(),
		rec: s.log.openWebSocket(capture.WebSocket{
			Host:        s.host,
			Path:        st.path,
			Protocol:    "h2",
			Connection:  s.log.connectionNumber(s.c),
			ProxyConn:   s.c.ProxyConn,
			Request:     index,
			Trace:       s.traceIndex,
			Stream:      id,
			Status:      200,
			Subprotocol: a.subprotocol,
			Extensions:  a.extensions,
		}),
	}
	s.mu.Lock()
	s.websockets[id] = ws
	s.mu.Unlock()
	senders.Go(func() {
		defer func() {
			s.mu.Lock()
			delete(s.websockets, id)
			delete(s.sendWindows, id)
			s.mu.Unlock()
		}()
		if err := s.wsHeaders(id, a); err != nil {
			return
		}
		var ended sync.Once
		end := func() error {
			var err error
			ended.Do(func() {
				s.wmu.Lock()
				defer s.wmu.Unlock()
				err = s.fr.WriteData(id, true, nil)
			})
			return err
		}
		sess := &wsSession{
			rec:    ws.rec,
			accept: a,
			write: func(p []byte) error {
				return s.writeStream(id, p)
			},
			end: end,
		}
		// Once the client ended its stream, the server ends its own
		// (a reset stream is closed already).
		if err := sess.run(ws.in); err == nil && !ws.in.reset() {
			_ = end()
		}
	})
	return nil
}

// wsHeaders answers an accepted extended CONNECT: 200, with the
// subprotocol and extensions the server accepted.
func (s *h2conn) wsHeaders(id uint32, a *wsAccept) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.buf.Reset()
	fields := []hpack.HeaderField{
		{
			Name:  ":status",
			Value: "200",
		},
	}
	if a.subprotocol != "" {
		fields = append(fields, hpack.HeaderField{
			Name:  "sec-websocket-protocol",
			Value: a.subprotocol,
		})
	}
	if a.extensions != "" {
		fields = append(fields, hpack.HeaderField{
			Name:  "sec-websocket-extensions",
			Value: a.extensions,
		})
	}
	for _, f := range fields {
		_ = s.enc.WriteField(f)
	}
	return s.fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      id,
		BlockFragment: s.buf.Bytes(),
		EndHeaders:    true,
	})
}

// writeStream sends p on stream id as DATA, within the client's flow
// control windows.
func (s *h2conn) writeStream(id uint32, p []byte) error {
	for len(p) > 0 {
		n, err := s.reserve(id, len(p))
		if err != nil {
			return err
		}
		s.wmu.Lock()
		err = s.fr.WriteData(id, false, p[:n])
		s.wmu.Unlock()
		if err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// onWebSocketData hands a WebSocket stream's DATA to its socket, and
// reports whether f was on one.
func (s *h2conn) onWebSocketData(f *http2.DataFrame) bool {
	s.mu.Lock()
	ws := s.websockets[f.StreamID]
	s.mu.Unlock()
	if ws == nil {
		return false
	}
	ws.in.write(f.Data())
	if f.StreamEnded() {
		ws.rec.streamEnd("END_STREAM")
		ws.in.end(io.EOF)
	}
	return true
}

// onReset ends a WebSocket stream the client reset.
func (s *h2conn) onReset(f *http2.RSTStreamFrame) {
	s.mu.Lock()
	ws := s.websockets[f.StreamID]
	s.mu.Unlock()
	if ws == nil {
		return
	}
	ws.rec.streamEnd("RST_STREAM " + f.ErrCode.String())
	ws.in.end(errWSStreamReset)
}
