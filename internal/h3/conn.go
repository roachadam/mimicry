// Package h3 is mimicry's HTTP/3 client (RFC 9114) on internal/quic, with
// field sections encoded by internal/qpack. It reproduces the profile's
// HTTP/3 (profile.HTTP3): SETTINGS and GREASE, when each stream opens, QPACK
// table use, PRIORITY_UPDATE, header lists and DATA framing.
//
// Each request's frames leave in one flight, in the browser's order
// (quic.Conn.Cork). A request the server did not process fails with
// ErrConnUnusable or ErrRejected so it can go on another connection.
package h3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/qpack"
	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/internal/quic/quicwire"
	"github.com/roachadam/mimicry/profile"
)

// ErrConnUnusable is a request the connection did not send: it is going
// away (the server's GOAWAY), or ended. Nothing of the request was sent,
// so it may go on another connection.
var ErrConnUnusable = errors.New("h3: connection cannot take the request")

// ErrRejected is a request with a body the server did not process (its
// GOAWAY, or H3_REQUEST_REJECTED): it may go on another connection with a
// fresh copy of its body.
var ErrRejected = errors.New("h3: request rejected by the server")

// ErrConnLost is a request whose connection ended (the server closed it,
// it timed out, or reset) before the response's headers arrived: the
// server may or may not have processed it.
var ErrConnLost = errors.New("h3: connection ended before the response")

// maxFieldSection bounds a response's field section and control frames
// when the profile announces no SETTINGS_MAX_FIELD_SECTION_SIZE.
const maxFieldSection = 256 << 10

// maxQPACKCapacity caps the dynamic table the encoder uses whatever the
// server allows: quiche's and neqo's default (64 KiB); the captured
// server offered exactly that.
const maxQPACKCapacity = 64 << 10

// settingsWait bounds, in round trips, how long a connection whose QPACK
// encoder stream opens at the handshake waits for the server's SETTINGS, so
// that stream can carry Set Dynamic Table Capacity. settingsMinWait keeps a
// busy scheduler from passing for missing SETTINGS.
const (
	settingsWait    = 1
	settingsMinWait = 20 * time.Millisecond
)

// Options are what a connection needs besides the QUIC connection and the
// profile.
type Options struct {
	// AltHost and AltPort are the alternative service the connection
	// reaches (alt-used).
	AltHost, AltPort string
	// Pending: the connection was dialled for a request about to be sent,
	// which goes before the control and QPACK streams in 0-RTT where the
	// profile says so (Firefox).
	Pending bool
}

// Conn is a client HTTP/3 connection on a QUIC connection, speaking as the
// profile's browser does down to the order of its frames on the wire. It is
// safe for concurrent use.
type Conn struct {
	qc      *quic.Conn
	p       *profile.Profile
	h3      *profile.HTTP3
	altHost string
	altPort string

	// startMu serialises request starts, so a request's encoder
	// instructions, PRIORITY_UPDATE and HEADERS leave together in stream
	// order. It guards the uni streams' state below.
	startMu sync.Mutex
	// opened is set once the streams opened at the handshake are.
	opened  bool
	control *quic.Stream
	encoder *quic.Stream
	decoder *quic.Stream
	// encoderTyped, decoderTyped: the stream's type has been written.
	encoderTyped bool
	decoderTyped bool

	// encMu guards enc, which the decoder stream reader also updates.
	encMu sync.Mutex
	enc   *qpack.Encoder

	// decMu guards dec; decCond wakes sections blocked on its inserts.
	decMu   sync.Mutex
	decCond *sync.Cond
	dec     *qpack.Decoder

	// mu guards the connection's state.
	mu     sync.Mutex
	active int
	// requests are the request streams in flight, by stream ID.
	requests map[uint64]*requestStream
	goaway   bool
	goawayID uint64
	failed   error
	// settings is closed once the server's SETTINGS are applied.
	settings     chan struct{}
	settingsOnce sync.Once
	done         chan struct{}
	// maxSection is the largest field section accepted.
	maxSection uint64
}

// New starts HTTP/3 on qc, which quic.Dial returned corked, for profile p,
// which has HTTP3 and QUIC sections. It opens the profile's handshake
// streams (after the request, for opts.Pending in 0-RTT where the profile
// sends it first) and serves the server's streams until qc ends.
func New(qc *quic.Conn, p *profile.Profile, opts Options) (*Conn, error) {
	h := p.HTTP3
	c := &Conn{
		qc:         qc,
		p:          p,
		h3:         h,
		altHost:    opts.AltHost,
		altPort:    opts.AltPort,
		settings:   make(chan struct{}),
		done:       make(chan struct{}),
		requests:   map[uint64]*requestStream{},
		maxSection: maxFieldSection,
	}

	c.decCond = sync.NewCond(&c.decMu)
	c.enc = qpack.NewEncoder(QPACKConfig(h))

	tableCapacity := uint64(0)

	for _, s := range h.Settings {
		switch s.ID {
		case profile.H3SettingQPACKMaxTableCapacity:
			tableCapacity = s.Value
		case profile.H3SettingMaxFieldSectionSize:
			c.maxSection = s.Value
		}
	}

	c.dec = qpack.NewDecoder(tableCapacity)

	go c.serve()

	ctx := context.Background()
	// Every stream ID the profile takes, in order, before anything is
	// written.
	for _, u := range h.UniStreams {
		s, err := qc.NewSendOnlyStream(ctx)
		if err != nil {
			qc.Abort(err)

			return nil, err
		}

		switch u.Type {
		case profile.H3StreamControl:
			c.control = s
		case profile.H3StreamQPACKEncoder:
			c.encoder = s
		case profile.H3StreamQPACKDecoder:
			c.decoder = s
		}
	}

	if c.control == nil {
		qc.Abort(nil)

		return nil, errors.New("h3: the profile opens no control stream")
	}

	c.startMu.Lock()
	defer c.startMu.Unlock()

	early := qc.EarlyData()
	switch {
	case early && opts.Pending && h.EarlyRequestFirst:
		// The request goes first; its start opens the streams after it,
		// and uncorks.
		return c, nil
	case !early && !h.StreamsWithFinished:
		// The Finished leaves alone; the streams follow.
		qc.Uncork()
		c.waitSettings()
		qc.Cork()
	}

	c.openStreams()
	qc.Uncork()
	// The streams' flight goes before the first request's stream exists
	// (quic.Conn.Settle).
	qc.Settle()

	return c, nil
}

// QPACKConfig is how the profile's browser encodes field sections.
// Without a measured encoder (Safari) it never uses the dynamic table.
func QPACKConfig(h *profile.HTTP3) qpack.Config {
	cfg := qpack.Config{
		Strategy:      qpack.StrategyStatic,
		HuffmanAlways: h.QPACK.Huffman == profile.HuffmanAlways,
		MaxCapacity:   maxQPACKCapacity,
	}
	switch h.QPACK.Encoder {
	case profile.QPACKIndexAll:
		cfg.Strategy = qpack.StrategyIndexAll
	case profile.QPACKNewNames:
		cfg.Strategy = qpack.StrategyNewNames
	}

	return cfg
}

// waitSettings waits briefly for the server's SETTINGS, which arrive with
// its handshake flight, so a QPACK encoder stream opened at the handshake
// carries Set Dynamic Table Capacity as the browser's does.
func (c *Conn) waitSettings() {
	encoderAtHandshake := false
	for _, u := range c.h3.UniStreams {
		if u.Type == profile.H3StreamQPACKEncoder && u.Open == profile.H3OpenAtHandshake {
			encoderAtHandshake = true
		}
	}

	if !encoderAtHandshake {
		return
	}

	wait := settingsWait * c.qc.SmoothedRTT()
	t := time.NewTimer(max(wait, settingsMinWait))

	defer t.Stop()

	select {
	case <-c.settings:
	case <-c.done:
	case <-t.C:
	}
}

// openStreams writes the streams the profile opens at the handshake, in
// stream ID order. c.startMu must be held, and the connection corked.
func (c *Conn) openStreams() {
	if c.opened {
		return
	}

	c.opened = true
	for _, u := range c.h3.UniStreams {
		if u.Open != profile.H3OpenAtHandshake {
			continue
		}

		switch u.Type {
		case profile.H3StreamControl:
			_, _ = c.control.Write(controlStream(c.h3))
			_ = c.control.Flush()

		case profile.H3StreamQPACKEncoder:
			c.encMu.Lock()

			capacity := c.enc.TakeCapacity()
			c.encMu.Unlock()
			c.writeEncoder(capacity, true)

		case profile.H3StreamQPACKDecoder:
			c.writeDecoder(nil)
		}
	}
}

// writeEncoder writes encoder stream instructions, opening the stream
// (its type first) on its first write, or with force when it opens at the
// handshake. c.startMu must be held.
func (c *Conn) writeEncoder(ins []byte, force bool) {
	if len(ins) == 0 && !force {
		return
	}

	if c.encoder == nil {
		s, err := c.qc.NewSendOnlyStream(context.Background())
		if err != nil {
			return
		}

		c.encoder = s
	}

	if !c.encoderTyped {
		c.encoderTyped = true

		ins = append(quicwire.AppendVarint(nil, streamQPACKEncoder), ins...)
	}

	_, _ = c.encoder.Write(ins)
	_ = c.encoder.Flush()
}

// writeDecoder writes decoder stream instructions, opening the stream as
// writeEncoder does: at the handshake (nil b), or on its first
// instruction. c.startMu must be held.
func (c *Conn) writeDecoder(b []byte) {
	if c.decoder == nil {
		s, err := c.qc.NewSendOnlyStream(context.Background())
		if err != nil {
			return
		}

		c.decoder = s
	}

	if !c.decoderTyped {
		c.decoderTyped = true

		b = append(quicwire.AppendVarint(nil, streamQPACKDecoder), b...)
	}

	if len(b) == 0 {
		return
	}

	_, _ = c.decoder.Write(b)
	_ = c.decoder.Flush()
}

// Close closes the connection at once with H3_NO_ERROR.
func (c *Conn) Close() error {
	c.qc.Abort(&quic.ConnectionCloseError{
		Code: codeNoError,
	})

	return nil
}

// Done is closed once the QUIC connection has ended.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Err is why the QUIC connection ended (quic.Conn.Err); nil before Done.
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.qc.Err()
	default:
		return nil
	}
}

// GoneAway reports whether the server sent GOAWAY.
func (c *Conn) GoneAway() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.goaway
}

// CanTakeNewRequest reports whether a new request may start on the
// connection: it has not ended, failed or received GOAWAY.
func (c *Conn) CanTakeNewRequest() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case <-c.qc.Done():
		return false
	default:
	}

	return !c.goaway && c.failed == nil
}

// Idle reports whether no request is in flight.
func (c *Conn) Idle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.active == 0
}

// fail ends the connection with an HTTP/3 connection error.
func (c *Conn) fail(err error) {
	c.mu.Lock()

	if c.failed == nil {
		c.failed = err
	}

	c.mu.Unlock()

	select {
	case <-c.qc.Done():
		// The connection has ended, or the peer closed it and the QUIC
		// layer answers as the profile does (Firefox's reply): the
		// streams failing with it are no error to send over that.
		return
	default:
	}

	code := uint64(codeInternalError)
	if ce, ok := errors.AsType[*connError](err); ok {
		code = ce.code
	}

	c.qc.Abort(&quic.ConnectionCloseError{
		Code: code,
	})
}

// serve accepts the server's streams until the connection ends.
func (c *Conn) serve() {
	go func() {
		<-c.qc.Done()
		close(c.done)
		c.decMu.Lock()
		c.decCond.Broadcast()
		c.decMu.Unlock()
	}()

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	go func() {
		<-c.done
		cancel()
	}()

	seen := map[uint64]bool{}
	for {
		s, err := c.qc.AcceptStream(ctx)
		if err != nil {
			return
		}

		if !s.IsReadOnly() {
			// Servers open no bidirectional streams (RFC 9114, 6.1).
			c.fail(&connError{
				code: codeStreamCreationError,
				msg:  "server opened a bidirectional stream",
			})

			return
		}

		go c.serveUni(s, seen)
	}
}

// serveUni reads one of the server's unidirectional streams by its type.
func (c *Conn) serveUni(s *quic.Stream, seen map[uint64]bool) {
	typ, err := readVarint(s)
	if err != nil {
		return
	}

	switch typ {
	case streamControl, streamQPACKEncoder, streamQPACKDecoder:
		c.mu.Lock()

		dup := seen[typ]

		seen[typ] = true
		c.mu.Unlock()

		if dup {
			c.fail(&connError{
				code: codeStreamCreationError,
				msg:  fmt.Sprintf("second stream of type %d", typ),
			})

			return
		}

	case streamPush:
		// No MAX_PUSH_ID was sent, so no push is allowed (RFC 9114,
		// 4.6).
		c.fail(&connError{
			code: codeIDError,
			msg:  "push stream without MAX_PUSH_ID",
		})

		return

	default:
		// Unknown (GREASE) types are ignored (RFC 9114, 6.2.3).
		_ = s.StopSending(codeStreamCreationError)
		return
	}

	switch typ {
	case streamControl:
		err = c.readControl(s)
	case streamQPACKEncoder:
		err = c.readEncoderStream(s)
	case streamQPACKDecoder:
		err = c.readDecoderStream(s)
	}

	select {
	case <-c.done:
		return
	default:
	}

	if err == nil || errors.Is(err, io.EOF) {
		err = &connError{
			code: codeClosedCriticalStream,
			msg:  "server closed a critical stream",
		}
	}

	c.fail(err)
}

// readControl reads the server's control stream: SETTINGS first, then
// GOAWAY and frames to ignore.
func (c *Conn) readControl(s *quic.Stream) error {
	first := true
	for {
		typ, length, err := readFrameHeader(s)
		if err != nil {
			return err
		}

		if first != (typ == frameSettings) {
			if first {
				return &connError{
					code: codeMissingSettings,
					msg:  "control stream does not start with SETTINGS",
				}
			}

			if typ == frameSettings {
				return &connError{
					code: codeFrameUnexpected,
					msg:  "second SETTINGS",
				}
			}
		}

		first = false

		switch typ {
		case frameSettings:
			b, err := readPayload(s, length, c.maxSection)
			if err != nil {
				return err
			}

			if err := c.applySettings(b); err != nil {
				return err
			}

		case frameGoAway:
			b, err := readPayload(s, length, 16)
			if err != nil {
				return err
			}

			id, n := quicwire.ConsumeVarint(b)
			if n != len(b) {
				return &connError{
					code: codeFrameError,
					msg:  "malformed GOAWAY",
				}
			}

			c.goAway(id)

		case frameData, frameHeaders, framePushPromise:
			return &connError{
				code: codeFrameUnexpected,
				msg:  fmt.Sprintf("frame type %d on the control stream", typ),
			}

		default:
			// CANCEL_PUSH, MAX_PUSH_ID, PRIORITY_UPDATE and unknown frames
			// carry nothing a client acts on.
			if err := skip(s, length); err != nil {
				return err
			}
		}
	}
}

// applySettings applies the server's SETTINGS to the QPACK encoder.
func (c *Conn) applySettings(b []byte) error {
	var capacity, blocked uint64

	seen := map[uint64]bool{}

	for len(b) > 0 {
		id, n := quicwire.ConsumeVarint(b)
		if n < 0 {
			return &connError{
				code: codeFrameError,
				msg:  "malformed SETTINGS",
			}
		}

		b = b[n:]

		v, n := quicwire.ConsumeVarint(b)

		if n < 0 {
			return &connError{
				code: codeFrameError,
				msg:  "malformed SETTINGS",
			}
		}

		b = b[n:]

		if seen[id] {
			return &connError{
				code: codeSettingsError,
				msg:  fmt.Sprintf("setting 0x%x twice", id),
			}
		}

		seen[id] = true
		switch id {
		case profile.H3SettingQPACKMaxTableCapacity:
			capacity = v
		case profile.H3SettingQPACKBlockedStreams:
			blocked = v
		case 0x02, 0x03, 0x04, 0x05:
			// HTTP/2 settings reserved in HTTP/3 (RFC 9114, 7.2.4.1).
			return &connError{
				code: codeSettingsError,
				msg:  fmt.Sprintf("reserved setting 0x%x", id),
			}
		}
	}

	c.encMu.Lock()
	c.enc.SetPeerSettings(capacity, blocked)
	c.encMu.Unlock()
	c.settingsOnce.Do(func() {
		close(c.settings)
	})

	return nil
}

// goAway records the server's GOAWAY: requests on streams from id on
// were not processed.
func (c *Conn) goAway(id uint64) {
	c.mu.Lock()

	if !c.goaway || id < c.goawayID {
		c.goaway, c.goawayID = true, id
	}

	var refused []*requestStream
	for sid, r := range c.requests {
		if sid >= c.goawayID {
			refused = append(refused, r)
		}
	}

	c.mu.Unlock()

	// The server will not answer them (RFC 9114, 5.2): they go on another
	// connection.
	for _, r := range refused {
		r.cancel()
	}
}

// rejected reports whether the server's GOAWAY covers stream id.
func (c *Conn) rejected(id uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.goaway && id >= c.goawayID
}

// readEncoderStream feeds the server's encoder stream to the decoder.
func (c *Conn) readEncoderStream(s *quic.Stream) error {
	buf := make([]byte, 4096)
	for {
		n, err := s.Read(buf)
		if n > 0 {
			c.decMu.Lock()

			derr := c.dec.OnEncoderStream(buf[:n])
			c.decCond.Broadcast()
			c.decMu.Unlock()

			if derr != nil {
				return &connError{
					code: codeQPACKEncoderStreamError,
					msg:  derr.Error(),
				}
			}
		}

		if err != nil {
			return err
		}
	}
}

// readDecoderStream feeds the server's decoder stream to the encoder.
func (c *Conn) readDecoderStream(s *quic.Stream) error {
	buf := make([]byte, 1024)
	for {
		n, err := s.Read(buf)
		if n > 0 {
			c.encMu.Lock()

			eerr := c.enc.OnDecoderStream(buf[:n])
			c.encMu.Unlock()

			if eerr != nil {
				return &connError{
					code: codeQPACKDecoderStreamError,
					msg:  eerr.Error(),
				}
			}
		}

		if err != nil {
			return err
		}
	}
}

// reserve counts a request about to start, unless the connection cannot
// take it. While requests are in flight the connection keeps itself alive,
// as browsers do with open streams.
func (c *Conn) reserve() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case <-c.qc.Done():
		return false
	default:
	}

	if c.goaway || c.failed != nil {
		return false
	}

	c.active++
	if c.active == 1 {
		c.qc.KeepAlive(true)
	}

	return true
}

func (c *Conn) release() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.active--
	if c.active == 0 {
		c.qc.KeepAlive(false)
	}
}

// RoundTrip sends req on a new request stream and returns the response once
// its headers arrive; req's context bounds the whole exchange, body reads
// included. req.Body is closed, except with ErrConnUnusable, which leaves it
// for the retry. A 0-RTT request answered 425 Too Early goes again in 1-RTT
// where the profile retries it (HTTP3.EarlyData) and its body can be re-read.
func (c *Conn) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, early, err := c.roundTrip(req, c.earlyMethod(req.Method))
	if err != nil || !early || resp.StatusCode != http.StatusTooEarly || c.h3.EarlyData == nil || !c.h3.EarlyData.RetryTooEarly {
		return resp, err
	}

	again, ok := rewound(req)
	if !ok {
		return resp, nil
	}

	_ = resp.Body.Close()
	resp, _, err = c.roundTrip(again, false)

	return resp, err
}

// earlyMethod reports whether the profile sends a request with method in
// 0-RTT (HTTP3.EarlyData.Methods).
func (c *Conn) earlyMethod(method string) bool {
	return c.h3.EarlyData == nil || slices.Contains(c.h3.EarlyData.Methods, method)
}

// rewound is req ready to send again, with a new copy of its body; false
// if its body cannot be read again.
func rewound(req *http.Request) (*http.Request, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, true
	}

	if req.GetBody == nil {
		return nil, false
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}

	req = req.Clone(req.Context())
	req.Body = body

	return req, true
}

// roundTrip is RoundTrip's one exchange, in 0-RTT only if mayEarly; early
// reports that its request went in 0-RTT.
func (c *Conn) roundTrip(req *http.Request, mayEarly bool) (resp *http.Response, early bool, err error) {
	ctx := req.Context()
	hasBody := req.Body != nil && req.Body != http.NoBody
	length := bodyLength(req)

	hd, err := buildHead(c.p, c.altHost, c.altPort, req, length)
	if err != nil {
		closeBody(req)

		return nil, false, err
	}

	if !c.reserve() {
		return nil, false, ErrConnUnusable
	}

	s, early, err := c.start(ctx, hd, !hasBody, mayEarly)
	if err != nil {
		c.release()

		if !errors.Is(err, ErrConnUnusable) {
			closeBody(req)
		}

		return nil, false, err
	}

	id := uint64(s.ID())
	r := &requestStream{
		c:  c,
		s:  s,
		id: id,
	}
	c.mu.Lock()

	c.requests[id] = r
	c.mu.Unlock()

	r.stop = context.AfterFunc(ctx, r.cancel)
	if hasBody {
		go r.sendBody(req.Body, length)
	}

	resp, err = r.readResponse(ctx, req)
	if err != nil {
		r.finish(err)

		var se quic.StreamError
		if ctx.Err() == nil && (c.rejected(id) || errors.As(err, &se) && uint64(se) == codeRequestRejected) {
			// The server did not process it (RFC 9114, 4.1.1).
			if hasBody {
				return nil, false, fmt.Errorf("%w: %w", ErrRejected, err)
			}

			return nil, false, fmt.Errorf("%w: %w", ErrConnUnusable, err)
		}

		if ctx.Err() == nil && !errors.Is(err, ErrConnLost) {
			select {
			case <-c.qc.Done():
				return nil, false, fmt.Errorf("%w: %w", ErrConnLost, err)
			default:
			}
		}

		return nil, false, err
	}

	return resp, early, nil
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

// start opens the request's stream and sends its HEADERS (ending the
// stream when end), with its encoder instructions and PRIORITY_UPDATE, in
// the profile's order and in one flight; in 0-RTT only if mayEarly
// (awaitHandshake). early reports that it went in 0-RTT.
func (c *Conn) start(ctx context.Context, hd head, end, mayEarly bool) (s *quic.Stream, early bool, err error) {
	if c.h3.RequestsAfterConfirmed && !c.qc.EarlyData() {
		// Safari's request leaves once the handshake is confirmed; a
		// connection that ends first fails NewStream below.
		select {
		case <-c.qc.Confirmed():
		case <-c.done:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}

	if c.qc.EarlyData() && !mayEarly {
		release, err := c.awaitHandshake(ctx)
		if err != nil {
			return nil, false, err
		}
		defer release()
	}

	c.startMu.Lock()
	defer c.startMu.Unlock()

	s, err = c.qc.NewStream(ctx)
	if err != nil {
		select {
		case <-c.qc.Done():
			return nil, false, fmt.Errorf("%w: %w", ErrConnUnusable, err)
		default:
		}

		return nil, false, err
	}

	id := uint64(s.ID())
	if c.rejected(id) {
		_ = s.Reset(codeRequestCancelled)
		return nil, false, ErrConnUnusable
	}

	s.SetWriteContext(ctx)
	s.SetReadContext(ctx)
	c.encMu.Lock()

	ins, block := c.enc.Encode(id, hd.fields)
	c.encMu.Unlock()

	headers := appendFrame(nil, frameHeaders, block)
	// Dial's cork is still held when the request goes before the streams
	// (0-RTT, EarlyRequestFirst); the Uncork below releases it.
	opened := c.opened
	if opened {
		c.qc.Cork()
	}

	pu := c.h3.PriorityUpdate && hd.priority != ""
	if pu && opened {
		_, _ = c.control.Write(priorityUpdate(id, hd.priority))
		_ = c.control.Flush()
	}

	if c.h3.QPACK.InsertsFirst {
		c.writeEncoder(ins, false)
	}

	// Before the handshake completes a request goes in 0-RTT; after it, in
	// 1-RTT, even if still corked here.
	select {
	case <-c.qc.HandshakeComplete():
	default:
		early = c.qc.EarlyData()
	}

	_, err = s.Write(headers)
	if end {
		_ = s.CloseWrite()
	} else {
		_ = s.Flush()
	}

	if !opened {
		c.openStreams()

		if pu {
			_, _ = c.control.Write(priorityUpdate(id, hd.priority))
			_ = c.control.Flush()
		}
	}

	if !c.h3.QPACK.InsertsFirst {
		c.writeEncoder(ins, false)
	}

	c.qc.Uncork()

	if !opened {
		// The first flight with the streams goes before the next
		// request's stream exists (quic.Conn.Settle).
		c.qc.Settle()
	}

	if err != nil {
		select {
		case <-c.qc.Done():
			// Part of the request may have gone before the end.
			return nil, false, fmt.Errorf("%w: %w", ErrConnLost, err)
		default:
		}

		return nil, false, err
	}

	return s, early, nil
}

// awaitHandshake holds a request the profile does not send in 0-RTT
// (EarlyData.Methods) until the handshake completes or the connection or ctx
// ends. release, called once the request is written, lets it leave with the
// client's Finished (HeldWithFinished) or after the Finished's datagram
// (FinishedSent), never sharing it otherwise. A connection dialled for it
// (Options.Pending) first sends its streams in 0-RTT without it.
func (c *Conn) awaitHandshake(ctx context.Context) (release func(), err error) {
	release = func() {}

	ready := c.qc.FinishedSent()

	if c.h3.EarlyData.HeldWithFinished {
		// Held first, so a handshake that completes meanwhile cannot send
		// the Finished without the request.
		c.qc.HoldFinished()

		release = c.qc.ReleaseFinished
		ready = c.qc.HandshakeComplete()
	}

	select {
	case <-ready:
		return release, nil
	default:
	}

	select {
	case <-c.qc.HandshakeComplete():
		// Too late for 0-RTT: the streams go after the request (start).
	default:
		c.startMu.Lock()

		if !c.opened {
			c.openStreams()
			c.qc.Uncork()
			c.qc.Settle()
		}

		c.startMu.Unlock()
	}

	select {
	case <-ready:
	case <-c.done:
		// NewStream fails after.
	case <-ctx.Done():
		release()

		return func() {}, ctx.Err()
	}

	return release, nil
}

// ackSection acknowledges a decoded section on the decoder stream.
func (c *Conn) ackSection(id uint64) {
	c.startMu.Lock()
	defer c.startMu.Unlock()

	c.writeDecoder(qpack.AppendSectionAck(nil, id))
}

// decode decodes a response's field section, waiting while it is blocked
// on the encoder stream, and acknowledges it.
func (c *Conn) decode(ctx context.Context, id uint64, block []byte) ([]qpack.Field, error) {
	c.decMu.Lock()

	var (
		fields []qpack.Field
		ric    uint64
		err    error
		stop   func() bool
	)

	defer func() {
		if stop != nil {
			stop()
		}
	}()

	for {
		fields, ric, err = c.dec.Decode(block)
		if !errors.Is(err, qpack.ErrBlocked) {
			break
		}

		// Only a blocked section waits, so only it needs waking when ctx
		// ends; most never block.
		if stop == nil {
			stop = context.AfterFunc(ctx, func() {
				c.decMu.Lock()
				c.decCond.Broadcast()
				c.decMu.Unlock()
			})
		}

		if ctx.Err() != nil {
			c.decMu.Unlock()

			return nil, ctx.Err()
		}

		select {
		case <-c.done:
			c.decMu.Unlock()

			return nil, ErrConnLost

		default:
		}

		c.decCond.Wait()
	}

	c.decMu.Unlock()

	if err != nil {
		c.fail(&connError{
			code: codeQPACKDecompressionFailed,
			msg:  err.Error(),
		})

		return nil, err
	}

	if ric > 0 {
		c.ackSection(id)
	}

	return fields, nil
}

// requestStream is one request's stream.
type requestStream struct {
	c    *Conn
	s    *quic.Stream
	id   uint64
	stop func() bool
	// once ends the request: the stream released and cancelled if
	// unfinished.
	once sync.Once
}

// cancel abandons the stream: the context ended or the body was closed
// early.
func (r *requestStream) cancel() {
	_ = r.s.StopSending(codeRequestCancelled)
	_ = r.s.Reset(codeRequestCancelled)
}

// finish ends the request once, cancelling the stream after an error.
func (r *requestStream) finish(err error) {
	r.once.Do(func() {
		r.stop()

		if err != nil {
			r.cancel()
		} else {
			// The peer's side is fully read: closing ours sends nothing
			// (no STOP_SENDING after the FIN) but lets the QUIC conn drop
			// the stream, which it keeps until both sides are closed.
			_ = r.s.CloseRead()
		}

		r.c.mu.Lock()
		delete(r.c.requests, r.id)
		r.c.mu.Unlock()
		r.c.release()
	})
}

// sendBody sends the request body in DATA frames as the profile frames
// them, then ends the stream; a body that fails resets it.
func (r *requestStream) sendBody(body io.ReadCloser, length int64) {
	defer func() {
		_ = body.Close()
	}()

	if err := r.writeBody(body, length); err != nil {
		_ = r.s.Reset(codeRequestCancelled)
		return
	}

	_ = r.s.CloseWrite()
}

// writeBody writes body in DATA frames: one for the whole of a body of
// known length where the profile sends it so (MaxDataFrame zero), else
// frames of up to MaxDataFrame (FirstDataFrame for the first).
func (r *requestStream) writeBody(body io.Reader, length int64) error {
	h := r.c.h3
	if h.MaxDataFrame == 0 && length > 0 {
		header := quicwire.AppendVarint(quicwire.AppendVarint(nil, frameData), uint64(length))
		if _, err := r.s.Write(header); err != nil {
			return err
		}

		n, err := io.Copy(r.s, io.LimitReader(body, length))
		if err != nil {
			return err
		}

		if n != length {
			return fmt.Errorf("h3: request body is %d bytes, not %d", n, length)
		}

		return nil
	}

	size := h.MaxDataFrame
	if size == 0 {
		size = 16 << 10
	}

	first := size
	if h.FirstDataFrame > 0 {
		first = h.FirstDataFrame
	}

	buf := make([]byte, max(first, size))

	var sent int64

	for n := first; ; n = size {
		k, err := io.ReadFull(body, buf[:n])
		if k > 0 {
			if _, werr := r.s.Write(appendFrame(nil, frameData, buf[:k])); werr != nil {
				return werr
			}

			sent += int64(k)
		}

		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}

		if err != nil {
			return err
		}
	}

	if length > 0 && sent != length {
		return fmt.Errorf("h3: request body is %d bytes, not %d", sent, length)
	}

	return nil
}

// readResponse reads the response's HEADERS (skipping interim ones) and
// returns the response, its body reading the stream's DATA.
func (r *requestStream) readResponse(ctx context.Context, req *http.Request) (*http.Response, error) {
	id := uint64(r.s.ID())
	for {
		typ, length, err := readFrameHeader(r.s)
		if err != nil {
			return nil, unexpectedEOF(err)
		}

		switch typ {
		case frameHeaders:
		case frameData:
			return nil, &connError{
				code: codeFrameUnexpected,
				msg:  "DATA before HEADERS",
			}

		case frameSettings, frameGoAway, frameMaxPushID, frameCancelPush:
			err := &connError{
				code: codeFrameUnexpected,
				msg:  fmt.Sprintf("frame type %d on a request stream", typ),
			}
			r.c.fail(err)

			return nil, err

		default:
			// PUSH_PROMISE is refused by never allowing pushes; unknown
			// frames are skipped.
			if err := skip(r.s, length); err != nil {
				return nil, err
			}

			continue
		}

		block, err := readPayload(r.s, length, r.c.maxSection)
		if err != nil {
			return nil, err
		}

		fields, err := r.c.decode(ctx, id, block)
		if err != nil {
			return nil, err
		}

		resp, err := response(fields)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode >= 100 && resp.StatusCode < 200 {
			continue
		}

		resp.Request = req
		resp.Body = &body{
			r:   r,
			ctx: ctx,
		}

		if req.Method == http.MethodHead || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
			resp.ContentLength = 0
		}

		return resp, nil
	}
}

// response builds a response from its decoded fields. The header map is
// sized for them and its values share one backing array (each key's slice
// capped at its one value, so a repeated field appends to a copy), as
// net/http's HTTP/2 does.
func response(fields []qpack.Field) (*http.Response, error) {
	resp := &http.Response{
		Proto:         "HTTP/3.0",
		ProtoMajor:    3,
		Header:        make(http.Header, len(fields)),
		ContentLength: -1,
	}
	regular := false
	vals := make([]string, len(fields))

	for i, f := range fields {
		if strings.HasPrefix(f.Name, ":") {
			if regular || f.Name != ":status" || resp.StatusCode != 0 {
				return nil, fmt.Errorf("h3: malformed response pseudo-header %q", f.Name)
			}

			code, err := strconv.Atoi(f.Value)
			if err != nil || len(f.Value) != 3 || code < 100 {
				return nil, fmt.Errorf("h3: malformed :status %q", f.Value)
			}

			resp.StatusCode = code
			resp.Status = statusLine(code, f.Value)

			continue
		}

		regular = true

		key := http.CanonicalHeaderKey(f.Name)
		if vv, ok := resp.Header[key]; ok {
			resp.Header[key] = append(vv, f.Value)

			continue
		}

		vals[i] = f.Value
		resp.Header[key] = vals[i : i+1 : i+1]
	}

	if resp.StatusCode == 0 {
		return nil, errors.New("h3: response without :status")
	}

	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n >= 0 {
			resp.ContentLength = n
		}
	}

	return resp, nil
}

// statusLine is a response's Status ("200 OK"), without building the
// common ones.
func statusLine(code int, value string) string {
	switch code {
	case http.StatusOK:
		return "200 OK"
	case http.StatusNoContent:
		return "204 No Content"
	case http.StatusPartialContent:
		return "206 Partial Content"
	case http.StatusMovedPermanently:
		return "301 Moved Permanently"
	case http.StatusFound:
		return "302 Found"
	case http.StatusNotModified:
		return "304 Not Modified"
	case http.StatusNotFound:
		return "404 Not Found"
	}

	return value + " " + http.StatusText(code)
}

// body is a response body: the DATA frames of its stream.
type body struct {
	r      *requestStream
	ctx    context.Context
	remain uint64
	err    error
}

func (b *body) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}

	for b.remain == 0 {
		typ, length, err := readFrameHeader(b.r.s)
		if err != nil {
			if errors.Is(err, io.EOF) {
				b.end(io.EOF)

				return 0, io.EOF
			}

			b.end(err)

			return 0, err
		}

		switch typ {
		case frameData:
			b.remain = length
		case frameHeaders:
			// Trailers: decoded (the table may change), then the end.
			block, err := readPayload(b.r.s, length, b.r.c.maxSection)
			if err == nil {
				_, err = b.r.c.decode(b.ctx, uint64(b.r.s.ID()), block)
			}

			if err != nil {
				b.end(err)

				return 0, err
			}

		default:
			if err := skip(b.r.s, length); err != nil {
				b.end(err)

				return 0, err
			}
		}
	}

	if uint64(len(p)) > b.remain {
		p = p[:b.remain]
	}

	n, err := b.r.s.Read(p)

	b.remain -= uint64(n)

	if err != nil {
		// The stream returns its last bytes with io.EOF: a clean end if
		// they finish the DATA frame.
		if !errors.Is(err, io.EOF) || b.remain != 0 {
			err = unexpectedEOF(err)
		}

		b.end(err)

		return n, err
	}

	return n, nil
}

// end records how the body ended and ends the request.
func (b *body) end(err error) {
	b.err = err
	if errors.Is(err, io.EOF) {
		b.r.finish(nil)

		return
	}

	b.r.finish(err)
}

// Close ends the body; one not read to its end cancels the stream
// (H3_REQUEST_CANCELLED).
func (b *body) Close() error {
	if b.err == nil {
		b.end(errors.New("h3: response body closed"))
	}

	return nil
}
