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
	"strconv"
	"strings"
	"sync"

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
// p's ForwardAuthAfter places it.
func (c *Conn) Forward(p *profile.Proxy) {
	c.forward = p
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
// Cancelling req's context closes the connection.
func (c *Conn) RoundTrip(req *http.Request, done func(reusable bool)) (*http.Response, error) {
	ctx := req.Context()
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
	resp, err := http.ReadResponse(c.br, req)
	if err != nil {
		finish(false)
		return nil, contextError(ctx, err)
	}
	reusable := !resp.Close && !req.Close
	resp.Body = &body{
		ReadCloser: resp.Body,
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

func (c *Conn) writeRequest(req *http.Request) error {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	target := req.URL.RequestURI()
	reqHeader := req.Header
	auth := ""
	if c.forward != nil {
		target = req.URL.Scheme + "://" + req.URL.Host + target
		if auth = reqHeader.Get("Proxy-Authorization"); auth != "" {
			reqHeader = reqHeader.Clone()
			reqHeader.Del("Proxy-Authorization")
		}
	}
	if _, err := fmt.Fprintf(c.bw, "%s %s HTTP/1.1\r\n", req.Method, target); err != nil {
		return err
	}
	hasBody := req.Body != nil && req.Body != http.NoBody
	chunked := hasBody && req.ContentLength < 0
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
	headers, h1Order, h1Case := c.headers, c.h1Order, c.h1Case
	if plan := headerorder.PlanFrom(req.Context()); plan != nil {
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
	for _, f := range fields {
		name := h1Case[f.Name]
		if name == "" {
			// A header the profile does not name goes out as the caller
			// spelled it, as browsers send a page's own headers.
			name = callerCase(reqHeader, f.Name)
		}
		if c.forward != nil && f.Name == "connection" && c.forward.ForwardConnection != "" {
			name = c.forward.ForwardConnection
		}
		if _, err := c.bw.WriteString(name + ": " + f.Value + "\r\n"); err != nil {
			return err
		}
		if auth != "" && f.Name == c.forward.ForwardAuthAfter {
			if _, err := c.bw.WriteString("Proxy-Authorization: " + auth + "\r\n"); err != nil {
				return err
			}
			auth = ""
		}
	}
	if auth != "" {
		// Last, or where its slot's header was not sent.
		if _, err := c.bw.WriteString("Proxy-Authorization: " + auth + "\r\n"); err != nil {
			return err
		}
	}
	if _, err := c.bw.WriteString("\r\n"); err != nil {
		return err
	}
	if hasBody {
		defer func() {
			_ = req.Body.Close()
		}()
		w := io.Writer(c.bw)
		var cw io.WriteCloser
		if chunked {
			cw = httputil.NewChunkedWriter(c.bw)
			w = cw
		}
		if _, err := io.Copy(w, req.Body); err != nil {
			return err
		}
		if cw != nil {
			if err := cw.Close(); err != nil {
				return err
			}
			if _, err := c.bw.WriteString("\r\n"); err != nil {
				return err
			}
		}
	}
	return c.bw.Flush()
}

// body reports how the response body ended: read to EOF, or closed early.
type body struct {
	io.ReadCloser
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

func (b *body) Close() error {
	err := b.ReadCloser.Close()
	b.finish(b.eof)
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
