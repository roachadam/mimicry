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
	conn        net.Conn
	br          *bufio.Reader
	bw          *bufio.Writer
	headers     []profile.Header
	cookieOrder string
}

// New wraps conn, which carries HTTP/1.1, plain or over TLS, for profile p.
func New(conn net.Conn, p *profile.Profile) *Conn {
	return &Conn{
		conn:        conn,
		br:          bufio.NewReader(conn),
		bw:          bufio.NewWriter(conn),
		headers:     p.Headers,
		cookieOrder: p.CookieOrder,
	}
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
	if _, err := fmt.Fprintf(c.bw, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n", req.Method, req.URL.RequestURI(), host); err != nil {
		return err
	}
	hasBody := req.Body != nil && req.Body != http.NoBody
	chunked := hasBody && req.ContentLength < 0
	switch {
	case chunked:
		if _, err := c.bw.WriteString("Transfer-Encoding: chunked\r\n"); err != nil {
			return err
		}
	case req.ContentLength > 0 || req.Method == http.MethodPost || req.Method == http.MethodPut || req.Method == http.MethodPatch:
		if _, err := c.bw.WriteString("Content-Length: " + strconv.FormatInt(max(req.ContentLength, 0), 10) + "\r\n"); err != nil {
			return err
		}
	}
	for _, f := range headerorder.Build(c.headers, req.Header, skipHeader) {
		if f.Name == "cookie" && c.cookieOrder != profile.CookieOrderCreation {
			f.Value = strings.Join(headerorder.Cookies(f.Value, c.cookieOrder), "; ")
		}
		if _, err := c.bw.WriteString(http.CanonicalHeaderKey(f.Name) + ": " + f.Value + "\r\n"); err != nil {
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
