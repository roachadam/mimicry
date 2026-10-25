package server

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/quictrace"
	"github.com/roachadam/mimicry/profile"
)

// HTTP/3 error codes the server sends (RFC 9114, section 8.1).
const (
	h3NoError      = 0x100
	h3GeneralError = 0x101
)

// h3GoAwayGrace is how long after a GOAWAY the server closes the
// connection, so the response it follows reaches the client.
const h3GoAwayGrace = time.Second

// h3conn is the server side of one HTTP/3 connection: it answers requests
// as the scenario says and records what the client sends on each stream.
type h3conn struct {
	conn  *quic.Conn
	sc    *Scenario
	log   *pageLog
	start time.Time
	// addr is the client's address, which finds the connection's
	// datagrams; acceptedMillis is when the server accepted it, since the
	// page started.
	addr           string
	acceptedMillis int64

	// mu guards rec, closed and c, the connection's capture (handed over
	// at /done, or when the connection ends).
	mu     sync.Mutex
	rec    quictrace.H3
	closed *quictrace.Close
	c      *capture.Capture
	emit   func()

	// qmu guards the QPACK decoder's table and how many inserts the server
	// has acknowledged to the client's encoder (known); qcond wakes
	// requests blocked on the encoder stream, and qdone ends their wait.
	// decoderOut carries the server's decoder stream instructions to its
	// writer, in the order they were decided.
	qmu        sync.Mutex
	qcond      *sync.Cond
	qdone      bool
	table      *quictrace.Table
	known      uint64
	decoderOut chan []byte

	// wmu serialises writes to the control stream; maxStream is the
	// largest request stream ID seen, for GOAWAY.
	wmu       sync.Mutex
	control   *quic.SendStream
	maxStream uint64
}

// serveH3 serves one QUIC connection until it ends, or until the server
// stops (serverCtx), which closes it: the server's control stream with the
// scenario's SETTINGS and its QPACK streams, then every stream the client
// opens.
func (s *Server) serveH3(serverCtx context.Context, conn *quic.Conn, sc *Scenario, log *pageLog) error { //nolint:contextcheck // the connection's handlers run for its own lifetime (conn.Context), ended early by serverCtx
	h := &h3conn{
		conn:           conn,
		sc:             sc,
		log:            log,
		start:          log.start,
		addr:           conn.RemoteAddr().String(),
		acceptedMillis: time.Since(log.start).Milliseconds(),
		rec: quictrace.H3{
			Streams:  []quictrace.H3Stream{},
			Requests: []quictrace.H3Request{},
		},
		c: &capture.Capture{
			Scenario: sc.Name,
			Protocol: "h3",
		},
		table:      quictrace.NewTable(sc.h3.setting(quictrace.SettingQPACKMaxTableCapacity)),
		decoderOut: make(chan []byte, 256),
	}
	h.qcond = sync.NewCond(&h.qmu)
	var once sync.Once
	h.emit = func() {
		once.Do(func() {
			if s.OnCapture != nil {
				s.OnCapture(h.c)
			}
		})
	}
	defer h.emit()
	log.quic.add(h)
	// ctx ends with the connection, or when the server stops, which closes
	// the connection.
	ctx, cancel := context.WithCancelCause(serverCtx)
	defer cancel(nil)
	connCtx := conn.Context()
	stopConn := context.AfterFunc(connCtx, func() { //nolint:contextcheck // the connection's own lifetime
		cancel(context.Cause(connCtx))
	})
	defer stopConn()
	stop := context.AfterFunc(serverCtx, func() {
		_ = conn.CloseWithError(h3NoError, "")
	})
	defer stop()
	var wg sync.WaitGroup
	decoder, err := h.openStreams()
	if err == nil {
		wg.Go(func() {
			h.writeDecoderStream(ctx, decoder)
		})
		wg.Go(func() {
			h.acceptUni(ctx, &wg)
		})
		wg.Go(func() {
			h.acceptRequests(ctx, &wg)
		})
	} else {
		_ = conn.CloseWithError(h3GeneralError, "")
	}
	<-ctx.Done()
	h.recordClose(context.Cause(ctx))
	h.qmu.Lock()
	h.qdone = true
	h.qcond.Broadcast()
	h.qmu.Unlock()
	wg.Wait()
	if isClosed(err) {
		return nil
	}
	return err
}

// openStreams opens the server's control stream, with its SETTINGS, and
// its QPACK encoder and decoder streams; it returns the decoder stream.
func (h *h3conn) openStreams() (*quic.SendStream, error) {
	var settings []byte
	for _, st := range h.sc.h3.settings {
		settings = quictrace.AppendVarint(settings, st[0])
		settings = quictrace.AppendVarint(settings, st[1])
	}
	var decoder *quic.SendStream
	for _, typ := range []uint64{
		quictrace.StreamControl,
		quictrace.StreamQPACKEncoder,
		quictrace.StreamQPACKDecoder,
	} {
		str, err := h.conn.OpenUniStream()
		if err != nil {
			return nil, err
		}
		first := quictrace.AppendVarint(nil, typ)
		switch typ {
		case quictrace.StreamControl:
			first = quictrace.AppendFrame(first, quictrace.FrameH3Settings, settings)
			h.control = str
		case quictrace.StreamQPACKDecoder:
			decoder = str
		}
		if _, err := str.Write(first); err != nil {
			return nil, err
		}
	}
	return decoder, nil
}

// writeDecoderStream writes the server's decoder stream instructions in
// order until the connection ends.
func (h *h3conn) writeDecoderStream(ctx context.Context, str *quic.SendStream) {
	for {
		select {
		case b := <-h.decoderOut:
			if _, err := str.Write(b); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// toDecoder queues a decoder stream instruction; once the connection has
// ended it is dropped.
func (h *h3conn) toDecoder(b []byte) {
	select {
	case h.decoderOut <- b:
	case <-h.conn.Context().Done():
	}
}

// millis is the time since the page started.
func (h *h3conn) millis() int64 {
	return time.Since(h.start).Milliseconds()
}

// acceptUni reads every unidirectional stream the client opens.
func (h *h3conn) acceptUni(ctx context.Context, wg *sync.WaitGroup) {
	for {
		str, err := h.conn.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		wg.Go(func() {
			h.uniStream(str)
		})
	}
}

// acceptRequests answers every request stream the client opens.
func (h *h3conn) acceptRequests(ctx context.Context, wg *sync.WaitGroup) {
	for {
		str, err := h.conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		h.wmu.Lock()
		h.maxStream = max(h.maxStream, uint64(str.StreamID())) //nolint:gosec // stream IDs are below 2^62
		h.wmu.Unlock()
		wg.Go(func() {
			h.request(str)
		})
	}
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// streamEnd describes how a stream read ended: "FIN" at its end, "RESET
// <code>" if the client reset it, "" if the connection went away.
func streamEnd(err error) string {
	var se *quic.StreamError
	switch {
	case errors.Is(err, io.EOF):
		return "FIN"
	case errors.As(err, &se) && se.Remote:
		return "RESET " + strconv.FormatUint(uint64(se.ErrorCode), 10)
	}
	return ""
}

// uniStream reads one unidirectional stream: its type, then a control
// stream's frames, a QPACK stream's instructions, or any other type's
// bytes.
func (h *h3conn) uniStream(str *quic.ReceiveStream) {
	counter := &countingReader{
		r: str,
	}
	r := bufio.NewReader(counter)
	typ, err := quictrace.ReadVarint(r)
	if err != nil {
		return
	}
	typeLen := len(quictrace.AppendVarint(nil, typ))
	index := h.addStream(quictrace.H3Stream{
		Millis: h.millis(),
		ID:     uint64(str.StreamID()), //nolint:gosec // stream IDs are below 2^62
		Type:   typ,
		Name:   quictrace.StreamTypeName(typ),
		Grease: quictrace.IsGreaseH3(typ),
	})
	update := func(f func(*quictrace.H3Stream)) {
		h.mu.Lock()
		defer h.mu.Unlock()
		s := &h.rec.Streams[index]
		f(s)
		s.Bytes = counter.n - typeLen - r.Buffered()
	}
	switch typ {
	case quictrace.StreamControl:
		err = h.controlStream(r, update)
	case quictrace.StreamQPACKEncoder:
		err = h.encoderStream(r, update)
	case quictrace.StreamQPACKDecoder:
		err = decoderStream(r, update)
	default:
		_, err = io.Copy(io.Discard, r)
		if err == nil {
			err = io.EOF
		}
	}
	update(func(s *quictrace.H3Stream) {
		s.End = streamEnd(err)
		if s.End == "" && err != nil && !isClosed(err) && !isConnClosed(err) {
			s.Error = err.Error()
		}
	})
}

// isConnClosed reports an error that means the QUIC connection ended.
func isConnClosed(err error) bool {
	var app *quic.ApplicationError
	var tr *quic.TransportError
	var idle *quic.IdleTimeoutError
	return errors.As(err, &app) || errors.As(err, &tr) || errors.As(err, &idle) || errors.Is(err, quic.ErrTransportClosed)
}

func (h *h3conn) addStream(s quictrace.H3Stream) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rec.Streams = append(h.rec.Streams, s)
	return len(h.rec.Streams) - 1
}

// controlStream records the client's control stream frames.
func (h *h3conn) controlStream(r *bufio.Reader, update func(func(*quictrace.H3Stream))) error {
	for {
		typ, length, payload, err := quictrace.ReadFrame(r)
		if err != nil {
			return err
		}
		f := quictrace.NewFrame(h.millis(), typ, length)
		perr := quictrace.ParseControlFrame(&f, payload)
		update(func(s *quictrace.H3Stream) {
			s.Frames = append(s.Frames, f)
		})
		if perr != nil {
			return perr
		}
	}
}

// encoderStream applies the client's QPACK encoder stream to the table,
// records its instructions, wakes the requests waiting on them, and
// acknowledges the inserts (Insert Count Increment) so the encoder may
// refer to them.
func (h *h3conn) encoderStream(r *bufio.Reader, update func(func(*quictrace.H3Stream))) error {
	var pending []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		pending = append(pending, buf[:n]...)
		h.qmu.Lock()
		ins, used, perr := h.table.ReadEncoderStream(pending)
		if inserted := h.table.Inserted(); inserted > h.known {
			h.toDecoder(quictrace.AppendInsertCountIncrement(nil, inserted-h.known))
			h.known = inserted
		}
		h.qcond.Broadcast()
		h.qmu.Unlock()
		pending = pending[used:]
		if len(ins) > 0 {
			update(func(s *quictrace.H3Stream) {
				s.Encoder = append(s.Encoder, ins...)
			})
		}
		if perr != nil {
			return perr
		}
		if err != nil {
			return err
		}
	}
}

// decoderStream records the client's QPACK decoder stream.
func decoderStream(r *bufio.Reader, update func(func(*quictrace.H3Stream))) error {
	var pending []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		pending = append(pending, buf[:n]...)
		ins, used, perr := quictrace.ReadDecoderStream(pending)
		pending = pending[used:]
		if len(ins) > 0 {
			update(func(s *quictrace.H3Stream) {
				s.Decoder = append(s.Decoder, ins...)
			})
		}
		if perr != nil {
			return perr
		}
		if err != nil {
			return err
		}
	}
}

// decodeSection decodes a request's field section, waiting while it is
// blocked on the encoder stream, and acknowledges it (Section
// Acknowledgment) if it referred to the dynamic table. Whether the client
// sent it blocked is read from the wire (Conn.Rederive), not from this
// wait, which depends on the order the server's goroutines ran.
func (h *h3conn) decodeSection(stream uint64, block []byte) (quictrace.Section, error) {
	h.qmu.Lock()
	defer h.qmu.Unlock()
	for {
		s, err := h.table.DecodeSection(block)
		if !errors.Is(err, quictrace.ErrBlocked) || h.qdone {
			if err == nil && s.RequiredInsertCount > 0 {
				h.toDecoder(quictrace.AppendSectionAck(nil, stream))
				h.known = max(h.known, s.RequiredInsertCount)
			}
			return s, err
		}
		h.qcond.Wait()
	}
}

// request reads one request stream's frames to its end, records them,
// and answers the request.
func (h *h3conn) request(str *quic.Stream) {
	id := uint64(str.StreamID()) //nolint:gosec // stream IDs are below 2^62
	index := h.addRequest(quictrace.H3Request{
		Millis: h.millis(),
		Stream: id,
		Frames: []quictrace.H3Frame{},
	})
	update := func(f func(*quictrace.H3Request)) {
		h.mu.Lock()
		defer h.mu.Unlock()
		f(&h.rec.Requests[index])
	}
	r := bufio.NewReader(str)
	var st request
	headers := false
	var err error
	for {
		var typ, length uint64
		var payload []byte
		if typ, length, payload, err = quictrace.ReadFrame(r); err != nil {
			break
		}
		f := quictrace.NewFrame(h.millis(), typ, length)
		update(func(q *quictrace.H3Request) {
			q.Frames = append(q.Frames, f)
		})
		switch typ {
		case quictrace.FrameH3Headers:
			s, derr := h.decodeSection(id, payload)
			if derr != nil {
				err = derr
				break
			}
			if headers {
				update(func(q *quictrace.H3Request) {
					q.Trailers = &s
				})
				continue
			}
			headers = true
			st = requestOf(s.Fields)
			update(func(q *quictrace.H3Request) {
				q.Method, q.Path = st.method, st.path
				q.Block = hex.EncodeToString(payload)
				q.Section = s
			})
			h.firstRequest(s.Fields)
		case quictrace.FrameH3Data:
			st.uploaded += int(length) //nolint:gosec // bounded by the stream
		}
		if err != nil {
			break
		}
	}
	end := streamEnd(err)
	update(func(q *quictrace.H3Request) {
		q.End, q.Uploaded = end, st.uploaded
		if end == "" && err != nil && !isClosed(err) && !isConnClosed(err) {
			q.Error = err.Error()
		}
	})
	if end != "FIN" || !headers {
		str.CancelWrite(h3GeneralError)
		return
	}
	h.respond(str, st)
}

func (h *h3conn) addRequest(q quictrace.H3Request) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rec.Requests = append(h.rec.Requests, q)
	return len(h.rec.Requests) - 1
}

// requestOf is what a scenario needs to know of a request's fields.
func requestOf(fields []quictrace.FieldLine) request {
	var st request
	for _, f := range fields {
		switch f.Name {
		case ":path":
			st.path = f.Value
		case ":method":
			st.method = f.Value
		case ":authority":
			st.host = f.Value
		case "origin":
			st.origin = f.Value
		case "access-control-request-headers":
			st.preflightHeaders = f.Value
		}
	}
	return st
}

// firstRequest records the connection's first request's headers in its
// capture, which name the browser (its user agent).
func (h *h3conn) firstRequest(fields []quictrace.FieldLine) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.c.Profile.Headers) > 0 || h.c.Finished {
		return
	}
	for _, f := range fields {
		if !strings.HasPrefix(f.Name, ":") {
			h.c.Profile.Headers = append(h.c.Profile.Headers, profile.Header{
				Name:  f.Name,
				Value: f.Value,
			})
		}
	}
}

// respond answers a finished request as the scenario says; /done hands
// the page over first.
func (h *h3conn) respond(str *quic.Stream, st request) {
	r := h.sc.answer(st)
	if st.path == "/done" {
		// page() snapshots this connection too, so it runs before h.mu is
		// taken.
		page := h.log.page()
		h.mu.Lock()
		h.c.Page = page
		h.c.Finished = true
		h.log.pageProfile(h.c)
		h.mu.Unlock()
		h.emit()
	}
	time.Sleep(r.delay)
	fields := make([][2]string, 0, 4+len(r.headers))
	fields = append(fields,
		[2]string{":status", strconv.Itoa(r.status)},
		[2]string{"content-type", r.contentType},
		[2]string{"content-length", strconv.Itoa(len(r.body))},
		[2]string{"cache-control", "no-store"},
	)
	for _, f := range r.headers {
		fields = append(fields, [2]string{strings.ToLower(f.Name), f.Value})
	}
	if _, err := str.Write(quictrace.AppendFrame(nil, quictrace.FrameH3Headers, quictrace.EncodeSection(fields))); err != nil {
		return
	}
	body := r.body
	for first := true; len(body) > 0; first = false {
		n := len(body)
		if r.dripChunk > 0 {
			n = min(n, r.dripChunk)
			if !first {
				time.Sleep(r.dripEvery)
			}
		}
		if _, err := str.Write(quictrace.AppendFrame(nil, quictrace.FrameH3Data, body[:n])); err != nil {
			return
		}
		body = body[n:]
	}
	if err := str.Close(); err != nil {
		return
	}
	if r.closeAfter {
		h.goAway()
	}
}

// goAway sends GOAWAY, so the client makes no more requests here, and
// closes the connection (H3_NO_ERROR) a moment later.
func (h *h3conn) goAway() {
	h.wmu.Lock()
	_, err := h.control.Write(quictrace.AppendFrame(nil, quictrace.FrameH3GoAway, quictrace.AppendVarint(nil, h.maxStream+4)))
	h.wmu.Unlock()
	if err != nil {
		return
	}
	time.AfterFunc(h3GoAwayGrace, func() {
		_ = h.conn.CloseWithError(h3NoError, "")
	})
}

// recordClose records how the connection ended.
func (h *h3conn) recordClose(cause error) {
	c := &quictrace.Close{
		By:     "server",
		Millis: h.millis(),
	}
	var idle *quic.IdleTimeoutError
	var app *quic.ApplicationError
	var tr *quic.TransportError
	switch {
	case errors.As(cause, &idle):
		c.By = "idle"
	case errors.As(cause, &app):
		if app.Remote {
			c.By = "client"
		}
	case errors.As(cause, &tr):
		if tr.Remote {
			c.By = "client"
		}
	}
	if cause != nil {
		c.Error = cause.Error()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = c
}

// snapshot copies what the connection recorded so far.
func (h *h3conn) snapshot() (*quictrace.H3, *quictrace.Close) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec := &quictrace.H3{
		Streams:  slices.Clone(h.rec.Streams),
		Requests: slices.Clone(h.rec.Requests),
	}
	for i := range rec.Streams {
		s := &rec.Streams[i]
		s.Frames, s.Encoder, s.Decoder = slices.Clone(s.Frames), slices.Clone(s.Encoder), slices.Clone(s.Decoder)
	}
	for i := range rec.Requests {
		rec.Requests[i].Frames = slices.Clone(rec.Requests[i].Frames)
	}
	var closed *quictrace.Close
	if h.closed != nil {
		closed = new(*h.closed)
	}
	return rec, closed
}

// h3Server is how a scenario serves HTTP/3: the SETTINGS the server sends,
// in order.
type h3Server struct {
	settings [][2]uint64
}

// setting is the value of one of the server's settings, 0 if it sends
// none.
func (s *h3Server) setting(id uint64) uint64 {
	for _, st := range s.settings {
		if st[0] == id {
			return st[1]
		}
	}
	return 0
}

// HTTP3 reports a scenario that also serves HTTP/3, on UDP at its TLS
// port (Server.PacketConn).
func (sc *Scenario) HTTP3() bool {
	return sc.h3 != nil
}

// H3Settings are the SETTINGS the scenario's HTTP/3 server sends, in
// order (id, value); none without HTTP/3.
func (sc *Scenario) H3Settings() [][2]uint64 {
	if sc.h3 == nil {
		return nil
	}
	return slices.Clone(sc.h3.settings)
}

// altSvc advertises HTTP/3 on port, for a day.
func altSvc(port string) hpack.HeaderField {
	return hpack.HeaderField{
		Name:  "alt-svc",
		Value: `h3=":` + port + `"; ma=86400`,
	}
}
