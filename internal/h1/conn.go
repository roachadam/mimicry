package h1

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// Conn is a client HTTP/1.1 connection carrying one request at a time.
type Conn struct {
	conn    net.Conn
	br      *bufio.Reader
	bw      *bufio.Writer
	headers []profile.Header
	// From the profile's http1 section, when it has one: its header list
	// with lowercase names (for headerorder) and each name's casing.
	h1Order []profile.Header
	h1Case  map[string]string
	// forward, when set, sends requests whole to an HTTP proxy (Forward).
	forward *profile.Proxy
}

// New wraps conn, which carries HTTP/1.1, plain or over TLS, for profile p.
func New(conn net.Conn, p *profile.Profile) *Conn {
	c := &Conn{
		conn:    conn,
		br:      bufio.NewReader(conn),
		bw:      bufio.NewWriter(conn),
		headers: p.Headers,
	}
	if p.HTTP1 != nil {
		c.h1Order, c.h1Case = lowerList(p.HTTP1.Headers)
	}
	return c
}

// Forward makes c send its requests to an HTTP proxy as the profile's
// browser does (p): absolute-form request lines, Connection under p's
// ForwardConnection name, and a request's Proxy-Authorization header where
// p's ForwardAuth places it.
func (c *Conn) Forward(p *profile.Proxy) {
	c.forward = p
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
// headerorder, and each name's casing.
func lowerList(headers []profile.Header) ([]profile.Header, map[string]string) {
	order := make([]profile.Header, 0, len(headers))
	cases := make(map[string]string, len(headers))
	for _, h := range headers {
		lower := strings.ToLower(h.Name)
		order = append(order, profile.Header{
			Name:  lower,
			Value: h.Value,
		})
		cases[lower] = h.Name
	}
	return order, cases
}

// Close closes the connection.
func (c *Conn) Close() error {
	return c.conn.Close()
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
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
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

// RoundTrip writes req and reads its response. done is called exactly once,
// when the response body has been read to the end or closed, or when the
// request fails: reusable reports whether another request may follow on c.
// Cancelling req's context closes the connection. Informational (1xx)
// responses are skipped, except 101 Switching Protocols, which is returned
// as final and leaves the connection to the caller: never reusable.
// RoundTrip always closes req.Body.
func (c *Conn) RoundTrip(req *http.Request, done func(reusable bool)) (*http.Response, error) {
	ctx := req.Context()
	if req.Body != nil {
		defer func() {
			_ = req.Body.Close()
		}()
	}
	stop := context.AfterFunc(ctx, func() {
		_ = c.conn.Close()
	})
	var once sync.Once
	finish := func(reusable bool) {
		once.Do(func() {
			// AfterFunc reports false if it already fired: the connection is
			// closed.
			if !stop() {
				reusable = false
			}
			done(reusable)
		})
	}

	if err := c.writeRequest(req); err != nil {
		finish(false)
		return nil, contextError(ctx, err)
	}
	var resp *http.Response
	for {
		var err error
		resp, err = http.ReadResponse(c.br, req)
		if err != nil {
			finish(false)
			return nil, contextError(ctx, err)
		}
		if resp.StatusCode < 100 || resp.StatusCode > 199 || resp.StatusCode == http.StatusSwitchingProtocols {
			break
		}
	}
	reusable := !resp.Close && !req.Close && resp.StatusCode != http.StatusSwitchingProtocols
	resp.Body = &body{
		ReadCloser: resp.Body,
		conn:       c.conn,
		// A response with no body is complete as soon as it is read.
		eof: req.Method == http.MethodHead || resp.ContentLength == 0 ||
			resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified,
		finish: func(eof bool) {
			finish(reusable && eof)
		},
	}
	return resp, nil
}

// contextError prefers the context's error when cancellation caused err.
func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// head is a request's head, validated and ready to write.
type head struct {
	line   string
	fields []headerLine
}

type headerLine struct {
	name, value string
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

func (c *Conn) buildHead(req *http.Request, framing http.Header) (*head, error) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	// "h:" is "h" with the scheme's default port, as browsers write it.
	host = strings.TrimSuffix(host, ":")
	if !httpguts.ValidHostHeader(host) {
		return nil, fmt.Errorf("h1: invalid Host %q", host)
	}
	if !httpguts.ValidHeaderFieldName(req.Method) {
		return nil, fmt.Errorf("h1: invalid method %q", req.Method)
	}
	target := req.URL.RequestURI()
	reqHeader := req.Header
	auth := ""
	if c.forward != nil {
		target = req.URL.Scheme + "://" + strings.TrimSuffix(req.URL.Host, ":") + target
		if auth = reqHeader.Get("Proxy-Authorization"); auth != "" {
			reqHeader = reqHeader.Clone()
			reqHeader.Del("Proxy-Authorization")
		}
	}
	if !validTarget(target) {
		return nil, fmt.Errorf("h1: invalid request target %q", target)
	}
	headers, h1Order, h1Case := c.headers, c.h1Order, c.h1Case
	if plan := headerorder.PlanFrom(req.Context()); plan != nil {
		if plan.NoHTTP1 {
			return nil, fmt.Errorf("%w: no HTTP/1.1 header list for this request (measured over HTTP/2 only)", headerorder.ErrNotMeasured)
		}
		headers, h1Order, h1Case = plan.Headers, nil, nil
		if len(plan.HTTP1) > 0 {
			h1Order, h1Case = lowerList(plan.HTTP1)
		}
	}
	var fields []headerorder.Field
	if h1Order != nil {
		// The profile places every header, Host and framing included.
		h := reqHeader.Clone()
		if h == nil {
			h = http.Header{}
		}
		h.Set("Host", host)
		for k, v := range framing {
			h[k] = v
		}
		fields = headerorder.Build(h1Order, h, func(string) bool {
			return false
		})
	} else {
		fields = append(fields, headerorder.Field{
			Name:  "host",
			Value: host,
		}, headerorder.Field{
			Name:  "connection",
			Value: "keep-alive",
		})
		for k, v := range framing {
			fields = append(fields, headerorder.Field{
				Name:  strings.ToLower(k),
				Value: v[0],
			})
		}
		fields = append(fields, headerorder.Build(headers, reqHeader, skipHeader)...)
	}
	at := len(fields)
	if auth != "" {
		answering, _ := req.Context().Value(ChallengeAnswered{}).(bool)
		at = authIndex(fields, c.forward.ForwardAuth, answering)
	}
	h := &head{
		line:   req.Method + " " + target + " HTTP/1.1\r\n",
		fields: make([]headerLine, 0, len(fields)+1),
	}
	for i, f := range fields {
		if auth != "" && i == at {
			h.fields = append(h.fields, headerLine{
				name:  "Proxy-Authorization",
				value: auth,
			})
		}
		name := h1Case[f.Name]
		if name == "" {
			// A header the profile does not name goes out as the caller
			// spelled it, as browsers send a page's own headers.
			name = callerCase(reqHeader, f.Name)
		}
		if c.forward != nil && f.Name == "connection" && c.forward.ForwardConnection != "" {
			name = c.forward.ForwardConnection
		}
		h.fields = append(h.fields, headerLine{
			name:  name,
			value: f.Value,
		})
	}
	if auth != "" && at == len(fields) {
		h.fields = append(h.fields, headerLine{
			name:  "Proxy-Authorization",
			value: auth,
		})
	}
	for _, f := range h.fields {
		if !httpguts.ValidHeaderFieldName(f.name) {
			return nil, fmt.Errorf("h1: invalid header name %q", f.name)
		}
		if !httpguts.ValidHeaderFieldValue(f.value) {
			return nil, fmt.Errorf("h1: invalid value for header %q", f.name)
		}
	}
	return h, nil
}

func (c *Conn) writeRequest(req *http.Request) error {
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
	h, err := c.buildHead(req, framing)
	if err != nil {
		return err
	}
	if _, err := c.bw.WriteString(h.line); err != nil {
		return err
	}
	for _, f := range h.fields {
		if _, err := c.bw.WriteString(f.name + ": " + f.value + "\r\n"); err != nil {
			return err
		}
	}
	if _, err := c.bw.WriteString("\r\n"); err != nil {
		return err
	}
	if hasBody {
		if err := c.writeBody(req, chunked); err != nil {
			return err
		}
	}
	return c.bw.Flush()
}

// writeBody writes req's body: chunked, or exactly ContentLength bytes,
// failing when the body is shorter or longer.
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
	conn   net.Conn
	eof    bool
	finish func(eof bool)
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		b.eof = true
	}
	return n, err
}

// Close closes a body not read to its end by closing the connection, so
// the rest, however long, is never read.
func (b *body) Close() error {
	if !b.eof {
		_ = b.conn.Close()
	}
	err := b.ReadCloser.Close()
	b.finish(b.eof)
	if !b.eof {
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
