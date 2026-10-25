package h2

import (
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

	"github.com/jonboulle/clockwork"

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
	c, err := New(tlsConn, p, clockwork.NewRealClock())
	if err != nil {
		t.Fatal(err)
	}
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
