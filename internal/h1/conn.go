// Package h1 speaks HTTP/1.1 over an established connection, writing each
// request head as the profile's browser does.
//
// With an HTTP/1.1 header list (the profile's http1, or the plan's), every
// header, Host and framing included, follows that list's order and casing.
// Without one, Host and Connection come first, then the HTTP/2 order. A plan
// measured over HTTP/2 only is refused (headerorder.ErrNotMeasured).
package h1

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/weburl"
	"github.com/roachadam/mimicry/profile"
)

// Conn is a client HTTP/1.1 connection carrying one request at a time.
type Conn struct {
	conn net.Conn
	// out is conn's Write alone, so a body copied through a bufio.Writer
	// over it is cut by the buffer, never handed to the connection's
	// ReadFrom.
	out io.Writer
	br  *bufio.Reader
	// bw is a request's buffered writer (writers), over out, while
	// writeRequest runs.
	bw *bufio.Writer
	// w writes its request heads.
	w Writer
	// tls is the connection's TLS state, which responses carry; nil for
	// plain HTTP.
	tls *tls.ConnectionState
	// done is told how each request ended (SetDone).
	done func(reusable bool)
}

// writers hold a request on its way out: profile.MaxRecord bytes, so
// each full buffer leaves as one write, and over TLS as one full record,
// as every captured browser cuts an upload. Pooled, so an idle
// connection holds none.
var writers = sync.Pool{
	New: func() any {
		return bufio.NewWriterSize(nil, profile.MaxRecord)
	},
}

// writerOnly hides a writer's other methods (net.TCPConn's ReadFrom).
type writerOnly struct {
	io.Writer
}

// Writer builds request heads as a profile's browser writes them over
// HTTP/1.1, for a Conn or, without a connection, for the engine's plans.
// Make one with NewWriter.
type Writer struct {
	headers []profile.Header
	// From the profile's http1 section, when it has one: its header list,
	// and the same with lowercase names (for headerorder).
	h1      []profile.Header
	h1Order []profile.Header
	// uploads is how a request with a body is cut into TLS records
	// (profile http1.uploadRecords); nil: one stream.
	uploads *profile.UploadRecords
	// forward, when set, writes requests whole for an HTTP proxy
	// (Forward).
	forward *profile.Proxy
}

// NewWriter returns the Writer for profile p.
func NewWriter(p *profile.Spec) Writer {
	w := Writer{
		headers: p.Headers,
	}
	if p.HTTP1 != nil {
		w.h1, w.h1Order = p.HTTP1.Headers, lowerList(p.HTTP1.Headers)
		w.uploads = p.HTTP1.UploadRecords
	}

	return w
}

// headApart reports whether a request whose head is head bytes and whose
// body is n bytes (Content-Length) sends its head in a record of its own,
// as profile http1.uploadRecords says; otherwise head and body are one
// stream.
func (w Writer) headApart(head int, n int64) bool {
	u := w.uploads

	return u != nil && u.HeadApart && (u.JoinUpTo == 0 || int64(head)+n > int64(u.JoinUpTo))
}

// Forward returns w writing requests to an HTTP proxy as the profile's
// browser does (p): absolute-form request lines, Connection under p's
// ForwardConnection name, and a request's Proxy-Authorization header
// where p's ForwardAuth places it.
func (w Writer) Forward(p *profile.Proxy) Writer {
	w.forward = p
	return w
}

// New wraps conn, which carries HTTP/1.1, plain or over TLS, writing
// requests with w (the engine builds one per profile with NewWriter).
func New(conn net.Conn, w Writer) *Conn {
	return &Conn{
		conn: conn,
		out: writerOnly{
			Writer: conn,
		},
		br: bufio.NewReader(conn),
		w:  w,
	}
}

// Forward makes c send its requests to an HTTP proxy as the profile's
// browser does (Writer.Forward).
func (c *Conn) Forward(p *profile.Proxy) {
	c.w = c.w.Forward(p)
}

// SetTLS gives c's responses s, the connection's TLS state (nil for plain
// HTTP), as their TLS.
func (c *Conn) SetTLS(s *tls.ConnectionState) {
	c.tls = s
}

// ChallengeAnswered is the context key (value true) that marks a request
// sent again with credentials because the proxy asked for them, which
// some browsers place differently (profile.ForwardAuth.RetryLast).
type ChallengeAnswered struct{}

// authIndex is where Proxy-Authorization goes among fields: before
// fields[i], or last at len(fields).
func authIndex(fields []headerorder.Field, a *profile.ForwardAuth, answering bool) int {
	if a == nil || (answering && a.RetryLast) {
		return len(fields)
	}

	if a.Before != "" {
		if i := slices.IndexFunc(fields, func(f headerorder.Field) bool {
			return f.Name == a.Before
		}); i >= 0 {
			return i
		}

		return len(fields)
	}

	at := len(fields)
	for i, f := range fields {
		if slices.Contains(a.After, f.Name) {
			at = i + 1
		}
	}

	return at
}

// lowerList returns an HTTP/1.1 header list with lowercase names, for
// headerorder. The names share one string.
func lowerList(headers []profile.Header) []profile.Header {
	size := 0
	for _, h := range headers {
		size += len(h.Name)
	}

	var b strings.Builder

	b.Grow(size)

	for _, h := range headers {
		for i := range len(h.Name) {
			c := h.Name[i]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}

			_ = b.WriteByte(c)
		}
	}

	names := b.String()
	order := make([]profile.Header, len(headers))

	for i, h := range headers {
		name := names[:len(h.Name)]

		names = names[len(h.Name):]

		for j := range len(h.Name) {
			if h.Name[j] >= utf8.RuneSelf {
				// Unicode lowercasing can change the length.
				name = strings.ToLower(h.Name)
				break
			}
		}

		order[i] = profile.Header{
			Name:  name,
			Value: h.Value,
		}
	}

	return order
}

// listCase returns how list spells the header lower, which order (list's
// names lowercased) has at the same index; "" when it has none. The last
// match wins, as in a map built in list order.
func listCase(list, order []profile.Header, lower string) string {
	for i, hd := range slices.Backward(order) {
		if hd.Name == lower {
			return list[i].Name
		}
	}

	return ""
}

// Close closes the connection.
func (c *Conn) Close() error {
	return c.conn.Close()
}

// Hijack returns c's connection and its reader, which may hold what the
// server sent after a 101 response already: the connection is the
// caller's from then on. Call it only once RoundTrip's 101 response has
// been returned and its body closed.
func (c *Conn) Hijack() (net.Conn, *bufio.Reader) {
	return c.conn, c.br
}

// Usable reports whether an idle c can carry another request: the peer has
// not closed it and has sent nothing since the last response. It does not
// block. Call it only between requests, never during RoundTrip.
func (c *Conn) Usable() bool {
	if c.br.Buffered() > 0 {
		return false
	}

	// A deadline in the past makes the read return at once: a timeout
	// means nothing is waiting, EOF or data means the connection is done.
	if err := c.conn.SetReadDeadline(time.Unix(1, 0)); err != nil {
		return false
	}

	_, err := c.br.Peek(1)
	if resetErr := c.conn.SetReadDeadline(time.Time{}); resetErr != nil {
		return false
	}

	netErr, ok := errors.AsType[net.Error](err)

	return ok && netErr.Timeout()
}

// framing are headers this package writes itself.
var framing = map[string]bool{
	"host":              true,
	"connection":        true,
	"content-length":    true,
	"transfer-encoding": true,
}

func skipHeader(name string) bool {
	return framing[name]
}

// closing reports a request that closes its connection: req.Close, or a
// caller's Connection: close, honoured as net/http does. Other Connection
// values in the map are ignored for the profile's.
func closing(req *http.Request) bool {
	if req.Close {
		return true
	}

	for k, v := range req.Header {
		if strings.EqualFold(k, "connection") && httpguts.HeaderValuesContainsToken(v, "close") {
			return true
		}
	}

	return false
}

// SetDone sets what RoundTrip calls exactly once per request, when the
// body is read to the end or closed or the request fails; reusable
// reports whether c may carry another. Set once per connection, so a
// request allocates no callback.
func (c *Conn) SetDone(done func(reusable bool)) {
	c.done = done
}

// RoundTrip writes req, reads its response and always closes req.Body,
// then calls the SetDone callback. Cancelling req's context closes c. 1xx
// responses are skipped except 101, which is final and hands the
// connection to the caller.
func (c *Conn) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if req.Body != nil {
		defer func() {
			_ = req.Body.Close()
		}()
	}

	// b is the response's body, made up front: it also tracks the call
	// to done.
	b := &body{
		conn: c.conn,
		done: c.done,
	}

	b.stop = context.AfterFunc(ctx, func() {
		_ = c.conn.Close()
	})

	if err := c.writeRequest(req); err != nil {
		b.finish(false)

		return nil, contextError(ctx, err)
	}

	var resp *http.Response
	for {
		var err error

		resp, err = http.ReadResponse(c.br, req)
		if err != nil {
			b.finish(false)

			return nil, contextError(ctx, err)
		}

		if resp.StatusCode < 100 || resp.StatusCode > 199 || resp.StatusCode == http.StatusSwitchingProtocols {
			break
		}
	}

	b.ReadCloser = resp.Body
	b.reusable = !resp.Close && !closing(req) && resp.StatusCode != http.StatusSwitchingProtocols
	// A response with no body is complete as soon as it is read.
	b.eof.Store(req.Method == http.MethodHead || resp.ContentLength == 0 ||
		resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified)

	resp.Body = b
	resp.TLS = c.tls

	return resp, nil
}

// contextError prefers the context's error when cancellation caused err.
func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	return err
}

// Head is a request's head as written: its request line and header
// lines, in order, without line ends.
type Head struct {
	Line   string
	Fields []Field
}

// Field is one header line, its name as written.
type Field struct {
	Name  string
	Value string
}

// validTarget reports whether a request target has no spaces or control
// characters, which would split the request line.
func validTarget(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		if s[i] <= ' ' || s[i] == 0x7f {
			return false
		}
	}

	return true
}

// Head returns the head w writes for req, its framing
// (Content-Length, Transfer-Encoding) from req's body as RoundTrip sends
// it. An error is one RoundTrip returns before writing anything.
func (w Writer) Head(req *http.Request) (Head, error) {
	h, _, err := w.head(req)
	return h, err
}

// head is Head, and whether the body goes chunked.
func (w Writer) head(req *http.Request) (Head, bool, error) {
	// As net/http: a body with ContentLength 0 has an unknown length.
	hasBody := req.Body != nil && req.Body != http.NoBody
	chunked := hasBody && req.ContentLength <= 0

	var framing http.Header

	switch {
	case chunked:
		framing = http.Header{
			"Transfer-Encoding": {"chunked"},
		}

	case req.ContentLength > 0 || req.Method == http.MethodPost || req.Method == http.MethodPut || req.Method == http.MethodPatch:
		framing = http.Header{
			"Content-Length": {strconv.FormatInt(max(req.ContentLength, 0), 10)},
		}
	}

	h, err := w.build(req, framing)

	return h, chunked, err
}

func (w Writer) build(req *http.Request, framing http.Header) (Head, error) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	// "h:" is "h" with the scheme's default port, as browsers write it.
	host = strings.TrimSuffix(host, ":")
	if !httpguts.ValidHostHeader(host) {
		return Head{}, fmt.Errorf("h1: invalid Host %q", host)
	}

	if !httpguts.ValidHeaderFieldName(req.Method) {
		return Head{}, fmt.Errorf("h1: invalid method %q", req.Method)
	}

	target := weburl.RequestURI(req.URL)
	// Framing comes from the request, never the caller's map.
	reqHeader := headerorder.WithoutFraming(req.Header)
	auth := ""

	if w.forward != nil {
		target = req.URL.Scheme + "://" + strings.TrimSuffix(req.URL.Host, ":") + target

		if auth = reqHeader.Get("Proxy-Authorization"); auth != "" {
			reqHeader = reqHeader.Clone()
			reqHeader.Del("Proxy-Authorization")
		}
	}

	if !validTarget(target) {
		return Head{}, fmt.Errorf("h1: invalid request target %q", target)
	}

	headers, h1Order, h1List := w.headers, w.h1Order, w.h1

	var (
		table    *profile.HeaderTable
		ownRun   bool
		lowerOwn bool
	)

	if plan := headerorder.PlanFrom(req.Context()); plan != nil {
		if plan.NoHTTP1 {
			return Head{}, fmt.Errorf("%w: no HTTP/1.1 header list for this request (measured over HTTP/2 only)", headerorder.ErrNotMeasured)
		}

		headers, h1Order, h1List, table, ownRun, lowerOwn = plan.Headers, nil, nil, plan.Table, plan.OwnRun, plan.LowerOwn
		if len(plan.HTTP1) > 0 {
			h1Order, h1List = lowerList(plan.HTTP1), plan.HTTP1
		}
	}

	// Room for the usual list on the stack; h.Fields is the copy kept.
	var buf [32]headerorder.Field

	fields := buf[:0]
	if h1Order != nil {
		// The profile places every header, Host and framing included.
		h := reqHeader.Clone()
		if h == nil {
			h = http.Header{}
		}

		h.Set("Host", host)
		maps.Copy(h, framing)

		if closing(req) && slices.ContainsFunc(h1Order, func(hd profile.Header) bool {
			return hd.Name == "connection"
		}) {
			h.Set("Connection", "close")
		}

		fields = headerorder.Build(fields, h1Order, h, nil, func(string) bool {
			return false
		}, table, ownRun)
	} else {
		fields = append(fields, headerorder.Field{
			Name:  "host",
			Value: host,
		}, headerorder.Field{
			Name:  "connection",
			Value: "keep-alive",
		})
		if closing(req) {
			fields[1].Value = "close"
		}

		for k, v := range framing {
			fields = append(fields, headerorder.Field{
				Name:  strings.ToLower(k),
				Value: v[0],
			})
		}

		fields = headerorder.Build(fields, headers, reqHeader, nil, skipHeader, table, ownRun)
	}

	// The caller's placements move lines the list wrote, Host and framing
	// included; Proxy-Authorization is placed after them.
	fields, err := headerorder.Place(fields, headerorder.PlacementsFrom(req.Context()))
	if err != nil {
		return Head{}, err
	}

	at := len(fields)
	if auth != "" {
		answering, _ := req.Context().Value(ChallengeAnswered{}).(bool)

		at = authIndex(fields, w.forward.ForwardAuth, answering)
	}

	h := Head{
		Line:   req.Method + " " + target + " HTTP/1.1",
		Fields: make([]Field, 0, len(fields)+1),
	}
	for i, f := range fields {
		if auth != "" && i == at {
			h.Fields = append(h.Fields, Field{
				Name:  "Proxy-Authorization",
				Value: auth,
			})
		}

		name := listCase(h1List, h1Order, f.Name)
		switch {
		case name != "":
		case lowerOwn:
			// fetch()'s Headers object lowercases a page's names.
			name = f.Name
		default:
			// A header the profile does not name goes out as the caller
			// spelled it, as browsers send a page's own headers (XHR).
			name = callerCase(reqHeader, f.Name)
		}

		if w.forward != nil && f.Name == "connection" && w.forward.ForwardConnection != "" {
			name = w.forward.ForwardConnection
		}

		h.Fields = append(h.Fields, Field{
			Name:  name,
			Value: f.Value,
		})
	}

	if auth != "" && at == len(fields) {
		h.Fields = append(h.Fields, Field{
			Name:  "Proxy-Authorization",
			Value: auth,
		})
	}

	for _, f := range h.Fields {
		if !httpguts.ValidHeaderFieldName(f.Name) {
			return Head{}, fmt.Errorf("h1: invalid header name %q", f.Name)
		}

		if !httpguts.ValidHeaderFieldValue(f.Value) {
			return Head{}, fmt.Errorf("h1: invalid value for header %q", f.Name)
		}
	}

	return h, nil
}

// writeRequest writes req's head and body through a pooled writer of
// profile.MaxRecord bytes: each full buffer is one write, so over TLS each
// record but a head's or body's last is full, as browsers cut uploads.
// The head goes alone first where the profile says (Writer.headApart);
// otherwise head and body are one stream.
func (c *Conn) writeRequest(req *http.Request) error {
	h, chunked, err := c.w.head(req)
	if err != nil {
		return err
	}

	c.bw = writers.Get().(*bufio.Writer) //nolint:forcetypeassert // the pool holds only this type
	c.bw.Reset(c.out)

	defer func() {
		c.bw.Reset(nil)
		writers.Put(c.bw)

		c.bw = nil
	}()

	if err := c.writeLine(h.Line, "", ""); err != nil {
		return err
	}

	headLen := len(h.Line) + len("\r\n\r\n")
	for _, f := range h.Fields {
		if err := c.writeLine(f.Name, ": ", f.Value); err != nil {
			return err
		}

		headLen += len(f.Name) + len(": \r\n") + len(f.Value)
	}

	if _, err := c.bw.WriteString("\r\n"); err != nil {
		return err
	}

	if req.Body == nil || req.Body == http.NoBody {
		return c.bw.Flush()
	}

	// A chunked body is never a browser's over HTTP/1.1: it stays one
	// stream with its head.
	if !chunked && c.w.headApart(headLen, req.ContentLength) {
		if err := c.bw.Flush(); err != nil {
			return err
		}
	}

	if err := c.writeBody(req, chunked); err != nil {
		return err
	}

	return c.bw.Flush()
}

// writeLine writes the line name+sep+value and CRLF without building it,
// into the buffer, where a bufio.Writer error sticks for the last write
// to return. A line too long for the buffer's free space is written as one
// string, as before: in pieces, it could reach the connection in other
// writes.
func (c *Conn) writeLine(name, sep, value string) error {
	if len(name)+len(sep)+len(value)+2 > c.bw.Available() {
		_, err := c.bw.WriteString(name + sep + value + "\r\n")
		return err
	}

	_, _ = c.bw.WriteString(name)
	_, _ = c.bw.WriteString(sep)
	_, _ = c.bw.WriteString(value)

	_, err := c.bw.WriteString("\r\n")

	return err
}

// writeBody writes req's body: chunked, or exactly ContentLength bytes,
// failing when the body is shorter or longer. The buffered writer reads
// the body into its free space and writes each full buffer.
func (c *Conn) writeBody(req *http.Request, chunked bool) error {
	if chunked {
		cw := httputil.NewChunkedWriter(c.bw)
		if _, err := io.Copy(cw, req.Body); err != nil {
			return err
		}

		if err := cw.Close(); err != nil {
			return err
		}

		_, err := c.bw.WriteString("\r\n")

		return err
	}

	n, err := io.CopyN(c.bw, req.Body, req.ContentLength)
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("h1: body is %d bytes, ContentLength %d", n, req.ContentLength)
	}

	if err != nil {
		return err
	}

	var extra [1]byte
	if m, _ := io.ReadFull(req.Body, extra[:]); m > 0 {
		return fmt.Errorf("h1: body longer than ContentLength %d", req.ContentLength)
	}

	return nil
}

// body reports how the response body ended: read to EOF, or closed early.
type body struct {
	io.ReadCloser
	conn net.Conn
	// eof is atomic: a decoded body's drain (the root package) may close
	// the body while a Read is blocked.
	eof atomic.Bool
	// reusable: the response leaves the connection reusable once its
	// body is read to the end.
	reusable bool
	// stop stops closing the connection on the request's cancellation;
	// done is the connection's (SetDone), called once (finish).
	stop func() bool
	done func(reusable bool)
	once sync.Once
}

// finish calls done, the first time only. stop reports false if the
// context already closed the connection: it is not reusable.
func (b *body) finish(reusable bool) {
	b.once.Do(func() {
		if !b.stop() {
			reusable = false
		}

		b.done(reusable)
	})
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		b.eof.Store(true)
	}

	return n, err
}

// Close closes a body not read to its end by closing the connection, so
// the rest, however long, is never read.
func (b *body) Close() error {
	eof := b.eof.Load()
	if !eof {
		_ = b.conn.Close()
	}

	err := b.ReadCloser.Close()
	b.finish(b.reusable && eof)

	if !eof {
		// The connection's read error from the abandoned rest is expected.
		return nil
	}

	return err
}

// callerCase returns how h spells the header lower, or its canonical form
// when h does not have it.
func callerCase(h http.Header, lower string) string {
	for k := range h {
		if strings.EqualFold(k, lower) {
			return k
		}
	}

	return http.CanonicalHeaderKey(lower)
}
