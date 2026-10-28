package mimicry_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/profiles"
)

// A client dropped without CloseIdleConnections closes its idle
// connections, HTTP/2 and HTTP/1.1, once the garbage collector finds its
// Transport unreachable, and their goroutines end: the goroutines serving
// the connections keep the engine reachable, never the Transport
// (decision 0040). Otherwise they would last until the idle timeout.
func TestDroppedTransportClosesItsConnections(t *testing.T) {
	cert, roots := shopCertificate()

	var closed atomic.Int32

	state := func(_ net.Conn, s http.ConnState) {
		if s == http.StateClosed {
			closed.Add(1)
		}
	}

	ok := func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}

	h2 := httptest.NewUnstartedServer(http.HandlerFunc(ok))

	h2.EnableHTTP2 = true
	h2.TLS = &tls.Config{
		Certificates: []tls.Certificate{
			cert,
		},
	}
	h2.Config.ConnState = state
	h2.StartTLS()

	defer h2.Close()

	h1 := httptest.NewUnstartedServer(http.HandlerFunc(ok))

	h1.Config.ConnState = state
	h1.Start()

	defer h1.Close()

	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		target := h2.Listener.Addr().String()
		if _, port, _ := net.SplitHostPort(addr); port == "80" {
			target = h1.Listener.Addr().String()
		}

		return (&net.Dialer{}).DialContext(ctx, network, target)
	}

	before := runtime.NumGoroutine()

	func() {
		client, err := mimicry.NewClient(mimicry.Config{
			Profile:     profiles.Chrome(),
			RootCAs:     roots,
			DialContext: dial,
			// The engine holds a check built from the Transport's pins:
			// it must not hold the Transport.
			VerifyConnection: func(tls.ConnectionState) error {
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		for _, target := range []string{"https://www.shop.test/", "http://www.shop.test/"} {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}

			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			if want := map[string]int{"https": 2, "http": 1}[req.URL.Scheme]; resp.ProtoMajor != want {
				t.Fatalf("%s went over %s, want HTTP/%d", target, resp.Proto, want)
			}
		}
	}()

	deadline := time.Now().Add(10 * time.Second)

	for closed.Load() < 2 || runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("10 s after the client was dropped: %d of 2 connections closed, %d goroutines, %d before", closed.Load(), runtime.NumGoroutine(), before)
		}

		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
}

// NewClient's Timeout is Config.Timeout: 30 s by default, none when
// negative. A server that stops mid-body fails the read at the timeout
// instead of hanging.
func TestClientTimeout(t *testing.T) {
	cert, roots := shopCertificate()
	stop := make(chan struct{})

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")

		_, _ = io.WriteString(w, "half")

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))

	srv.EnableHTTP2 = true
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{
			cert,
		},
	}
	srv.StartTLS()

	defer srv.Close()
	defer close(stop)

	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}

	for _, tc := range []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{
			name: "default",
			want: 30 * time.Second,
		},
		{
			name:    "none",
			timeout: -1,
		},
		{
			name:    "stopped mid-body",
			timeout: 200 * time.Millisecond,
			want:    200 * time.Millisecond,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := mimicry.NewClient(mimicry.Config{
				Profile:     profiles.Chrome(),
				RootCAs:     roots,
				DialContext: dial,
				Timeout:     tc.timeout,
			})
			if err != nil {
				t.Fatal(err)
			}

			defer client.CloseIdleConnections()

			if client.Timeout != tc.want {
				t.Fatalf("Timeout %v, want %v", client.Timeout, tc.want)
			}

			if tc.timeout <= 0 {
				return
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://www.shop.test/", nil)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}

			defer func() {
				_ = resp.Body.Close()
			}()

			_, err = io.ReadAll(resp.Body)

			var ne net.Error
			if !errors.As(err, &ne) || !ne.Timeout() {
				t.Fatalf("reading a stopped body: %v, want a timeout", err)
			}
		})
	}
}
