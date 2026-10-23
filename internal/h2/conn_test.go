package h2

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry/profiles"
)

// newServer runs handler on an HTTP/2 TLS test server.
func newServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// dial opens a mimicry HTTP/2 connection to srv with the Chrome profile.
func dial(t *testing.T, srv *httptest.Server) *Conn {
	t.Helper()
	raw, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName: "example.com",
		RootCAs:    srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs,
		NextProtos: []string{
			"h2",
		},
	})
	if err := tlsConn.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	p := profiles.Chrome155MacOS()
	c, err := New(tlsConn, p.HTTP2, p.Headers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
	})
	return c
}

// get sends a request with the test's context and returns the response.
func roundTrip(t *testing.T, c *Conn, req *http.Request) *http.Response {
	t.Helper()
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
	return resp
}

func newRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func readAll(t *testing.T, body io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGet(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("server saw %s", r.Proto)
		}
		w.Header().Set("X-Agent", r.UserAgent())
		_, _ = io.WriteString(w, "hello "+r.URL.RawQuery) //nolint:gosec // test handler echoing its own request
	})
	c := dial(t, srv)
	resp := roundTrip(t, c, newRequest(t, http.MethodGet, srv.URL+"/path?q=1", nil))
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK || resp.Proto != "HTTP/2.0" {
		t.Errorf("status %q proto %q", resp.Status, resp.Proto)
	}
	if body := readAll(t, resp.Body); string(body) != "hello q=1" {
		t.Errorf("body %q", body)
	}
	// The profile's user-agent is sent by default.
	if got, want := resp.Header.Get("X-Agent"), profiles.Chrome155MacOS().Headers[4].Value; got != want {
		t.Errorf("server saw user-agent %q, want the profile's %q", got, want)
	}
}

// A download far larger than the receive windows only completes if the client
// returns credit with WINDOW_UPDATE as the body is read.
func TestLargeDownload(t *testing.T) {
	const size = 40 << 20 // larger than Chrome's 15 MiB connection window
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(size))
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for sent := 0; sent < size; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	c := dial(t, srv)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	resp := roundTrip(t, c, newRequest(t, http.MethodGet, srv.URL, nil).WithContext(ctx))
	defer func() {
		_ = resp.Body.Close()
	}()
	if n := len(readAll(t, resp.Body)); n != size {
		t.Fatalf("read %d bytes, want %d", n, size)
	}
}

// An upload larger than the server's windows only completes if the client
// waits for and honours the server's WINDOW_UPDATEs.
func TestLargeUploadEchoed(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, r.Body)
	})
	c := dial(t, srv)
	body := bytes.Repeat([]byte("0123456789"), 500_000) // 5 MB
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	resp := roundTrip(t, c, newRequest(t, http.MethodPost, srv.URL, bytes.NewReader(body)).WithContext(ctx))
	defer func() {
		_ = resp.Body.Close()
	}()
	if got := readAll(t, resp.Body); !bytes.Equal(got, body) {
		t.Fatalf("echoed %d bytes, want the %d sent", len(got), len(body))
	}
}

// Concurrent requests must open streams with increasing IDs in the order
// their HEADERS go out; otherwise the server kills the connection.
func TestConcurrentRequests(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(w, r.URL.Path) //nolint:gosec // test handler echoing its own request
	})
	c := dial(t, srv)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			path := "/" + strconv.Itoa(i)
			resp, err := c.RoundTrip(newRequest(t, http.MethodGet, srv.URL+path, nil))
			if err != nil {
				t.Error(err)
				return
			}
			defer func() {
				_ = resp.Body.Close()
			}()
			if body, err := io.ReadAll(resp.Body); err != nil || string(body) != path {
				t.Errorf("request %s got %q, %v", path, body, err)
			}
		})
	}
	wg.Wait()
}

// Closing a body early resets that stream only; the connection keeps working.
func TestEarlyBodyClose(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			chunk := bytes.Repeat([]byte("x"), 64<<10)
			for range 200 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	c := dial(t, srv)
	resp := roundTrip(t, c, newRequest(t, http.MethodGet, srv.URL+"/big", nil))
	if _, err := resp.Body.Read(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	resp = roundTrip(t, c, newRequest(t, http.MethodGet, srv.URL+"/small", nil))
	if body := readAll(t, resp.Body); string(body) != "ok" {
		t.Fatalf("body after early close %q", body)
	}
}

func TestContextCancel(t *testing.T) {
	release := make(chan struct{})
	srv := newServer(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	c := dial(t, srv)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	resp, err := c.RoundTrip(newRequest(t, http.MethodGet, srv.URL, nil).WithContext(ctx))
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v, want the context's deadline", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation took too long")
	}
	if !c.CanTakeNewRequest() {
		t.Fatal("a cancelled request made the connection unusable")
	}
}

// After the server closes the connection, no new requests start on it, and
// the error says the request is safe to retry elsewhere.
func TestGoAway(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	c := dial(t, srv)
	resp := roundTrip(t, c, newRequest(t, http.MethodGet, srv.URL, nil))
	readAll(t, resp.Body)
	srv.Config.SetKeepAlivesEnabled(false)
	srv.CloseClientConnections()
	deadline := time.Now().Add(5 * time.Second)
	for c.CanTakeNewRequest() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.CanTakeNewRequest() {
		t.Fatal("connection still takes requests after the server closed it")
	}
	resp, err := c.RoundTrip(newRequest(t, http.MethodGet, srv.URL, nil))
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrConnUnusable) {
		t.Fatalf("error %v, want ErrConnUnusable so the caller can retry elsewhere", err)
	}
}
