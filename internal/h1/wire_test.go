package h1

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// fakeConn replays in to the reader, then fails reads with end (io.EOF when
// nil), and records what is written.
type fakeConn struct {
	in     io.Reader
	end    error
	out    bytes.Buffer
	closed bool
}

func (f *fakeConn) Read(p []byte) (int, error) {
	if f.in != nil {
		n, err := f.in.Read(p)
		if n > 0 || !errors.Is(err, io.EOF) {
			return n, err
		}
	}
	if f.end != nil {
		return 0, f.end
	}
	return 0, io.EOF
}

func (f *fakeConn) Write(p []byte) (int, error) {
	return f.out.Write(p)
}

func (f *fakeConn) Close() error {
	f.closed = true
	return nil
}

func (f *fakeConn) LocalAddr() net.Addr {
	return &net.TCPAddr{}
}

func (f *fakeConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{}
}

func (f *fakeConn) SetDeadline(time.Time) error {
	return nil
}

func (f *fakeConn) SetReadDeadline(time.Time) error {
	return nil
}

func (f *fakeConn) SetWriteDeadline(time.Time) error {
	return nil
}

// closeBody records whether it was closed.
type closeBody struct {
	io.Reader
	closed bool
}

func (b *closeBody) Close() error {
	b.closed = true
	return nil
}

func TestRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name    string
		forward bool
		edit    func(r *http.Request)
	}{
		{
			name: "header value with CRLF",
			edit: func(r *http.Request) {
				r.Header["X-A"] = []string{"a\r\nX-Injected: 1"}
			},
		},
		{
			name: "header name with space",
			edit: func(r *http.Request) {
				r.Header["X A"] = []string{"a"}
			},
		},
		{
			name: "method with space",
			edit: func(r *http.Request) {
				r.Method = "GET / HTTP/1.1\r\n"
			},
		},
		{
			name: "target with space",
			edit: func(r *http.Request) {
				r.URL.Opaque = "/a b"
			},
		},
		{
			name: "host with CRLF",
			edit: func(r *http.Request) {
				r.Host = "example.com\r\nX: y"
			},
		},
		{
			name:    "forwarded proxy authorization with CRLF",
			forward: true,
			edit: func(r *http.Request) {
				r.Header.Set("Proxy-Authorization", "Basic x\r\nX: y")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := &fakeConn{}
			c := New(fc, profiles.Chrome())
			if tt.forward {
				c.Forward(&profile.Proxy{})
			}
			b := &closeBody{
				Reader: strings.NewReader("x"),
			}
			req := newRequest(t, http.MethodPost, "http://example.com/", nil)
			req.Body, req.ContentLength = b, 1
			tt.edit(req)
			released := make(chan bool, 1)
			if _, err := c.RoundTrip(req, func(reusable bool) {
				released <- reusable
			}); err == nil {
				t.Fatal("invalid request sent")
			}
			if fc.out.Len() != 0 {
				t.Errorf("wrote %q", fc.out.String())
			}
			if !b.closed {
				t.Error("request body not closed")
			}
			if <-released {
				t.Error("connection released as reusable")
			}
		})
	}
}

func TestBodyMustMatchContentLength(t *testing.T) {
	tests := []struct {
		name string
		body string
		cl   int64
	}{
		{
			name: "longer",
			body: "0123456789SMUGGLED",
			cl:   10,
		},
		{
			name: "shorter",
			body: "01234",
			cl:   10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := &fakeConn{}
			c := New(fc, profiles.Chrome())
			req := newRequest(t, http.MethodPost, "http://example.com/", nil)
			req.Body, req.ContentLength = io.NopCloser(strings.NewReader(tt.body)), tt.cl
			if _, err := c.RoundTrip(req, func(bool) {}); err == nil {
				t.Fatal("mismatched body sent")
			}
			if strings.Contains(fc.out.String(), "SMUGGLED") {
				t.Errorf("wrote past ContentLength: %q", fc.out.String())
			}
		})
	}
}

func TestInformationalResponses(t *testing.T) {
	fc := &fakeConn{
		in: strings.NewReader("HTTP/1.1 100 Continue\r\n\r\n" +
			"HTTP/1.1 103 Early Hints\r\nLink: </a.css>; rel=preload\r\n\r\n" +
			"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"),
	}
	c := New(fc, profiles.Chrome())
	got, reusable := do(t, c, newRequest(t, http.MethodGet, "http://example.com/", nil))
	if got != "ok" || !reusable {
		t.Errorf("got %q, reusable %v; want the final response, reusable", got, reusable)
	}
}

// Usable on a live TCP connection does not block and leaves it working.
func TestUsableOverTCP(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()
	c := dial(t, ln.Addr().String())
	srv := <-accepted
	if srv == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() {
		_ = srv.Close()
	})
	if !c.Usable() {
		t.Fatal("live connection reported unusable")
	}
	if _, err := io.WriteString(srv, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"); err != nil {
		t.Fatal(err)
	}
	if got, _ := do(t, c, newRequest(t, http.MethodGet, "http://"+ln.Addr().String()+"/", nil)); got != "ok" {
		t.Errorf("after Usable, body %q", got)
	}
}
