package h1

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry/profiles"
)

func dial(t *testing.T, addr string) *Conn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c := New(conn, profiles.Chrome155MacOS().Headers)
	t.Cleanup(func() {
		_ = c.Close()
	})
	return c
}

func newRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// do sends req, reads the whole body, and reports the body and whether the
// connection was released as reusable.
func do(t *testing.T, c *Conn, req *http.Request) (string, bool) {
	t.Helper()
	released := make(chan bool, 1)
	resp, err := c.RoundTrip(req, func(reusable bool) {
		released <- reusable
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return string(b), <-released
}

// Host and Connection come first, then the profile's headers in its order,
// with the request's own headers in their profile positions.
func TestRequestHeadOrder(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	head := make(chan []string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		r := bufio.NewReader(conn)
		var lines []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			lines = append(lines, line)
		}
		head <- lines
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	}()
	c := dial(t, ln.Addr().String())
	req := newRequest(t, http.MethodGet, "http://example.com/a?b=1", nil)
	req.Header.Set("Accept-Language", "de")
	do(t, c, req)

	lines := <-head
	names := make([]string, 0, len(lines)-1)
	for _, l := range lines[1:] {
		name, _, _ := strings.Cut(l, ":")
		names = append(names, name)
	}
	want := make([]string, 0, 2+len(profiles.Chrome155MacOS().Headers))
	want = append(want, "Host", "Connection")
	for _, h := range profiles.Chrome155MacOS().Headers {
		want = append(want, http.CanonicalHeaderKey(h.Name))
	}
	if lines[0] != "GET /a?b=1 HTTP/1.1" {
		t.Errorf("request line %q", lines[0])
	}
	if !slices.Equal(names, want) {
		t.Errorf("header order\n got %v\nwant %v", names, want)
	}
	if !slices.Contains(lines, "Accept-Language: de") {
		t.Errorf("request's Accept-Language not sent: %v", lines)
	}
}

func TestKeepAliveReuse(t *testing.T) {
	var addrs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addrs = append(addrs, r.RemoteAddr)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	c := dial(t, srv.Listener.Addr().String())
	for range 3 {
		body, reusable := do(t, c, newRequest(t, http.MethodGet, srv.URL, nil))
		if body != "ok" || !reusable {
			t.Fatalf("body %q reusable %v", body, reusable)
		}
	}
	if addrs[0] != addrs[1] || addrs[1] != addrs[2] {
		t.Fatalf("requests arrived on different connections: %v", addrs)
	}
}

func TestBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the whole body first: Go's server closes a connection whose
		// handler answered before reading a large body.
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, strings.Join(r.TransferEncoding, ",")+"|") //nolint:gosec // test handler echoing its own request
		_, _ = w.Write(body)                                                //nolint:gosec // test handler echoing its own request
	}))
	t.Cleanup(srv.Close)
	c := dial(t, srv.Listener.Addr().String())
	payload := strings.Repeat("abc", 100_000)

	sized := newRequest(t, http.MethodPost, srv.URL, strings.NewReader(payload))
	if body, _ := do(t, c, sized); body != "|"+payload {
		t.Errorf("sized upload echoed %d bytes", len(body))
	}
	// Unknown length: chunked.
	unsized := newRequest(t, http.MethodPost, srv.URL, io.MultiReader(strings.NewReader(payload)))
	unsized.ContentLength = -1
	if body, _ := do(t, c, unsized); body != "chunked|"+payload {
		t.Errorf("chunked upload echoed %q...", body[:min(len(body), 20)])
	}
}

// A body closed before its end, or a server that closes, makes the connection
// unusable; it must not be handed to the next request.
func TestNotReusable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/close" {
			w.Header().Set("Connection", "close")
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1<<20))
	}))
	t.Cleanup(srv.Close)

	c := dial(t, srv.Listener.Addr().String())
	released := make(chan bool, 1)
	resp, err := c.RoundTrip(newRequest(t, http.MethodGet, srv.URL, nil), func(reusable bool) {
		released <- reusable
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = resp.Body.Read(make([]byte, 10))
	_ = resp.Body.Close()
	if <-released {
		t.Error("connection reusable after its body was closed early")
	}

	c = dial(t, srv.Listener.Addr().String())
	if _, reusable := do(t, c, newRequest(t, http.MethodGet, srv.URL+"/close", nil)); reusable {
		t.Error("connection reusable after Connection: close")
	}
}

func TestContextCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	defer close(release)
	c := dial(t, srv.Listener.Addr().String())
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	released := make(chan bool, 1)
	resp, err := c.RoundTrip(newRequest(t, http.MethodGet, srv.URL, nil).WithContext(ctx), func(reusable bool) {
		released <- reusable
	})
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v, want the context's deadline", err)
	}
	if <-released {
		t.Error("cancelled connection released as reusable")
	}
}
