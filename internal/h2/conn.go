package h2

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"
	xhpack "golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/hpack"
	"github.com/roachadam/mimicry/profile"
)

// ErrConnUnusable is returned for a request the peer cannot have processed:
// the connection was closed or going away (GOAWAY) before its HEADERS went
// out in full, or the peer refused its stream (GOAWAY past it,
// REFUSED_STREAM) before its body was read. Such a request is safe to retry
// on another connection, whatever its method, with the same body: RoundTrip
// has neither read nor closed it. A connection that returned it takes no new
// requests and closes itself once its last stream ends.
var ErrConnUnusable = errors.New("h2: connection unusable")

var (
	// errConnLost fails requests in flight when the connection dies. They
	// may have been processed, so unlike ErrConnUnusable they are not
	// retried.
	errConnLost = errors.New("h2: connection lost with the request in flight")
	// errRefusedAfterBody fails a refused request whose body was already
	// read: it cannot be sent again.
	errRefusedAfterBody = errors.New("h2: stream refused after its request body was read")
	errStreamClosed     = errors.New("h2: stream closed")
	errBodyTooLong      = errors.New("h2: request body longer than its ContentLength")
	errBodyTooShort     = errors.New("h2: request body shorter than its ContentLength")
	errHeaderListSize   = errors.New("h2: response header list larger than advertised")
)

const (
	defaultWindow    = 65535
	defaultFrameSize = 16384
	defaultTableSize = 4096
	// defaultHeaderListSize bounds a response's header list when the
	// profile advertises no SETTINGS_MAX_HEADER_LIST_SIZE.
	defaultHeaderListSize = 1 << 20
	// maxWindow is the largest flow-control window (RFC 9113, 6.9.1).
	maxWindow = 1<<31 - 1
	// uploadChunk is how much of a request body is read at a time. DATA
	// frames are cut from each chunk, so with Chrome's 16375-byte cap a
	// chunk goes out as 16375×4+35, as Chrome and Safari send theirs.
	uploadChunk = 65535
	// lingerTimeout bounds how long a fully read, closed response waits for
	// the peer's END_STREAM before the stream is reset.
	lingerTimeout = 10 * time.Second
	// goAwayTimeout bounds how long a connection that failed waits to write
	// its GOAWAY before it closes anyway.
	goAwayTimeout = time.Second
)

// Conn is a client HTTP/2 connection that opens like the profile's browser.
// It is safe for concurrent use.
type Conn struct {
	conn    net.Conn
	fr      *http2.Framer
	headers []profile.Header
	h2      *profile.HTTP2
	clock   clockwork.Clock
	// done is closed once the read loop has finished and the connection is
	// closed.
	done chan struct{}

	// wmu serializes frame writes and guards the HPACK encoder. Lock order:
	// wmu may be held while taking mu, never the reverse.
	wmu    sync.Mutex
	henc   *hpack.Encoder
	hblock []byte

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
	// draining is set once no new stream may start but the streams in
	// flight run on: after GOAWAY or REFUSED_STREAM, or with stream IDs used
	// up. The connection closes when the last one ends.
	draining bool
	// Peer settings.
	maxFrameSize  uint32
	initialWindow int32
	maxStreams    uint32
	// connectProtocol is the peer's SETTINGS_ENABLE_CONNECT_PROTOCOL:
	// extended CONNECT (RFC 8441) may open WebSockets on c.
	connectProtocol bool
	// sendWindow is the connection-level window for our DATA.
	sendWindow int32
	// Receive flow control: our advertised windows and bytes consumed by
	// readers that have not been returned with WINDOW_UPDATE yet, returned
	// once they reach the thresholds. recvConnAvail is what the peer may
	// still send on the connection; more is a FLOW_CONTROL_ERROR.
	recvStreamWindow uint32
	recvConnWindow   uint32
	recvConnUnacked  uint32
	recvConnAvail    int64
	streamThreshold  uint32
	connThreshold    uint32
	// Chromium's timers (profile.HTTP2 WindowUpdateIntervalMillis and
	// IdlePingMillis): credit is also returned once updateInterval has
	// passed since the last WINDOW_UPDATE, and a request that starts after
	// idlePing without reading anything sends a PING, whose ACK must come
	// back within idlePing or the connection is closed.
	updateInterval time.Duration
	connLastUpdate time.Time
	idlePing       time.Duration
	lastRead       time.Time
	pingCount      uint64
	// pingTimer is set while a PING awaits its ACK.
	pingTimer clockwork.Timer

	// onHeadersHook, set by tests only, runs in the read loop after a
	// response's HEADERS found its stream and before the response is
	// delivered.
	onHeadersHook func()
}

type stream struct {
	id         uint32 // 0 until the HEADERS frame is written
	sendWindow int32
	// recvWindow is what the peer may still send on the stream; more is a
	// FLOW_CONTROL_ERROR.
	recvWindow int64
	// resp delivers the response headers, or an error, once: by whoever
	// sets delivered.
	resp chan responseOrError
	body *pipe
	// unacked counts consumed body bytes not yet returned by WINDOW_UPDATE;
	// lastUpdate is when credit was last returned (or the stream opened).
	unacked    uint32
	lastUpdate time.Time
	// delivered is set once resp was sent: the response headers went to
	// RoundTrip (later HEADERS frames are trailers) or the stream ended.
	delivered bool
	// bodyStarted is set once the request body is being read: a refusal
	// after that cannot be retried with the same body.
	bodyStarted bool
	// done is set when the stream ended, with err (io.EOF for a normal end).
	done bool
	err  error
	// linger resets the stream if the peer never sends END_STREAM after the
	// caller read the whole body and closed it.
	linger clockwork.Timer
	// stopCtx stops the request context from resetting the stream.
	stopCtx func() bool
}

type responseOrError struct {
	resp *http.Response
	err  error
}

// ending is how a stream ends: with err, after sending RST_STREAM with code
// if rst is set.
type ending struct {
	err  error
	rst  bool
	code http2.ErrCode
}

// New opens an HTTP/2 connection on conn, which must have negotiated "h2",
// for profile p (which must have HTTP2): it sends the preface, then the
// profile's SETTINGS, connection WINDOW_UPDATE and PRIORITY frames, in that
// order, as the browser does. clock drives the profile's timers.
func New(conn net.Conn, p *profile.Profile, clock clockwork.Clock) (*Conn, error) {
	h2 := p.HTTP2
	firstID := h2.FirstStreamID
	if firstID == 0 {
		firstID = 1
	}
	now := clock.Now()
	c := &Conn{
		conn:             conn,
		fr:               http2.NewFramer(conn, conn),
		headers:          p.Headers,
		h2:               h2,
		clock:            clock,
		done:             make(chan struct{}),
		henc:             hpack.NewEncoder(h2.HPACK),
		streams:          map[uint32]*stream{},
		nextID:           firstID,
		maxFrameSize:     defaultFrameSize,
		initialWindow:    defaultWindow,
		maxStreams:       100,
		sendWindow:       defaultWindow,
		recvStreamWindow: defaultWindow,
		recvConnWindow:   defaultWindow + h2.WindowUpdate,
		updateInterval:   time.Duration(h2.WindowUpdateIntervalMillis) * time.Millisecond,
		connLastUpdate:   now,
		idlePing:         time.Duration(h2.IdlePingMillis) * time.Millisecond,
		lastRead:         now,
	}
	c.recvConnAvail = int64(c.recvConnWindow)
	c.cond = sync.NewCond(&c.mu)
	tableSize := uint32(defaultTableSize)
	readFrameSize := uint32(defaultFrameSize)
	headerListSize := uint32(defaultHeaderListSize)
	for _, s := range h2.Settings {
		switch http2.SettingID(s.ID) {
		case http2.SettingInitialWindowSize:
			c.recvStreamWindow = s.Value
		case http2.SettingHeaderTableSize:
			tableSize = s.Value
		case http2.SettingMaxFrameSize:
			readFrameSize = s.Value
		case http2.SettingMaxHeaderListSize:
			headerListSize = s.Value
		}
	}
	c.recvStreamWindow += h2.StreamWindowUpdate
	c.streamThreshold = h2.StreamUpdateThreshold
	if c.streamThreshold == 0 {
		c.streamThreshold = c.recvStreamWindow / 2
	}
	c.connThreshold = h2.ConnectionUpdateThreshold
	if c.connThreshold == 0 {
		c.connThreshold = c.recvConnWindow / 2
	}
	// The peer is held to what we advertise.
	c.fr.SetMaxReadFrameSize(readFrameSize)
	c.fr.ReadMetaHeaders = xhpack.NewDecoder(tableSize, nil)
	c.fr.MaxHeaderListSize = headerListSize

	if err := writePreface(conn, c.fr, h2); err != nil {
		return nil, err
	}
	go c.readLoop()
	return c, nil
}

// writePreface writes the client preface and the profile's SETTINGS,
// connection WINDOW_UPDATE and PRIORITY frames, in that order.
func writePreface(conn net.Conn, fr *http2.Framer, h2 *profile.HTTP2) error {
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		return err
	}
	settings := make([]http2.Setting, len(h2.Settings))
	for i, s := range h2.Settings {
		settings[i] = http2.Setting{
			ID:  http2.SettingID(s.ID),
			Val: s.Value,
		}
	}
	if err := fr.WriteSettings(settings...); err != nil {
		return err
	}
	if h2.WindowUpdate > 0 {
		if err := fr.WriteWindowUpdate(0, h2.WindowUpdate); err != nil {
			return err
		}
	}
	for _, p := range h2.Priorities {
		if err := fr.WritePriority(p.StreamID, priorityParam(p.PriorityParam)); err != nil {
			return err
		}
	}
	return nil
}

// Decline opens an HTTP/2 connection on conn, which must have negotiated
// "h2", only to close it: it sends what New sends, then GOAWAY (NO_ERROR,
// no streams), and closes conn without acknowledging the server's
// SETTINGS. Firefox does this to a connection it no longer needs once its
// handshake is done, because another one serves the host
// (profile.CoalesceOnConnect). conn is closed on errors too.
//
// The close waits, in the background, until what the server sent has been
// read (at most declineLinger): closing with unread data resets the
// connection, and a reset can overtake the frames above, which the server
// then never sees. Every Firefox sample shows them all arriving; how
// Firefox's own close looks on TCP is not captured.
func Decline(conn net.Conn, p *profile.Profile) error {
	fr := http2.NewFramer(conn, conn)
	err := writePreface(conn, fr, p.HTTP2)
	if err == nil {
		err = fr.WriteGoAway(0, http2.ErrCodeNo, nil)
	}
	if err != nil {
		_ = conn.Close()
		return err
	}
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(declineLinger))
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
	}()
	return nil
}

// declineLinger bounds how long a declined connection reads before it
// closes.
const declineLinger = time.Second

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

// GoAway sends GOAWAY (NO_ERROR, no server streams) and takes no more
// requests: those in flight run to completion and the connection closes
// after the last, or, idle, once the server has had declineLinger to
// read the GOAWAY. Firefox does this to an origin's HTTP/2 connection
// once its HTTP/3 connection is up (profile http3.discovery.goaway_tcp).
func (c *Conn) GoAway() {
	c.mu.Lock()
	if c.err != nil || c.draining {
		c.mu.Unlock()
		return
	}
	c.draining = true
	idle := c.active == 0
	// Counted as a stream until the frame is written: the last stream
	// ending meanwhile must not close the connection under it (endStream
	// closes a draining connection once nothing is active).
	c.active++
	c.cond.Broadcast()
	c.mu.Unlock()
	c.wmu.Lock()
	_ = c.fr.WriteGoAway(0, http2.ErrCodeNo, nil)
	c.wmu.Unlock()
	c.mu.Lock()
	c.active--
	ended := c.active == 0
	c.mu.Unlock()
	switch {
	case idle:
		c.clock.AfterFunc(declineLinger, func() {
			_ = c.conn.Close()
		})
	case ended:
		// The streams in flight ended while it was written.
		_ = c.conn.Close()
	}
}

// Done is closed once the connection has finished: closed by either side,
// failed, or drained after GOAWAY.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// CanTakeNewRequest reports whether new requests can start on c.
func (c *Conn) CanTakeNewRequest() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err == nil && !c.draining
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
// stream. The request context bounds the whole exchange, body reads
// included. req.Body is closed, except with ErrConnUnusable, which leaves it
// unread for the retry.
func (c *Conn) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	hasBody := req.Body != nil && req.Body != http.NoBody
	// As in net/http, a body with ContentLength 0 has an unknown length (-1).
	var length int64
	if hasBody {
		length = req.ContentLength
		if length <= 0 {
			length = -1
		}
	}
	head, err := c.head(req, length, "")
	if err != nil {
		closeBody(req)
		return nil, err
	}
	s, err := c.newStream(ctx)
	if err != nil {
		if !errors.Is(err, ErrConnUnusable) {
			closeBody(req)
		}
		return nil, err
	}
	if err := c.writeHeaders(s, head); err != nil {
		c.endStream(s, ending{
			err: err,
		})
		// The first ending wins: a cancel may have come first.
		r := <-s.resp
		if !errors.Is(r.err, ErrConnUnusable) {
			closeBody(req)
		}
		return nil, r.err
	}
	if hasBody {
		go c.sendBody(ctx, s, req.Body, length)
	}
	r := <-s.resp
	if r.err != nil {
		return nil, r.err
	}
	r.resp.Request = req
	return r.resp, nil
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

// requestHead is a request's header list, in the order it is sent, and how
// its HEADERS frame goes out.
type requestHead struct {
	fields []hpack.Field
	// weight overrides the profile's HEADERS weight when not zero.
	weight    uint8
	endStream bool
}

// head builds req's header list: pseudo-headers in the profile's order,
// then its headers through headerorder. length is the body's: 0 for none,
// -1 when unknown. A protocol makes it an extended CONNECT (RFC 8441):
// :method CONNECT, with :protocol after the profile's pseudo-headers, as
// every browser places it, and the stream left open. A method,
// authority, path or header field that is not valid on the wire is an
// error, as in net/http.
func (c *Conn) head(req *http.Request, length int64, protocol string) (requestHead, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	if protocol != "" {
		method = http.MethodConnect
	}
	if !httpguts.ValidHeaderFieldName(method) {
		return requestHead{}, fmt.Errorf("h2: invalid method %q", method)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	// "h:" is "h" with the scheme's default port, as browsers write it.
	host = strings.TrimSuffix(host, ":")
	if !httpguts.ValidHostHeader(host) {
		return requestHead{}, fmt.Errorf("h2: invalid host %q", host)
	}
	path := req.URL.RequestURI()
	if (!strings.HasPrefix(path, "/") && path != "*") || !httpguts.ValidHeaderFieldValue(path) {
		return requestHead{}, fmt.Errorf("h2: invalid path %q", path)
	}
	pseudo := map[string]string{
		":method":    method,
		":authority": host,
		":scheme":    req.URL.Scheme,
		":path":      path,
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	plan := headerorder.PlanFrom(req.Context())
	if length != 0 {
		// A body's length is the one declared, never the caller's header.
		for name := range h {
			if strings.EqualFold(name, "Content-Length") {
				delete(h, name)
			}
		}
		if length > 0 && (plan == nil || !plan.NoContentLength) {
			h.Set("Content-Length", strconv.FormatInt(length, 10))
		} else {
			// Unknown, or not sent by the browser: no length is
			// declared, not even a profile default.
			h["Content-Length"] = nil
		}
	}
	list := c.headers
	head := requestHead{
		endStream: length == 0 && protocol == "",
	}
	var table *profile.HeaderTable
	if plan != nil {
		list, table = plan.Headers, plan.Table
		head.weight = plan.Weight
	}
	ordered := headerorder.Build(list, h, skipHeader, table)
	head.fields = make([]hpack.Field, 0, len(c.h2.PseudoHeaderOrder)+1+len(ordered))
	for _, name := range c.h2.PseudoHeaderOrder {
		head.fields = append(head.fields, hpack.Field{
			Name:  name,
			Value: pseudo[name],
		})
	}
	if protocol != "" {
		head.fields = append(head.fields, hpack.Field{
			Name:  ":protocol",
			Value: protocol,
		})
	}
	for _, f := range ordered {
		if !httpguts.ValidHeaderFieldName(f.Name) {
			return requestHead{}, fmt.Errorf("h2: invalid header field name %q", f.Name)
		}
		if !httpguts.ValidHeaderFieldValue(f.Value) {
			// The value is not quoted: it may be a secret.
			return requestHead{}, fmt.Errorf("h2: invalid value for header field %q", f.Name)
		}
		if f.Name == "cookie" {
			crumbs := headerorder.Crumbs(f.Value)
			if c.h2.SplitCookie {
				// One field per cookie (RFC 9113, 8.2.3), as browsers send
				// them.
				for _, crumb := range crumbs {
					head.fields = append(head.fields, hpack.Field{
						Name:  f.Name,
						Value: crumb,
					})
				}
				continue
			}
			f.Value = strings.Join(crumbs, "; ")
		}
		head.fields = append(head.fields, hpack.Field{
			Name:  f.Name,
			Value: f.Value,
		})
	}
	return head, nil
}

// newStream waits, as long as ctx allows, for the peer's
// MAX_CONCURRENT_STREAMS to leave room, then starts a stream that ctx
// resets when it ends.
func (c *Conn) newStream(ctx context.Context) (*stream, error) {
	stop := context.AfterFunc(ctx, c.broadcast)
	defer stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if c.err != nil || c.draining {
			return nil, ErrConnUnusable
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.active < c.maxStreams {
			break
		}
		c.cond.Wait()
	}
	s := &stream{
		resp:       make(chan responseOrError, 1),
		recvWindow: int64(c.recvStreamWindow),
	}
	s.body = newPipe(func(n int) {
		c.consumed(s, n)
	}, func(complete bool, discarded int) {
		c.bodyClosed(s, complete, discarded)
	})
	s.stopCtx = context.AfterFunc(ctx, func() {
		c.reset(s, http2.ErrCodeCancel, ctx.Err())
	})
	c.active++
	return s, nil
}

func (c *Conn) broadcast() {
	c.mu.Lock()
	c.cond.Broadcast()
	c.mu.Unlock()
}

// writeHeaders gives s its stream ID and writes head as HEADERS plus
// CONTINUATION frames, then the profile's per-stream WINDOW_UPDATE. ID
// assignment, encoding and writing happen under one hold of wmu, so HPACK
// state and stream IDs follow the order frames go out.
func (c *Conn) writeHeaders(s *stream, head requestHead) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	if s.done {
		// Cancelled before it was sent.
		err := s.err
		c.mu.Unlock()
		return err
	}
	if c.err != nil || c.draining {
		c.mu.Unlock()
		return ErrConnUnusable
	}
	id := c.nextID
	c.nextID += 2
	if c.nextID >= 1<<31 {
		// No ID is left for another stream.
		c.draining = true
	}
	// Chromium's parent: the most recently opened stream still open.
	var parent uint32
	for open := range c.streams {
		parent = max(parent, open)
	}
	s.id = id
	s.lastUpdate = c.clock.Now()
	// The window is the one in force when the stream joins c.streams, so a
	// SETTINGS change before then is not lost: from here on, SETTINGS
	// adjust it with the other open streams.
	s.sendWindow = c.initialWindow
	c.streams[id] = s
	frameSize := int(c.maxFrameSize)
	c.mu.Unlock()

	c.hblock = c.henc.AppendBlock(c.hblock[:0], head.fields)
	block := c.hblock
	first := block
	if len(first) > frameSize {
		first = block[:frameSize]
	}
	param := http2.HeadersFrameParam{
		StreamID:      id,
		BlockFragment: first,
		EndStream:     head.endStream,
		EndHeaders:    len(first) == len(block),
	}
	if p := c.h2.HeadersPriority; p != nil {
		param.Priority = priorityParam(*p)
		if head.weight != 0 {
			// As on the wire, like the profile's (captured from frames).
			param.Priority.Weight = head.weight
		}
		if c.h2.ChainPriorities {
			param.Priority.StreamDep = parent
			param.Priority.Exclusive = true
		}
	}
	if err := c.fr.WriteHeaders(param); err != nil {
		return c.headersFailed(id, err)
	}
	for rest := block[len(first):]; len(rest) > 0; {
		chunk := rest
		if len(chunk) > frameSize {
			chunk = rest[:frameSize]
		}
		rest = rest[len(chunk):]
		if err := c.fr.WriteContinuation(id, len(rest) == 0, chunk); err != nil {
			return c.headersFailed(id, err)
		}
	}
	// From here the request is out: a failed write closes the connection
	// and the read loop fails the stream as lost.
	if inc := c.h2.StreamWindowUpdate; inc > 0 {
		if c.broken(c.fr.WriteWindowUpdate(id, inc)) != nil {
			return nil
		}
	}
	c.maybePing()
	return nil
}

// headersFailed handles a header block that did not go out in full: the
// peer cannot have processed the request, so it fails with ErrConnUnusable,
// but the HPACK encoder has moved on without the peer, so the connection
// closes. The stream leaves c.streams first, so the read loop does not fail
// it as lost. wmu must be held.
func (c *Conn) headersFailed(id uint32, err error) error {
	c.mu.Lock()
	delete(c.streams, id)
	if c.err == nil {
		c.err = ErrConnUnusable
	}
	c.cond.Broadcast()
	c.mu.Unlock()
	_ = c.conn.Close()
	return fmt.Errorf("%w: %w", ErrConnUnusable, err)
}

// broken closes the connection after a failed write, which may have left
// part of a frame on the wire. It returns err.
func (c *Conn) broken(err error) error {
	if err != nil {
		_ = c.conn.Close()
	}
	return err
}

// maybePing sends Chromium's connection check after a request's HEADERS: a
// PING carrying a counter from 1 when nothing was read for idlePing and no
// PING is in flight. wmu must be held.
func (c *Conn) maybePing() {
	if c.idlePing == 0 {
		return
	}
	c.mu.Lock()
	send := c.pingTimer == nil && c.clock.Since(c.lastRead) >= c.idlePing
	if send {
		c.pingCount++
		c.pingTimer = c.clock.AfterFunc(c.idlePing, c.pingTimedOut)
	}
	count := c.pingCount
	c.mu.Unlock()
	if !send {
		return
	}
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], count)
	_ = c.broken(c.fr.WritePing(false, data))
}

// pingTimedOut closes a connection whose PING went unanswered: it is dead.
func (c *Conn) pingTimedOut() {
	c.mu.Lock()
	hung := c.pingTimer != nil
	c.mu.Unlock()
	if hung {
		_ = c.conn.Close()
	}
}

// sendBody writes the request body on s and from then owns it: it is closed
// once written or failed. A stream that ended before the body was read
// leaves it untouched if it ended with ErrConnUnusable, for the retry.
func (c *Conn) sendBody(ctx context.Context, s *stream, body io.ReadCloser, length int64) {
	c.mu.Lock()
	if s.done {
		err := s.err
		c.mu.Unlock()
		if !errors.Is(err, ErrConnUnusable) {
			_ = body.Close()
		}
		return
	}
	s.bodyStarted = true
	c.mu.Unlock()
	defer func() {
		_ = body.Close()
	}()
	if err := c.writeBody(ctx, s, body, length); err != nil {
		c.reset(s, http2.ErrCodeCancel, err)
	}
}

// writeBody sends body as DATA frames within the peer's flow-control
// windows, one read of up to uploadChunk at a time, cut into frames. With a
// known length, END_STREAM rides on the frame that completes it, as
// browsers send theirs, once the body is checked to have nothing more: one
// longer or shorter than declared fails without sending past the declared
// length. With an unknown length (-1), END_STREAM rides on the data of the
// read that reports EOF, or goes in an empty DATA frame after the last.
func (c *Conn) writeBody(ctx context.Context, s *stream, body io.Reader, length int64) error {
	buf := make([]byte, uploadChunk)
	var sent int64
	for {
		b := buf
		if length >= 0 {
			b = buf[:min(int64(len(buf)), length-sent)]
		}
		n, err := body.Read(b)
		eof := errors.Is(err, io.EOF)
		if err != nil && !eof {
			return err
		}
		sent += int64(n)
		if length >= 0 {
			if sent == length && !eof {
				more, err := hasMore(body)
				if err != nil {
					return err
				}
				if more {
					return errBodyTooLong
				}
				eof = true
			}
			if eof && sent != length {
				return errBodyTooShort
			}
		}
		if err := c.sendData(ctx, s, b[:n], eof); err != nil {
			return err
		}
		if eof {
			return nil
		}
	}
}

// hasMore reports whether body has bytes left, reading at most one.
func hasMore(body io.Reader) (bool, error) {
	var one [1]byte
	for {
		n, err := body.Read(one[:])
		if n > 0 {
			return true, nil
		}
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}

// sendData writes data as DATA frames within the flow-control windows; end
// sets END_STREAM on the last one, an empty frame if data is.
func (c *Conn) sendData(ctx context.Context, s *stream, data []byte, end bool) error {
	if len(data) == 0 && end {
		c.mu.Lock()
		done := s.done
		c.mu.Unlock()
		if done {
			return errStreamClosed
		}
	}
	for len(data) > 0 || end {
		var n int
		if len(data) > 0 {
			var err error
			n, err = c.reserveSend(ctx, s, len(data))
			if err != nil {
				return err
			}
		}
		last := end && n == len(data)
		c.wmu.Lock()
		err := c.broken(c.fr.WriteData(s.id, last, data[:n]))
		c.wmu.Unlock()
		if err != nil {
			return err
		}
		data = data[n:]
		if last {
			return nil
		}
	}
	return nil
}

// reserveSend waits until both windows allow some data and takes up to want
// bytes from them.
func (c *Conn) reserveSend(ctx context.Context, s *stream, want int) (int, error) {
	stop := context.AfterFunc(ctx, c.broadcast)
	defer stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if c.err != nil {
			return 0, c.err
		}
		if s.done {
			return 0, errStreamClosed
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		limit := c.maxFrameSize
		if c.h2.MaxDataFrame > 0 {
			limit = min(limit, c.h2.MaxDataFrame)
		}
		n := min(int32(want), s.sendWindow, c.sendWindow, int32(limit)) //nolint:gosec // want is at most one read buffer; limit at most 2^24-1
		if n > 0 {
			s.sendWindow -= n
			c.sendWindow -= n
			return int(n), nil
		}
		c.cond.Wait()
	}
}

// consumed returns flow-control credit once a reader has taken n body bytes:
// when the profile's thresholds are reached or, with an update interval, when
// that long has passed since the last WINDOW_UPDATE, as browsers do. Like
// Chrome, the connection's update goes before the stream's.
func (c *Conn) consumed(s *stream, n int) {
	now := c.clock.Now()
	c.mu.Lock()
	s.unacked += uint32(n)         //nolint:gosec // n is a read length
	c.recvConnUnacked += uint32(n) //nolint:gosec // n is a read length
	var streamInc, connInc uint32
	if !s.done && c.due(s.unacked, c.streamThreshold, s.lastUpdate, now) {
		streamInc, s.unacked, s.lastUpdate = s.unacked, 0, now
		s.recvWindow += int64(streamInc)
	}
	if c.due(c.recvConnUnacked, c.connThreshold, c.connLastUpdate, now) {
		connInc, c.recvConnUnacked, c.connLastUpdate = c.recvConnUnacked, 0, now
		c.recvConnAvail += int64(connInc)
	}
	c.mu.Unlock()
	if streamInc == 0 && connInc == 0 {
		return
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if connInc > 0 {
		_ = c.broken(c.fr.WriteWindowUpdate(0, connInc))
	}
	if streamInc > 0 {
		_ = c.broken(c.fr.WriteWindowUpdate(s.id, streamInc))
	}
}

// due reports whether unacked bytes should be returned now. c.mu must be
// held.
func (c *Conn) due(unacked, threshold uint32, last, now time.Time) bool {
	return unacked >= threshold || (c.updateInterval > 0 && unacked > 0 && now.Sub(last) >= c.updateInterval)
}

// bodyClosed handles the caller closing a response body. One read in full
// waits for the peer's END_STREAM; one closed early is reset. Bytes still
// buffered go back to the connection window, after the reset, as Chromium
// returns the data it discards.
func (c *Conn) bodyClosed(s *stream, complete bool, discarded int) {
	if complete {
		c.linger(s)
	} else {
		c.reset(s, http2.ErrCodeCancel, errBodyClosed)
	}
	if discarded > 0 {
		_ = c.returnConnCredit(uint32(discarded)) //nolint:gosec // at most the stream window
	}
}

// linger waits for the peer's END_STREAM on a stream whose body was read in
// full and closed, instead of resetting it: browsers never reset a finished
// response, and many servers send END_STREAM in a separate empty DATA frame
// after the last byte. A peer that never ends the stream is reset after
// lingerTimeout. The caller is done with the stream, so its context no
// longer resets it.
func (c *Conn) linger(s *stream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.done {
		return
	}
	s.stopCtx()
	s.linger = c.clock.AfterFunc(lingerTimeout, func() {
		c.reset(s, http2.ErrCodeCancel, errBodyClosed)
	})
}

// reset sends RST_STREAM with code and ends the stream with err.
func (c *Conn) reset(s *stream, code http2.ErrCode, err error) {
	c.endStream(s, ending{
		err:  err,
		rst:  true,
		code: code,
	})
}

// endStream is where every stream ends, once: normally (io.EOF) or with an
// error. It removes s, sends RST_STREAM if asked, then hands the error to
// RoundTrip if the response was not delivered and ends the body (an error
// discards what it buffered and returns that credit to the connection), and
// closes a draining connection once its last stream is gone.
func (c *Conn) endStream(s *stream, e ending) {
	err := e.err
	c.mu.Lock()
	if s.done {
		c.mu.Unlock()
		return
	}
	if s.bodyStarted && errors.Is(err, ErrConnUnusable) {
		// Not wrapped: it must no longer match ErrConnUnusable.
		err = fmt.Errorf("%w: %s", errRefusedAfterBody, err.Error())
	}
	s.done, s.err = true, err
	id := s.id
	if id != 0 {
		delete(c.streams, id)
	}
	if s.linger != nil {
		s.linger.Stop()
	}
	s.stopCtx()
	// Whoever sets delivered owns the one send on s.resp, so it need not
	// happen under mu.
	deliver := !s.delivered
	s.delivered = true
	c.active--
	idle := c.draining && c.active == 0
	c.cond.Broadcast()
	c.mu.Unlock()
	if e.rst && id != 0 {
		c.wmu.Lock()
		_ = c.broken(c.fr.WriteRSTStream(id, e.code))
		c.wmu.Unlock()
	}
	if deliver {
		s.resp <- responseOrError{
			err: err,
		}
	}
	if n := s.body.end(err); n > 0 {
		_ = c.returnConnCredit(uint32(n)) //nolint:gosec // at most the stream window
	}
	if idle {
		_ = c.conn.Close()
	}
}

func (c *Conn) readLoop() {
	defer close(c.done)
	err := c.read()
	streamErr := err
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		// New requests may go to another connection. Requests already sent
		// may have been processed, so they are not retried.
		err, streamErr = ErrConnUnusable, errConnLost
	}
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.cond.Broadcast()
	c.mu.Unlock()
	if ce, ok := errors.AsType[http2.ConnectionError](err); ok {
		c.writeGoAway(http2.ErrCode(ce))
	}
	_ = c.conn.Close()
	// A header block being written goes out, or fails and leaves c.streams,
	// before the streams in flight are failed.
	c.wmu.Lock()
	c.mu.Lock()
	streams := make([]*stream, 0, len(c.streams))
	for _, s := range c.streams {
		streams = append(streams, s)
	}
	c.mu.Unlock()
	c.wmu.Unlock()
	for _, s := range streams {
		c.endStream(s, ending{
			err: streamErr,
		})
	}
}

// writeGoAway tells the peer why the connection is failing. A peer that
// stopped reading does not hold it up: the connection closes after
// goAwayTimeout either way.
func (c *Conn) writeGoAway(code http2.ErrCode) {
	t := c.clock.AfterFunc(goAwayTimeout, func() {
		_ = c.conn.Close()
	})
	defer t.Stop()
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.fr.WriteGoAway(0, code, nil)
}

func (c *Conn) read() error {
	for {
		f, err := c.fr.ReadFrame()
		if se, ok := errors.AsType[http2.StreamError](err); ok {
			// One stream's error (RFC 9113, 5.4.2): only it is reset.
			if s := c.stream(se.StreamID); s != nil {
				c.reset(s, se.Code, se)
			}
			continue
		}
		if errors.Is(err, http2.ErrFrameTooLarge) {
			// Larger than our SETTINGS_MAX_FRAME_SIZE (RFC 9113, 4.2).
			return http2.ConnectionError(http2.ErrCodeFrameSize)
		}
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.lastRead = c.clock.Now()
		c.mu.Unlock()
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if f.IsAck() {
				continue
			}
			if err := c.applySettings(f); err != nil {
				return err
			}
		case *http2.WindowUpdateFrame:
			if err := c.onWindowUpdate(f); err != nil {
				return err
			}
		case *http2.MetaHeadersFrame:
			c.onHeaders(f)
		case *http2.DataFrame:
			if err := c.onData(f); err != nil {
				return err
			}
		case *http2.RSTStreamFrame:
			c.onReset(f)
		case *http2.PingFrame:
			if f.IsAck() {
				c.mu.Lock()
				if c.pingTimer != nil {
					c.pingTimer.Stop()
					c.pingTimer = nil
				}
				c.mu.Unlock()
				continue
			}
			c.wmu.Lock()
			err := c.broken(c.fr.WritePing(true, f.Data))
			c.wmu.Unlock()
			if err != nil {
				return err
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
		if err := s.Valid(); err != nil {
			return err
		}
		switch s.ID {
		case http2.SettingMaxFrameSize:
			c.maxFrameSize = s.Val
		case http2.SettingMaxConcurrentStreams:
			c.maxStreams = s.Val
		case http2.SettingInitialWindowSize:
			delta := int64(s.Val) - int64(c.initialWindow)
			for _, st := range c.streams {
				w := int64(st.sendWindow) + delta
				if w > maxWindow {
					return http2.ConnectionError(http2.ErrCodeFlowControl)
				}
				st.sendWindow = int32(w) //nolint:gosec // checked above
			}
			c.initialWindow = int32(s.Val) //nolint:gosec // Valid: at most 2^31-1
		case http2.SettingHeaderTableSize:
			tableSize, haveTableSize = s.Val, true
		case http2.SettingEnableConnectProtocol:
			// RFC 8441, section 3: once 1, it may not go back to 0.
			if s.Val == 0 && c.connectProtocol {
				return http2.ConnectionError(http2.ErrCodeProtocol)
			}
			c.connectProtocol = s.Val == 1
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
		c.henc.SetPeerTableSize(tableSize)
	}
	return c.broken(c.fr.WriteSettingsAck())
}

// onWindowUpdate adds the peer's credit. A window past 2^31-1 is a
// FLOW_CONTROL_ERROR: for the connection, or for the stream only.
func (c *Conn) onWindowUpdate(f *http2.WindowUpdateFrame) error {
	c.mu.Lock()
	if f.StreamID == 0 {
		w := int64(c.sendWindow) + int64(f.Increment)
		if w > maxWindow {
			c.mu.Unlock()
			return http2.ConnectionError(http2.ErrCodeFlowControl)
		}
		c.sendWindow = int32(w) //nolint:gosec // checked above
		c.cond.Broadcast()
		c.mu.Unlock()
		return nil
	}
	s := c.streams[f.StreamID]
	if s == nil {
		c.mu.Unlock()
		return nil
	}
	w := int64(s.sendWindow) + int64(f.Increment)
	if w > maxWindow {
		c.mu.Unlock()
		c.reset(s, http2.ErrCodeFlowControl, http2.StreamError{
			StreamID: f.StreamID,
			Code:     http2.ErrCodeFlowControl,
		})
		return nil
	}
	s.sendWindow = int32(w) //nolint:gosec // checked above
	c.cond.Broadcast()
	c.mu.Unlock()
	return nil
}

func (c *Conn) onHeaders(f *http2.MetaHeadersFrame) {
	s := c.stream(f.StreamID)
	if s == nil {
		return
	}
	if c.onHeadersHook != nil {
		c.onHeadersHook()
	}
	if f.Truncated {
		c.reset(s, http2.ErrCodeProtocol, errHeaderListSize)
		return
	}
	c.mu.Lock()
	trailers := s.delivered
	c.mu.Unlock()
	if trailers {
		// Trailers carry no :status; they are not exposed, but may end the
		// stream.
		if f.StreamEnded() {
			c.endStream(s, ending{
				err: io.EOF,
			})
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
		if f.StreamEnded() {
			c.reset(s, http2.ErrCodeProtocol, fmt.Errorf("h2: stream ended by a %d response", code))
		}
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
	s.body.setExpected(resp.ContentLength)
	c.mu.Lock()
	if s.done {
		// Ended meanwhile (a cancel): RoundTrip has its error.
		c.mu.Unlock()
		return
	}
	s.delivered = true
	s.resp <- responseOrError{
		resp: resp,
	}
	c.mu.Unlock()
	if f.StreamEnded() {
		c.endStream(s, ending{
			err: io.EOF,
		})
	}
}

// onData buffers a DATA frame's payload for the stream's reader. The frame,
// padding included, counts against both receive windows: past the
// connection's is a connection FLOW_CONTROL_ERROR, past the stream's resets
// the stream.
func (c *Conn) onData(f *http2.DataFrame) error {
	size := f.Length
	c.mu.Lock()
	c.recvConnAvail -= int64(size)
	if c.recvConnAvail < 0 {
		c.mu.Unlock()
		return http2.ConnectionError(http2.ErrCodeFlowControl)
	}
	s := c.streams[f.StreamID]
	var streamErr error
	code := http2.ErrCodeFlowControl
	if s != nil {
		s.recvWindow -= int64(size)
		switch {
		case s.recvWindow < 0:
			streamErr = http2.StreamError{
				StreamID: f.StreamID,
				Code:     code,
			}
		case !s.delivered:
			code = http2.ErrCodeProtocol
			streamErr = errors.New("h2: DATA before the response headers")
		}
	}
	c.mu.Unlock()
	if s == nil {
		// A stream we already closed: still return the connection credit.
		return c.returnConnCredit(size)
	}
	if streamErr != nil {
		c.reset(s, code, streamErr)
		return c.returnConnCredit(size)
	}
	if data := f.Data(); len(data) > 0 && !s.body.write(data) {
		// The body was closed or ended: nobody will read these bytes.
		if err := c.returnConnCredit(size); err != nil {
			return err
		}
	} else if pad := int(size) - len(f.Data()); pad > 0 {
		c.consumed(s, pad)
	}
	if f.StreamEnded() {
		c.endStream(s, ending{
			err: io.EOF,
		})
	}
	return nil
}

// returnConnCredit counts size bytes that no reader will consume towards the
// connection's WINDOW_UPDATE.
func (c *Conn) returnConnCredit(size uint32) error {
	now := c.clock.Now()
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil
	}
	c.recvConnUnacked += size
	var inc uint32
	if c.due(c.recvConnUnacked, c.connThreshold, c.connLastUpdate, now) {
		inc, c.recvConnUnacked, c.connLastUpdate = c.recvConnUnacked, 0, now
		c.recvConnAvail += int64(inc)
	}
	c.mu.Unlock()
	if inc == 0 {
		return nil
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.broken(c.fr.WriteWindowUpdate(0, inc))
}

// onReset ends a stream the peer reset. One it refused was not processed
// (RFC 9113, 8.7), so it may be retried elsewhere: the connection then takes
// no new request and closes once idle, so dropping it from a pool does not
// leak it.
func (c *Conn) onReset(f *http2.RSTStreamFrame) {
	s := c.stream(f.StreamID)
	if s == nil {
		return
	}
	var err error = http2.StreamError{
		StreamID: f.StreamID,
		Code:     f.ErrCode,
	}
	if f.ErrCode == http2.ErrCodeRefusedStream {
		c.mu.Lock()
		c.draining = true
		c.mu.Unlock()
		err = fmt.Errorf("%w: %w", ErrConnUnusable, err)
	}
	c.endStream(s, ending{
		err: err,
	})
}

// onGoAway stops new streams. Streams the peer will not process (past
// LastStreamID) fail with ErrConnUnusable; the others run to completion, and
// the connection closes after the last.
func (c *Conn) onGoAway(f *http2.GoAwayFrame) {
	c.mu.Lock()
	c.draining = true
	var refused []*stream
	for id, s := range c.streams {
		if id > f.LastStreamID {
			refused = append(refused, s)
		}
	}
	idle := c.active == 0
	c.cond.Broadcast()
	c.mu.Unlock()
	for _, s := range refused {
		c.endStream(s, ending{
			err: fmt.Errorf("%w: GOAWAY after stream %d", ErrConnUnusable, f.LastStreamID),
		})
	}
	if idle {
		_ = c.conn.Close()
	}
}
