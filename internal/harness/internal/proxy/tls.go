package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// tlsRecord is what the proxy keeps of one TLS connection: its handshake,
// and over HTTP/2 everything the client sent.
type tlsRecord struct {
	conn capture.ProxyTLS
	h2   *h2Recorder
}

// TLSConns returns the client's connections to a TLS proxy, in the order
// their handshakes ended, each with how many CONNECTs it carried.
func (p *Proxy) TLSConns() []capture.ProxyTLS {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]capture.ProxyTLS, len(p.tlsConns))
	for i, rec := range p.tlsConns {
		out[i] = rec.conn
		for _, r := range p.requests {
			if r.Conn == rec.conn.Conn {
				out[i].Requests++
			}
		}

		if rec.h2 != nil {
			out[i].H2, out[i].Settings = rec.h2.snapshot()
		}
	}

	return out
}

// WaitH2Ended waits, as long as ctx allows, until the client has ended
// every HTTP/2 connection to a TLS proxy so far: each one's record is then
// complete.
func (p *Proxy) WaitH2Ended(ctx context.Context) error {
	p.mu.Lock()

	recs := slices.Clone(p.tlsConns)
	p.mu.Unlock()

	for _, rec := range recs {
		p.mu.Lock()

		r := rec.h2
		p.mu.Unlock()

		if r == nil {
			continue
		}

		select {
		case <-r.ended:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// handshake reads conn's ClientHello from br, completes the TLS handshake
// as the proxy, and records both; id numbers the connection.
func (p *Proxy) handshake(ctx context.Context, conn net.Conn, br *bufio.Reader, id int) (*tls.Conn, *tlsRecord, error) {
	rec := &tlsRecord{
		conn: capture.ProxyTLS{
			Conn: id,
		},
	}

	var got bytes.Buffer

	raw, _, err := server.ReadClientHello(io.TeeReader(br, &got))
	if err != nil {
		rec.conn.Records = hex.EncodeToString(got.Bytes())
		rec.conn.Failed = "reading the ClientHello: " + err.Error()
		p.addTLS(rec)

		return nil, nil, err
	}

	tc := tls.Server(&passConn{
		Conn: conn,
		r:    io.MultiReader(bytes.NewReader(raw), br),
		id:   id,
	}, p.TLS)
	herr := tc.HandshakeContext(ctx)
	state := tc.ConnectionState()

	c, err := server.HelloConnection(raw, state)
	if err != nil {
		p.logf("%s: the ClientHello to the proxy: %v", conn.RemoteAddr(), err)

		c = capture.Connection{
			Host:     state.ServerName,
			Protocol: state.NegotiatedProtocol,
			Accepted: state.DidResume,
		}
	}

	// Records holds the ClientHello as sent; the message would repeat it.
	c.ClientHello = ""
	rec.conn.Connection = c
	rec.conn.Records = hex.EncodeToString(raw)

	if herr != nil {
		rec.conn.Failed = herr.Error()
	}

	p.addTLS(rec)

	if herr != nil {
		return nil, nil, herr
	}

	return tc, rec, nil
}

func (p *Proxy) addTLS(rec *tlsRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.tlsConns = append(p.tlsConns, rec)
}

func (p *Proxy) logf(format string, args ...any) {
	if p.Logf != nil {
		p.Logf(format, args...)
	}
}

// tunnelHello reads the first ClientHello the client sends through the
// tunnel its CONNECT (on the proxy connection id, HTTP/2 stream or 0) opened,
// records it with the CONNECT, and returns a reader of everything the
// tunnel carries, that hello first.
func (p *Proxy) tunnelHello(id int, stream uint32, pass io.Reader) io.Reader {
	var got bytes.Buffer

	raw, _, err := server.ReadClientHello(io.TeeReader(pass, &got))
	rest := io.MultiReader(bytes.NewReader(got.Bytes()), pass)

	if err != nil {
		p.logf("connection %d: the ClientHello through the tunnel: %v", id, err)

		return rest
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for i := len(p.requests) - 1; i >= 0; i-- {
		if r := &p.requests[i]; r.Conn == id && r.Stream == stream && r.Status == http.StatusOK {
			r.ClientHello = hex.EncodeToString(raw)

			break
		}
	}

	return rest
}

// serveH2 serves CONNECT over HTTP/2 on conn, the proxy connection id
// whose handshake rec holds, recording every frame the client sends,
// until the client or ctx ends it.
func (p *Proxy) serveH2(ctx context.Context, conn *tls.Conn, rec *tlsRecord, id int) error {
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return err
	}

	r := &h2Recorder{
		Conn:     conn,
		dec:      hpack.NewDecoder(4096, nil),
		trace:    &h2trace.Trace{},
		prefaced: make(chan struct{}),
		ended:    make(chan struct{}),
	}

	p.mu.Lock()

	rec.h2 = r
	p.mu.Unlock()

	var protocols http.Protocols

	protocols.SetUnencryptedHTTP2(true)

	// TLS is done: the HTTP/2 server sees the connection as one with prior
	// knowledge.
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p.connectH2(ctx, w, req, id, r)
		}),
		Protocols:         &protocols,
		ReadHeaderTimeout: 30 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	err := srv.Serve(&oneConn{
		conn: r,
	})
	if errors.Is(err, net.ErrClosed) {
		return nil
	}

	return err
}

// oneConn is a listener of one connection: it hands it over once, then
// fails when it has been read to its end, so the server serving it returns.
type oneConn struct {
	conn  *h2Recorder
	taken bool
}

func (l *oneConn) Accept() (net.Conn, error) {
	if !l.taken {
		l.taken = true

		return l.conn, nil
	}

	<-l.conn.ended

	return nil, net.ErrClosed
}

func (l *oneConn) Close() error {
	return nil
}

func (l *oneConn) Addr() net.Addr {
	return l.conn.LocalAddr()
}

// connectH2 answers one request on an HTTP/2 proxy connection: a CONNECT
// is recorded (with the header list r holds for it) and answered as over
// HTTP/1.1, and an accepted tunnel is handed to the capture server, the
// handler writing what it sends until either side closes it.
func (p *Proxy) connectH2(ctx context.Context, w http.ResponseWriter, req *http.Request, id int, r *h2Recorder) {
	if req.Method != http.MethodConnect {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	host, _, err := net.SplitHostPort(req.Host)
	if err != nil || !p.Allow(host) {
		w.WriteHeader(http.StatusForbidden)

		return
	}

	auth := req.Header.Get("Proxy-Authorization")
	stream, lines := r.claim(req.Host, auth)
	rec := capture.ProxyRequest{
		Kind:   capture.ProxyConnect,
		Conn:   id,
		Stream: stream,
		Target: req.Host,
		Lines:  lines,
	}

	switch {
	case p.Auth != "" && auth != p.Auth:
		rec.Status = http.StatusProxyAuthRequired
		p.record(rec)

		w.Header().Set("Proxy-Authenticate", `Basic realm="mimicry capture"`)
		w.WriteHeader(http.StatusProxyAuthRequired)

		return

	case p.Plain:
		rec.Status = http.StatusBadGateway
		p.record(rec)

		w.WriteHeader(http.StatusBadGateway)

		return
	}

	rec.Status = http.StatusOK
	p.record(rec)

	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}

	sc := &streamConn{
		body:   req.Body,
		writes: make(chan []byte),
		wrote:  make(chan streamWrite),
		done:   make(chan struct{}),
		local:  r.LocalAddr(),
		remote: r.RemoteAddr(),
	}

	out := p.ln
	if p.PlainPort != 0 && portOf(req.Host) == p.PlainPort {
		out = p.plainLn
	}

	pass := io.Reader(sc)
	if out == p.ln {
		pass = p.tunnelHello(id, stream, sc)
	}

	go func() {
		if err := out.hand(ctx, &passConn{
			Conn: sc,
			r:    pass,
			id:   id,
		}); err != nil {
			_ = sc.Close()
		}
	}()

	for {
		select {
		case b := <-sc.writes:
			n, err := w.Write(b)
			if err == nil {
				err = rc.Flush()
			}

			sc.wrote <- streamWrite{
				n:   n,
				err: err,
			}

		case <-sc.done:
			return
		case <-req.Context().Done():
			_ = sc.Close()

			return
		}
	}
}

// streamWrite is the outcome of one write to a stream.
type streamWrite struct {
	n   int
	err error
}

// streamConn is a CONNECT tunnel on an HTTP/2 stream as a net.Conn. It
// reads the request body; writes go through the stream's handler, the one
// goroutine the response writer allows. Deadlines are not kept: the
// capture's end closes the proxy connection under it.
type streamConn struct {
	body          io.ReadCloser
	writes        chan []byte
	wrote         chan streamWrite
	done          chan struct{}
	once          sync.Once
	local, remote net.Addr
}

// Read reads the tunnel. The stream ending any way is the end of the
// tunnel: the client resetting it or going away (which the HTTP/2 server
// reports as errors of its own) reads as io.EOF, and after Close as
// net.ErrClosed, as the capture server expects of a connection.
func (c *streamConn) Read(b []byte) (int, error) {
	n, err := c.body.Read(b)
	if err == nil || errors.Is(err, io.EOF) {
		return n, err
	}

	select {
	case <-c.done:
		return n, net.ErrClosed
	default:
		return n, io.EOF
	}
}

func (c *streamConn) Write(b []byte) (int, error) {
	select {
	case c.writes <- b:
	case <-c.done:
		return 0, net.ErrClosed
	}

	// The handler took b: it answers once written.
	w := <-c.wrote

	return w.n, w.err
}

func (c *streamConn) Close() error {
	c.once.Do(func() {
		close(c.done)

		_ = c.body.Close()
	})

	return nil
}

func (c *streamConn) LocalAddr() net.Addr {
	return c.local
}

func (c *streamConn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *streamConn) SetDeadline(time.Time) error {
	return nil
}

func (c *streamConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *streamConn) SetWriteDeadline(time.Time) error {
	return nil
}

// h2Recorder parses what the client sends on an HTTP/2 proxy connection as
// it is read, before the HTTP/2 server sees it, into a trace: so a
// CONNECT's header list, in order, is there when its handler asks.
//
// It also holds back what the server writes until the client's connection
// preface has arrived. Firefox lost a server preface that came while its
// connection sat in the pool before it started HTTP/2 on it (a TLS
// handshake it completed while idle): it then read the server's
// WINDOW_UPDATE as the first frame and ended the connection with
// PROTOCOL_ERROR ("First Frame Type Must Be Settings", its HTTP log,
// Firefox 159, 2026-11-09), failing the page's tunnel if it was on that
// connection. A server may send its preface after the client's (RFC 9113,
// section 3.4).
type h2Recorder struct {
	net.Conn

	mu       sync.Mutex
	buf      []byte
	preface  bool
	start    time.Time
	dec      *hpack.Decoder
	trace    *h2trace.Trace
	settings []profile.Setting
	// block is the header block being assembled, on blockID.
	block   []byte
	blockID uint32
	// connects are the CONNECT header lists no handler has claimed.
	connects []connectBlock
	// prefaced is closed once the client's preface has been read, ended
	// once reading failed.
	prefaced, ended chan struct{}
	endOnce         sync.Once
}

// connectBlock is one CONNECT's header list.
type connectBlock struct {
	stream          uint32
	authority, auth string
	lines           []string
}

func (r *h2Recorder) Read(b []byte) (int, error) {
	n, err := r.Conn.Read(b)
	if n > 0 {
		r.feed(b[:n])
	}

	if err != nil {
		r.endOnce.Do(func() {
			close(r.ended)
		})
	}

	return n, err
}

// Write writes once the client's preface has arrived.
func (r *h2Recorder) Write(b []byte) (int, error) {
	select {
	case <-r.prefaced:
		return r.Conn.Write(b)
	case <-r.ended:
		return 0, net.ErrClosed
	}
}

// The HTTP/2 frame header's length and the flags the recorder reads.
const (
	frameHeaderLen  = 9
	flagEndHeaders  = 0x4
	flagPadded      = 0x8
	flagPriority    = 0x20
	flagSettingsAck = 0x1
)

// feed parses the frames b completes.
func (r *h2Recorder) feed(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf = append(r.buf, b...)
	if !r.preface {
		if len(r.buf) < len(http2.ClientPreface) {
			return
		}

		r.buf = r.buf[len(http2.ClientPreface):]
		r.preface = true
		r.start = time.Now()

		close(r.prefaced)
	}

	for len(r.buf) >= frameHeaderLen {
		n := int(r.buf[0])<<16 | int(r.buf[1])<<8 | int(r.buf[2])
		if len(r.buf) < frameHeaderLen+n {
			break
		}

		r.frame(r.buf[:frameHeaderLen+n])

		r.buf = r.buf[frameHeaderLen+n:]
	}

	r.buf = append(r.buf[:0:0], r.buf...)
}

// frame records one whole frame, header included.
func (r *h2Recorder) frame(f []byte) {
	typ, flags := http2.FrameType(f[3]), f[4]
	stream := binary.BigEndian.Uint32(f[5:9]) & 0x7fffffff
	payload := f[frameHeaderLen:]
	tf := h2trace.Frame{
		Millis: time.Since(r.start).Milliseconds(),
		Type:   typ.String(),
		Stream: stream,
		Flags:  flags,
		Length: uint32(len(payload)),
	}

	switch typ {
	case http2.FrameSettings:
		if flags&flagSettingsAck == 0 && r.settings == nil {
			r.settings = []profile.Setting{}
			for s := payload; len(s) >= 6; s = s[6:] {
				r.settings = append(r.settings, profile.Setting{
					ID:    binary.BigEndian.Uint16(s),
					Value: binary.BigEndian.Uint32(s[2:]),
				})
			}
		}

	case http2.FrameWindowUpdate:
		if len(payload) == 4 {
			tf.Increment = binary.BigEndian.Uint32(payload) & 0x7fffffff
		}

	case http2.FrameRSTStream:
		if len(payload) == 4 {
			tf.ErrCode = http2.ErrCode(binary.BigEndian.Uint32(payload)).String()
		}

	case http2.FrameGoAway:
		if len(payload) >= 8 {
			tf.ErrCode = http2.ErrCode(binary.BigEndian.Uint32(payload[4:])).String()
		}

	case http2.FramePing:
		tf.Data = hex.EncodeToString(payload)
	case http2.FramePriority:
		if len(payload) == 5 {
			tf.Priority = priority(payload)
		}

	case http2.FrameHeaders:
		fragment := payload
		if flags&flagPadded != 0 && len(fragment) > 0 {
			pad := int(fragment[0])

			fragment = fragment[1:]

			if pad <= len(fragment) {
				fragment = fragment[:len(fragment)-pad]
			}
		}

		if flags&flagPriority != 0 && len(fragment) >= 5 {
			tf.Priority = priority(fragment)
			fragment = fragment[5:]
		}

		r.block, r.blockID = append(r.block[:0], fragment...), stream
		if flags&flagEndHeaders != 0 {
			r.headers()
		}

	case http2.FrameContinuation:
		r.block = append(r.block, payload...)
		if flags&flagEndHeaders != 0 {
			r.headers()
		}

	case http2.FrameData, http2.FramePushPromise, http2.FramePriorityUpdate:
		// The frame header says all the trace keeps.
	}

	r.trace.Frames = append(r.trace.Frames, tf)
}

// priority reads a PRIORITY frame's payload, or a HEADERS frame's priority
// fields.
func priority(b []byte) *h2trace.Priority {
	dep := binary.BigEndian.Uint32(b)

	return &h2trace.Priority{
		DependsOn: dep & 0x7fffffff,
		Exclusive: dep&0x80000000 != 0,
		Weight:    b[4],
	}
}

// headers records the complete header block, and keeps a CONNECT's header
// list for its handler.
func (r *h2Recorder) headers() {
	ins, err := h2trace.Decode(r.dec, r.block)
	req := h2trace.Request{
		Stream:       r.blockID,
		Block:        hex.EncodeToString(r.block),
		Instructions: ins,
	}

	if err != nil {
		// The HTTP/2 server fails the connection on the same block.
		r.trace.Requests = append(r.trace.Requests, req)

		return
	}

	c := connectBlock{
		stream: r.blockID,
	}
	method := ""

	for _, in := range ins {
		if in.Rep == h2trace.SizeUpdate {
			continue
		}

		c.lines = append(c.lines, in.Name+": "+in.Value)

		switch in.Name {
		case ":method":
			method = in.Value
		case ":authority":
			c.authority = in.Value
		case ":path":
			req.Path = in.Value
		case "proxy-authorization":
			if c.auth == "" {
				c.auth = in.Value
			}
		}
	}

	r.trace.Requests = append(r.trace.Requests, req)
	if method == http.MethodConnect {
		r.connects = append(r.connects, c)
	}
}

// claim returns the stream and header list of the oldest CONNECT not yet
// claimed for authority with the Proxy-Authorization value auth, as the
// HTTP/2 server hands over its request.
func (r *h2Recorder) claim(authority, auth string) (uint32, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, c := range r.connects {
		if c.authority == authority && c.auth == auth {
			r.connects = slices.Delete(r.connects, i, i+1)

			return c.stream, c.lines
		}
	}

	return 0, nil
}

// snapshot copies the trace and the first SETTINGS so far.
func (r *h2Recorder) snapshot() (*h2trace.Trace, []profile.Setting) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t := &h2trace.Trace{
		Frames:   slices.Clone(r.trace.Frames),
		Requests: slices.Clone(r.trace.Requests),
	}

	return t, slices.Clone(r.settings)
}
