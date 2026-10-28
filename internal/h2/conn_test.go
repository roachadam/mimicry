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
	"slices"
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

	srvTr, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httptest client Transport is %T", srv.Client().Transport)
	}

	tlsConn := tls.Client(raw, &tls.Config{
		ServerName: "example.com",
		RootCAs:    srvTr.TLSClientConfig.RootCAs,
		NextProtos: []string{
			"h2",
		},
	})
	if err := tlsConn.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	p := profiles.Chrome()

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
// their HEADERS go out; otherwise the server kills the connection. Each
// stream's request and response bodies must come through whole and its
// own, though their buffers are pooled across streams, and so must its
// response headers, though one array holds all of a response's values: a
// second value, or the caller's append, must not overwrite another name's.
func TestConcurrentRequests(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)

		if r.Header.Get("Echo-Length") != "" {
			w.Header().Set("Content-Length", strconv.FormatInt(r.ContentLength+int64(len(r.URL.Path)), 10))
		}

		w.Header().Set("Alt-Svc", "h3=\""+r.URL.Path+"\"")
		w.Header().Add("Set-Cookie", "a="+r.URL.Path)
		w.Header().Add("Set-Cookie", "b="+r.URL.Path)
		w.Header().Set("X-Stream", r.URL.Path)

		_, _ = io.WriteString(w, r.URL.Path) //nolint:gosec // test handler echoing its own request
		_, _ = io.Copy(w, r.Body)
	})
	c := dial(t, srv)

	var wg sync.WaitGroup

	for i := range 20 {
		wg.Go(func() {
			path := "/" + strconv.Itoa(i)
			// A pattern that differs by stream and by offset, so a chunk
			// from another stream or another place shows.
			sent := make([]byte, 70_000+i*4099)
			for j := range sent {
				sent[j] = byte(j%251) ^ byte(i)
			}

			// Even requests declare their length and get it back; odd ones
			// stream both ways.
			var body io.Reader = bytes.NewReader(sent)
			if i%2 == 1 {
				body = io.MultiReader(body)
			}

			req := newRequest(t, http.MethodPost, srv.URL+path, body)
			if i%2 == 0 {
				req.Header.Set("Echo-Length", "1")
			}

			resp, err := c.RoundTrip(req)
			if err != nil {
				t.Error(err)

				return
			}

			defer func() {
				_ = resp.Body.Close()
			}()

			checkHeader(t, path, resp.Header)

			got, err := io.ReadAll(resp.Body)
			if err != nil || string(got) != path+string(sent) {
				t.Errorf("request %s got %d bytes starting %q, %v", path, len(got), got[:min(len(got), 16)], err)
			}
		})
	}

	wg.Wait()
}

// checkHeader wants the headers TestConcurrentRequests' server sends for
// path in h, then appends to every name and wants the others unchanged.
func checkHeader(t *testing.T, path string, h http.Header) {
	t.Helper()

	for name, want := range map[string][]string{
		"Alt-Svc":    {"h3=\"" + path + "\""},
		"Set-Cookie": {"a=" + path, "b=" + path},
		"X-Stream":   {path},
	} {
		if !slices.Equal(h[name], want) {
			t.Errorf("request %s got %s %q, want %q", path, name, h[name], want)
		}
	}

	before := h.Clone()
	for name := range h {
		h[name] = append(h[name], "appended")
	}

	for name, want := range before {
		if got := h[name]; !slices.Equal(got[:len(got)-1], want) {
			t.Errorf("request %s: appending to the headers changed %s from %q to %q", path, name, want, got)
		}
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
