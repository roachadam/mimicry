package h1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	c := New(conn, profiles.Chrome())
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

func TestBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the whole body first: Go's server closes a connection whose
		// handler answered before reading a large body.
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, strings.Join(r.TransferEncoding, ",")+"|") //nolint:gosec // test handler echoing its own request
		_, _ = w.Write(body)
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
	// ContentLength 0 with a body is an unknown length too, as in net/http.
	unknown := newRequest(t, http.MethodPost, srv.URL, nil)
	unknown.Body = io.NopCloser(strings.NewReader(payload))
	if body, _ := do(t, c, unknown); body != "chunked|"+payload {
		t.Errorf("unknown-length upload echoed %q...", body[:min(len(body), 20)])
	}
}

// A body closed before its end, or a server that closes, makes the connection
// unusable; it must not be handed to the next request. Closing early must not
// read the rest: this server's body never ends.
func TestNotReusable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/close" {
			w.Header().Set("Connection", "close")
			_, _ = io.WriteString(w, "done")
			return
		}
		chunk := bytes.Repeat([]byte("x"), 1<<16)
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
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
