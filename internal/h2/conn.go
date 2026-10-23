package h2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// ErrConnUnusable is returned for a request that never started because the
// connection was closed or went away. Such a request is safe to retry on a
// new connection.
var ErrConnUnusable = errors.New("h2: connection unusable")

const (
	defaultWindow    = 65535
	defaultFrameSize = 16384
	defaultTableSize = 4096
)

// Conn is a client HTTP/2 connection that opens like the profile's browser.
// It is safe for concurrent use.
type Conn struct {
	conn    net.Conn
	fr      *http2.Framer
	headers []profile.Header
	h2      *profile.HTTP2

	// wmu serializes frame writes and guards the HPACK encoder. Lock order:
	// wmu may be held while taking mu, never the reverse.
	wmu   sync.Mutex
	hbuf  bytes.Buffer
	hpack *hpack.Encoder

	mu   sync.Mutex
	cond *sync.Cond // broadcast when windows, streams or state change
	// streams in flight, by ID; a stream gets its ID when its HEADERS frame is
	// written, because IDs must increase in the order streams open.
	streams map[uint32]*stream
	nextID  uint32
	// active counts started streams, with or without an ID yet, against the
	// peer's MAX_CONCURRENT_STREAMS.
	active uint32
	// err is set once the connection is dead; no new streams start.
	err error
	// goAway is set when the server sent GOAWAY; no new streams start.
	goAway bool
	// Peer settings.
	maxFrameSize  uint32
	initialWindow int32
	maxStreams    uint32
	// sendWindow is the connection-level window for our DATA.
	sendWindow int32
	// Receive flow control: our advertised windows and bytes consumed by
	// readers that have not been returned with WINDOW_UPDATE yet.
	recvStreamWindow uint32
	recvConnWindow   uint32
	recvConnUnacked  uint32
}

type stream struct {
	id         uint32 // 0 until the HEADERS frame is written
	sendWindow int32
	// resp delivers the response headers, or an error, once.
	resp chan responseOrError
	body *pipe
	// unacked counts consumed body bytes not yet returned by WINDOW_UPDATE.
	unacked uint32
	// delivered is set once the response headers went to RoundTrip; later
	// HEADERS frames are trailers.
	delivered bool
	done      bool
}

type responseOrError struct {
	resp *http.Response
	err  error
}

// New opens an HTTP/2 connection on conn, which must have negotiated "h2":
// it sends the preface, then the profile's SETTINGS, connection
// WINDOW_UPDATE and PRIORITY frames, in that order, as the browser does.
func New(conn net.Conn, h2 *profile.HTTP2, headers []profile.Header) (*Conn, error) {
	c := &Conn{
		conn:             conn,
		fr:               http2.NewFramer(conn, conn),
		headers:          headers,
		h2:               h2,
		streams:          map[uint32]*stream{},
		nextID:           1,
		maxFrameSize:     defaultFrameSize,
		initialWindow:    defaultWindow,
		maxStreams:       100,
		sendWindow:       defaultWindow,
		recvStreamWindow: defaultWindow,
		recvConnWindow:   defaultWindow + h2.WindowUpdate,
	}
	c.cond = sync.NewCond(&c.mu)
	tableSize := uint32(defaultTableSize)
	settings := make([]http2.Setting, len(h2.Settings))
	for i, s := range h2.Settings {
		settings[i] = http2.Setting{
			ID:  http2.SettingID(s.ID),
			Val: s.Value,
		}
		switch http2.SettingID(s.ID) {
		case http2.SettingInitialWindowSize:
			c.recvStreamWindow = s.Value
		case http2.SettingHeaderTableSize:
			tableSize = s.Value
		}
	}
	c.hpack = hpack.NewEncoder(&c.hbuf)
	c.fr.ReadMetaHeaders = hpack.NewDecoder(tableSize, nil)
	c.fr.MaxHeaderListSize = 1 << 20

	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		return nil, err
	}
	if err := c.fr.WriteSettings(settings...); err != nil {
		return nil, err
	}
	if h2.WindowUpdate > 0 {
		if err := c.fr.WriteWindowUpdate(0, h2.WindowUpdate); err != nil {
			return nil, err
		}
	}
	for _, p := range h2.Priorities {
		if err := c.fr.WritePriority(p.StreamID, priorityParam(p.PriorityParam)); err != nil {
			return nil, err
		}
	}
	go c.readLoop()
	return c, nil
}

func priorityParam(p profile.PriorityParam) http2.PriorityParam {
	return http2.PriorityParam{
		StreamDep: p.DependsOn,
		Exclusive: p.Exclusive,
		Weight:    p.Weight,
	}
}

// Close closes the connection; in-flight requests fail.
func (c *Conn) Close() error {
	return c.conn.Close()
}

// CanTakeNewRequest reports whether new requests can start on c.
func (c *Conn) CanTakeNewRequest() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err == nil && !c.goAway && c.nextID < 1<<31
}

// Idle reports whether c has no requests in flight.
func (c *Conn) Idle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active == 0
}

// hopByHop are connection-level headers HTTP/2 forbids (RFC 9113, 8.2.2).
var hopByHop = map[string]bool{
	"connection":        true,
	"proxy-connection":  true,
	"keep-alive":        true,
	"transfer-encoding": true,
	"upgrade":           true,
	"host":              true,
}

func skipHeader(name string) bool {
	return hopByHop[name]
}

// RoundTrip sends req on a new stream and returns the response once its
// headers arrive. The body streams as it is read; closing it early resets the
// stream.
func (c *Conn) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	s, err := c.newStream()
	if err != nil {
		return nil, err
	}
	hasBody := req.Body != nil && req.Body != http.NoBody && req.ContentLength != 0
	if err := c.writeHeaders(s, req, !hasBody); err != nil {
		c.closeStream(s, err)
		return nil, err
	}
	if hasBody {
		go func() {
			if err := c.writeBody(ctx, s, req.Body); err != nil {
				c.reset(s, http2.ErrCodeCancel, err)
			}
		}()
	}
	select {
	case r := <-s.resp:
		if r.err != nil {
			return nil, r.err
		}
		r.resp.Request = req
		return r.resp, nil
	case <-ctx.Done():
		c.reset(s, http2.ErrCodeCancel, ctx.Err())
		return nil, ctx.Err()
	}
}

func (c *Conn) newStream() (*stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.err == nil && !c.goAway && c.active >= c.maxStreams {
		c.cond.Wait()
	}
	if c.err != nil || c.goAway || c.nextID >= 1<<31 {
		return nil, ErrConnUnusable
	}
	s := &stream{
		sendWindow: c.initialWindow,
		resp:       make(chan responseOrError, 1),
	}
	s.body = newPipe(func(n int) {
		c.consumed(s, n)
	}, func() {
		// The caller closed the body before the end: stop the stream.
		c.reset(s, http2.ErrCodeCancel, errBodyClosed)
	})
	c.active++
	return s, nil
}

// writeHeaders encodes req's pseudo-headers in the profile's order and its
// headers through headerorder, gives s its stream ID, and writes HEADERS plus
// CONTINUATION frames. Encoding, ID assignment and writing happen under one
// hold of wmu, so HPACK state and stream IDs follow the order frames go out.
func (c *Conn) writeHeaders(s *stream, req *http.Request, endStream bool) error {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	path := req.URL.RequestURI()
	pseudo := map[string]string{
		":method":    req.Method,
		":authority": host,
		":scheme":    req.URL.Scheme,
		":path":      path,
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	if req.ContentLength > 0 && h.Get("Content-Length") == "" {
		h.Set("Content-Length", strconv.FormatInt(req.ContentLength, 10))
	}
	fields := headerorder.Build(c.headers, h, skipHeader)

	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.hbuf.Reset()
	for _, name := range c.h2.PseudoHeaderOrder {
		if err := c.hpack.WriteField(hpack.HeaderField{
			Name:  name,
			Value: pseudo[name],
		}); err != nil {
			return err
		}
	}
	for _, f := range fields {
		if err := c.hpack.WriteField(hpack.HeaderField{
			Name:  f.Name,
			Value: f.Value,
		}); err != nil {
			return err
		}
	}
	block := c.hbuf.Bytes()
	c.mu.Lock()
	if c.err != nil || c.goAway || c.nextID >= 1<<31 {
		c.mu.Unlock()
		return ErrConnUnusable
	}
	id := c.nextID
	c.nextID += 2
	s.id = id
	c.streams[id] = s
	frameSize := int(c.maxFrameSize)
	c.mu.Unlock()
	first := block
	if len(first) > frameSize {
		first = block[:frameSize]
	}
	param := http2.HeadersFrameParam{
		StreamID:      id,
		BlockFragment: first,
		EndStream:     endStream,
		EndHeaders:    len(first) == len(block),
	}
	if c.h2.HeadersPriority != nil {
		param.Priority = priorityParam(*c.h2.HeadersPriority)
	}
	if err := c.fr.WriteHeaders(param); err != nil {
		return err
	}
	for rest := block[len(first):]; len(rest) > 0; {
		chunk := rest
		if len(chunk) > frameSize {
			chunk = rest[:frameSize]
		}
		rest = rest[len(chunk):]
		if err := c.fr.WriteContinuation(id, len(rest) == 0, chunk); err != nil {
			return err
		}
	}
	return nil
}

// writeBody sends body as DATA frames within the peer's flow-control windows,
// ending the stream after the last one.
func (c *Conn) writeBody(ctx context.Context, s *stream, body io.ReadCloser) error {
	defer func() {
		_ = body.Close()
	}()
	buf := make([]byte, defaultFrameSize)
	for {
		n, readErr := body.Read(buf)
		data := buf[:n]
		for len(data) > 0 {
			allowed, err := c.reserveSend(ctx, s, len(data))
			if err != nil {
				return err
			}
			c.wmu.Lock()
			err = c.fr.WriteData(s.id, false, data[:allowed])
			c.wmu.Unlock()
			if err != nil {
				return err
			}
			data = data[allowed:]
		}
		if errors.Is(readErr, io.EOF) {
			c.wmu.Lock()
			defer c.wmu.Unlock()
			return c.fr.WriteData(s.id, true, nil)
		}
		if readErr != nil {
			return readErr
		}
	}
}

// reserveSend waits until both windows allow some data and takes up to want
// bytes from them.
func (c *Conn) reserveSend(ctx context.Context, s *stream, want int) (int, error) {
	stop := context.AfterFunc(ctx, func() {
		c.mu.Lock()
		c.cond.Broadcast()
		c.mu.Unlock()
	})
	defer stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if c.err != nil {
			return 0, c.err
		}
		if s.done {
			return 0, errors.New("h2: stream closed")
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n := min(int32(want), s.sendWindow, c.sendWindow, int32(c.maxFrameSize)) //nolint:gosec // want is at most one read buffer
		if n > 0 {
			s.sendWindow -= n
			c.sendWindow -= n
			return int(n), nil
		}
		c.cond.Wait()
	}
}

// consumed returns flow-control credit once a reader has taken n body bytes,
// in WINDOW_UPDATEs of at least half a window, as browsers batch them.
func (c *Conn) consumed(s *stream, n int) {
	c.mu.Lock()
	s.unacked += uint32(n)         //nolint:gosec // n is a read length
	c.recvConnUnacked += uint32(n) //nolint:gosec // n is a read length
	var streamInc, connInc uint32
	if !s.done && s.unacked >= c.recvStreamWindow/2 {
		streamInc, s.unacked = s.unacked, 0
	}
	if c.recvConnUnacked >= c.recvConnWindow/2 {
		connInc, c.recvConnUnacked = c.recvConnUnacked, 0
	}
	c.mu.Unlock()
	if streamInc == 0 && connInc == 0 {
		return
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if streamInc > 0 {
		_ = c.fr.WriteWindowUpdate(s.id, streamInc)
	}
	if connInc > 0 {
		_ = c.fr.WriteWindowUpdate(0, connInc)
	}
}

// reset sends RST_STREAM and fails the stream.
func (c *Conn) reset(s *stream, code http2.ErrCode, err error) {
	c.mu.Lock()
	send := !s.done && s.id != 0
	id := s.id
	c.mu.Unlock()
	if send {
		c.wmu.Lock()
		_ = c.fr.WriteRSTStream(id, code)
		c.wmu.Unlock()
	}
	c.closeStream(s, err)
}

// closeStream removes s and ends its body and response with err (io.EOF for
// a normal end).
func (c *Conn) closeStream(s *stream, err error) {
	c.mu.Lock()
	if !s.done {
		s.done = true
		if s.id != 0 {
			delete(c.streams, s.id)
		}
		c.active--
		c.cond.Broadcast()
	}
	c.mu.Unlock()
	select {
	case s.resp <- responseOrError{
		err: err,
	}:
	default:
	}
	s.body.closeWithError(err)
}

func (c *Conn) readLoop() {
	err := c.read()
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = ErrConnUnusable
	}
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	streams := make([]*stream, 0, len(c.streams))
	for _, s := range c.streams {
		streams = append(streams, s)
	}
	c.cond.Broadcast()
	c.mu.Unlock()
	for _, s := range streams {
		c.closeStream(s, err)
	}
	_ = c.conn.Close()
}

func (c *Conn) read() error {
	for {
		f, err := c.fr.ReadFrame()
		if err != nil {
			return err
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if f.IsAck() {
				continue
			}
			if err := c.applySettings(f); err != nil {
				return err
			}
		case *http2.WindowUpdateFrame:
			c.mu.Lock()
			if f.StreamID == 0 {
				c.sendWindow += int32(f.Increment) //nolint:gosec // increments are at most 2^31-1
			} else if s := c.streams[f.StreamID]; s != nil {
				s.sendWindow += int32(f.Increment) //nolint:gosec // increments are at most 2^31-1
			}
			c.cond.Broadcast()
			c.mu.Unlock()
		case *http2.MetaHeadersFrame:
			c.onHeaders(f)
		case *http2.DataFrame:
			if err := c.onData(f); err != nil {
				return err
			}
		case *http2.RSTStreamFrame:
			if s := c.stream(f.StreamID); s != nil {
				c.closeStream(s, http2.StreamError{
					StreamID: f.StreamID,
					Code:     f.ErrCode,
				})
			}
		case *http2.PingFrame:
			if !f.IsAck() {
				c.wmu.Lock()
				err := c.fr.WritePing(true, f.Data)
				c.wmu.Unlock()
				if err != nil {
					return err
				}
			}
		case *http2.GoAwayFrame:
			c.onGoAway(f)
		}
	}
}

func (c *Conn) stream(id uint32) *stream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streams[id]
}

func (c *Conn) applySettings(f *http2.SettingsFrame) error {
	var tableSize uint32
	haveTableSize := false
	c.mu.Lock()
	err := f.ForeachSetting(func(s http2.Setting) error {
		switch s.ID {
		case http2.SettingMaxFrameSize:
			c.maxFrameSize = s.Val
		case http2.SettingMaxConcurrentStreams:
			c.maxStreams = s.Val
		case http2.SettingInitialWindowSize:
			delta := int32(s.Val) - c.initialWindow //nolint:gosec // validated by the framer (at most 2^31-1)
			c.initialWindow = int32(s.Val)          //nolint:gosec // validated by the framer
			for _, st := range c.streams {
				st.sendWindow += delta
			}
		case http2.SettingHeaderTableSize:
			tableSize, haveTableSize = s.Val, true
		}
		return nil
	})
	c.cond.Broadcast()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	// Lock order is wmu then mu, never mu then wmu (writeHeaders holds wmu
	// while reading the frame size), so the encoder is updated after mu is
	// released.
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if haveTableSize {
		c.hpack.SetMaxDynamicTableSizeLimit(tableSize)
	}
	return c.fr.WriteSettingsAck()
}

func (c *Conn) onHeaders(f *http2.MetaHeadersFrame) {
	s := c.stream(f.StreamID)
	if s == nil {
		return
	}
	c.mu.Lock()
	trailers := s.delivered
	c.mu.Unlock()
	if trailers {
		// Trailers carry no :status; they are not exposed, but may end the
		// stream.
		if f.StreamEnded() {
			c.closeStream(s, io.EOF)
		}
		return
	}
	status := f.PseudoValue("status")
	code, err := strconv.Atoi(status)
	if err != nil {
		c.reset(s, http2.ErrCodeProtocol, fmt.Errorf("h2: bad :status %q", status))
		return
	}
	if code >= 100 && code < 200 {
		// Informational; the final response follows.
		return
	}
	h := make(http.Header, len(f.Fields))
	for _, field := range f.RegularFields() {
		key := http.CanonicalHeaderKey(field.Name)
		h[key] = append(h[key], field.Value)
	}
	resp := &http.Response{
		Status:        status + " " + http.StatusText(code),
		StatusCode:    code,
		Proto:         "HTTP/2.0",
		ProtoMajor:    2,
		Header:        h,
		ContentLength: -1,
		Body:          s.body,
	}
	if cl := h.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			resp.ContentLength = n
		}
	}
	c.mu.Lock()
	s.delivered = true
	c.mu.Unlock()
	s.resp <- responseOrError{
		resp: resp,
	}
	if f.StreamEnded() {
		c.closeStream(s, io.EOF)
	}
}

func (c *Conn) onData(f *http2.DataFrame) error {
	// Flow control counts the whole frame, padding included.
	size := f.Length
	s := c.stream(f.StreamID)
	if s == nil {
		// A stream we already closed: still return the connection credit.
		c.mu.Lock()
		c.recvConnUnacked += size
		var inc uint32
		if c.recvConnUnacked >= c.recvConnWindow/2 {
			inc, c.recvConnUnacked = c.recvConnUnacked, 0
		}
		c.mu.Unlock()
		if inc > 0 {
			c.wmu.Lock()
			defer c.wmu.Unlock()
			return c.fr.WriteWindowUpdate(0, inc)
		}
		return nil
	}
	if data := f.Data(); len(data) > 0 {
		s.body.write(data)
	}
	if pad := int(size) - len(f.Data()); pad > 0 {
		c.consumed(s, pad)
	}
	if f.StreamEnded() {
		c.closeStream(s, io.EOF)
	}
	return nil
}

func (c *Conn) onGoAway(f *http2.GoAwayFrame) {
	c.mu.Lock()
	c.goAway = true
	var unstarted []*stream
	for id, s := range c.streams {
		if id > f.LastStreamID {
			unstarted = append(unstarted, s)
		}
	}
	c.cond.Broadcast()
	c.mu.Unlock()
	for _, s := range unstarted {
		c.closeStream(s, ErrConnUnusable)
	}
}
