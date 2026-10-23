package mimicry

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"

	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profiles"
)

// server starts a TLS server for localhost speaking HTTP/2 when h2 is set and
// HTTP/1.1 otherwise. It counts accepted connections.
type testServer struct {
	url   string
	srv   *httptest.Server
	conns *atomic.Int32
	tr    *Transport
}

func server(t *testing.T, h2 bool, handler http.HandlerFunc) testServer {
	t.Helper()
	cert, pool := testutil.Certificate(t, "localhost")
	srv := httptest.NewUnstartedServer(handler)
	conns := &atomic.Int32{}
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.EnableHTTP2 = h2
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{
			cert,
		},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	tr, err := NewTransport(Config{
		Profile: profiles.Chrome155MacOS(),
		RootCAs: pool,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	return testServer{
		url:   "https://localhost:" + port,
		srv:   srv,
		conns: conns,
		tr:    tr,
	}
}

func get(t *testing.T, client *http.Client, url string, header http.Header) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func TestProtocols(t *testing.T) {
	for _, h2 := range []bool{true, false} {
		ts := server(t, h2, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, r.Proto) //nolint:gosec // test handler echoing its own request
		})
		_, body := get(t, &http.Client{Transport: ts.tr}, ts.url, nil)
		want := "HTTP/1.1"
		if h2 {
			want = "HTTP/2.0"
		}
		if body != want {
			t.Errorf("server saw %q, want %q", body, want)
		}
	}
}

// Concurrent requests to one HTTP/2 host share one dial and one connection.
func TestSharedDial(t *testing.T) {
	ts := server(t, true, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	client := &http.Client{Transport: ts.tr}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, body := get(t, client, ts.url, nil); body != "ok" {
				t.Errorf("body %q", body)
			}
		})
	}
	wg.Wait()
	if n := ts.conns.Load(); n != 1 {
		t.Fatalf("%d connections, want 1", n)
	}
}

func TestDecompression(t *testing.T) {
	const text = "hello, compressed world"
	encode := map[string]func(io.Writer) io.WriteCloser{
		"gzip": func(w io.Writer) io.WriteCloser {
			return gzip.NewWriter(w)
		},
		"br": func(w io.Writer) io.WriteCloser {
			bw, err := brrr.NewWriter(w, 5)
			if err != nil {
				t.Fatal(err)
			}
			return bw
		},
		"zstd": func(w io.Writer) io.WriteCloser {
			zw, err := zstd.NewWriter(w)
			if err != nil {
				t.Fatal(err)
			}
			return zw
		},
	}
	ts := server(t, true, func(w http.ResponseWriter, r *http.Request) {
		enc := r.URL.Query().Get("enc")
		var buf bytes.Buffer
		cw := encode[enc](&buf)
		_, _ = io.WriteString(cw, text)
		_ = cw.Close()
		w.Header().Set("Content-Encoding", enc)
		_, _ = w.Write(buf.Bytes())
	})
	client := &http.Client{Transport: ts.tr}
	for enc := range encode {
		resp, body := get(t, client, ts.url+"/?enc="+enc, nil)
		if body != text || resp.Header.Get("Content-Encoding") != "" {
			t.Errorf("%s: body %q, Content-Encoding %q", enc, body, resp.Header.Get("Content-Encoding"))
		}
	}
	// A caller that set Accept-Encoding gets the encoded body.
	resp, body := get(t, client, ts.url+"/?enc=gzip", http.Header{
		"Accept-Encoding": {"gzip"},
	})
	if body == text || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("caller's Accept-Encoding: body decoded anyway")
	}
}

// An idle HTTP/1.1 connection the server closed is replaced transparently.
func TestStaleIdleConnection(t *testing.T) {
	ts := server(t, false, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	client := &http.Client{Transport: ts.tr}
	get(t, client, ts.url, nil)
	// Close the server side of every connection, leaving the client's idle
	// connection stale.
	ts.srv.CloseClientConnections()
	if _, body := get(t, client, ts.url, nil); body != "ok" {
		t.Fatalf("body %q after the idle connection went stale", body)
	}
	if n := ts.conns.Load(); n != 2 {
		t.Fatalf("%d connections, want 2", n)
	}
}
