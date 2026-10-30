package mimicry

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"

	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// server starts a TLS server for localhost speaking HTTP/2 when h2 is set and
// HTTP/1.1 otherwise. It counts accepted connections.
type testServer struct {
	url   string
	srv   *httptest.Server
	conns *atomic.Int32
	tr    *Transport
	pool  *x509.CertPool
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
		Profile: profiles.Chrome(),
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
		pool:  pool,
	}
}

func get(t *testing.T, client *http.Client, url string, header http.Header) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	maps.Copy(req.Header, header)

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

// A body in one encoding mimicry decodes is decoded whoever set
// Accept-Encoding, which changes only what goes on the wire; with
// DisableDecompression, in an encoding it does not decode, or in several,
// the body is the server's bytes and its headers are intact. A status read
// alone never starts a decompressor.
func TestDecompression(t *testing.T) {
	const text = "hello, compressed world"

	encode := func(t *testing.T, enc string, b []byte) []byte {
		t.Helper()

		var (
			buf bytes.Buffer
			w   io.WriteCloser
		)

		switch enc {
		case "gzip":
			w = gzip.NewWriter(&buf)
		case "deflate":
			w = zlib.NewWriter(&buf)
		case "br":
			bw, err := brrr.NewWriter(&buf, 5)
			if err != nil {
				t.Fatal(err)
			}

			w = bw

		case "zstd":
			zw, err := zstd.NewWriter(&buf)
			if err != nil {
				t.Fatal(err)
			}

			w = zw
		}

		_, _ = w.Write(b)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}

		return buf.Bytes()
	}

	chrome := profiles.Chrome()

	type answer struct {
		encs []string
		wire []byte
	}

	var (
		next atomic.Pointer[answer]
		sent atomic.Pointer[string]
	)

	ts := server(t, true, func(w http.ResponseWriter, r *http.Request) {
		ae := r.Header.Get("Accept-Encoding")
		sent.Store(&ae)

		a := next.Load()

		w.Header()["Content-Encoding"] = a.encs
		_, _ = w.Write(a.wire)
	})

	for _, tc := range []struct {
		name string
		// encs are the response's Content-Encoding lines, applied in order.
		encs []string
		// label replaces encs on the response, the body still encoded by encs.
		label []string
		// headers are Config.Headers; header the request's own.
		headers http.Header
		header  http.Header
		raw     bool
		// wantAE, when set, is the Accept-Encoding the server receives.
		wantAE  string
		decoded bool
	}{
		{name: "gzip", encs: []string{"gzip"}, decoded: true},
		{name: "deflate", encs: []string{"deflate"}, decoded: true},
		{name: "br", encs: []string{"br"}, decoded: true},
		{name: "zstd", encs: []string{"zstd"}, decoded: true},
		{name: "x-gzip", encs: []string{"gzip"}, label: []string{"X-Gzip"}, decoded: true},
		{
			name:    "request's Accept-Encoding",
			encs:    []string{"gzip"},
			header:  http.Header{"Accept-Encoding": {"gzip"}},
			wantAE:  "gzip",
			decoded: true,
		},
		{
			name:    "Config.Headers Accept-Encoding",
			encs:    []string{"br"},
			headers: http.Header{"accept-encoding": {"identity"}},
			wantAE:  "identity",
			decoded: true,
		},
		{name: "DisableDecompression", encs: []string{"zstd"}, raw: true},
		{name: "unknown encoding", encs: []string{"gzip"}, label: []string{"dcb"}},
		{name: "stacked, one line", encs: []string{"gzip", "br"}, label: []string{"gzip, br"}},
		{name: "stacked, two lines", encs: []string{"gzip", "br"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := []byte(text)
			for _, enc := range tc.encs {
				wire = encode(t, enc, wire)
			}

			labels := tc.encs
			if tc.label != nil {
				labels = tc.label
			}

			next.Store(&answer{
				encs: labels,
				wire: wire,
			})

			tr, err := NewTransport(Config{
				Profile:              chrome,
				RootCAs:              ts.pool,
				Headers:              tc.headers,
				DisableDecompression: tc.raw,
			})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(tr.CloseIdleConnections)

			resp, body := get(t, &http.Client{Transport: tr}, ts.url+"/", tc.header)

			if gotAE := *sent.Load(); tc.wantAE != "" && gotAE != tc.wantAE {
				t.Errorf("sent Accept-Encoding %q, want %q", gotAE, tc.wantAE)
			}

			gotEnc := resp.Header.Values("Content-Encoding")
			gotLen := resp.Header.Get("Content-Length")

			if tc.decoded {
				if body != text || len(gotEnc) != 0 || gotLen != "" || resp.ContentLength != -1 || !resp.Uncompressed {
					t.Errorf("body %q, Content-Encoding %q, Content-Length %q (%d), Uncompressed %v; want %q decoded, both headers removed, -1",
						body, gotEnc, gotLen, resp.ContentLength, resp.Uncompressed, text)
				}

				return
			}

			wantLen := strconv.Itoa(len(wire))
			if body != string(wire) || !slices.Equal(gotEnc, labels) || gotLen != wantLen || resp.ContentLength != int64(len(wire)) || resp.Uncompressed {
				t.Errorf("body %q, Content-Encoding %q, Content-Length %q (%d), Uncompressed %v; want the %d raw bytes, %q, %s",
					body, gotEnc, gotLen, resp.ContentLength, resp.Uncompressed, len(wire), labels, wantLen)
			}
		})
	}

	// The decompressor reads the body as it opens: a caller that reads
	// only the status and closes must not have it read anything.
	t.Run("status only", func(t *testing.T) {
		for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
			src := &countingBody{
				r: bytes.NewReader(encode(t, enc, []byte(text))),
			}
			tr := fakeTransport(t, Config{}, &fakeEngine{
				answer: func(req *http.Request) *http.Response {
					return &http.Response{
						StatusCode:    http.StatusOK,
						Header:        http.Header{"Content-Encoding": {enc}},
						Body:          src,
						ContentLength: -1,
						Request:       req,
					}
				},
			})

			resp, err := tr.RoundTrip(newRequest(t, "https://a.test/"))
			if err != nil {
				t.Fatal(err)
			}

			if resp.StatusCode != http.StatusOK || !resp.Uncompressed {
				t.Fatalf("%s: status %d, Uncompressed %v", enc, resp.StatusCode, resp.Uncompressed)
			}

			if err := resp.Body.Close(); err != nil {
				t.Errorf("%s: close: %v", enc, err)
			}

			if src.reads != 0 || !src.closed {
				t.Errorf("%s: body read %d times, closed %v; want none, closed", enc, src.reads, src.closed)
			}
		}
	})
}

// countingBody counts its Reads and records its Close.
type countingBody struct {
	r      io.Reader
	reads  int
	closed bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.reads++

	return b.r.Read(p)
}

func (b *countingBody) Close() error {
	b.closed = true

	return nil
}

// fakeEngine records the requests it is sent and answers each with
// answer's response, or 200 with no body when answer is nil or returns nil.
type fakeEngine struct {
	mu     sync.Mutex
	sent   []*http.Request
	answer func(req *http.Request) *http.Response
}

func (e *fakeEngine) RoundTrip(req *http.Request) (*http.Response, error) {
	e.mu.Lock()

	e.sent = append(e.sent, req)
	e.mu.Unlock()

	if e.answer != nil {
		if resp := e.answer(req); resp != nil {
			return resp, nil
		}
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func (*fakeEngine) CloseIdleConnections() {}

// fakeTransport is a Transport for cfg sending through e.
func fakeTransport(t *testing.T, cfg Config, e *fakeEngine) *Transport {
	t.Helper()

	if cfg.Profile == nil {
		cfg.Profile = profiles.Chrome()
	}

	cfg.engine = e

	tr, err := NewTransport(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return tr
}

// failingBody fails every Read, as a reset connection does.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) {
	return 0, errors.New("connection reset")
}

func (failingBody) Close() error {
	return nil
}

// A body that cannot be decoded fails to read, and closing it then still
// works; responses without a body (204, 304, Content-Length 0) are left as
// they are, whatever their Content-Encoding; and an empty encoded redirect
// is followed.
func TestDecodeErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		enc  string
		body func() io.ReadCloser
	}{
		{name: "gzip, not gzip", enc: "gzip", body: func() io.ReadCloser {
			return io.NopCloser(strings.NewReader("plain text"))
		}},
		{name: "gzip, read fails", enc: "gzip", body: func() io.ReadCloser {
			return failingBody{}
		}},
		{name: "deflate, read fails", enc: "deflate", body: func() io.ReadCloser {
			return failingBody{}
		}},
		{name: "zstd, read fails", enc: "zstd", body: func() io.ReadCloser {
			return failingBody{}
		}},
	} {
		t.Run("undecodable/"+tc.name, func(t *testing.T) {
			tr := fakeTransport(t, Config{}, &fakeEngine{
				answer: func(req *http.Request) *http.Response {
					return &http.Response{
						StatusCode:    http.StatusOK,
						Header:        http.Header{"Content-Encoding": {tc.enc}},
						Body:          tc.body(),
						ContentLength: -1,
						Request:       req,
					}
				},
			})

			resp, err := tr.RoundTrip(newRequest(t, "https://a.test/"))
			if err != nil {
				t.Fatal(err)
			}

			if _, err := io.ReadAll(resp.Body); err == nil {
				t.Error("read a body that cannot be decoded")
			}

			if err := resp.Body.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		})
	}

	for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
		t.Run("empty/"+enc, func(t *testing.T) {
			for _, tc := range []struct {
				status int
				length int64
			}{
				{status: http.StatusNoContent, length: -1},
				{status: http.StatusNotModified, length: -1},
				{status: http.StatusOK, length: 0},
			} {
				tr := fakeTransport(t, Config{}, &fakeEngine{
					answer: func(req *http.Request) *http.Response {
						h := http.Header{"Content-Encoding": {enc}}
						if tc.length == 0 {
							h.Set("Content-Length", "0")
						}

						return &http.Response{
							StatusCode:    tc.status,
							Header:        h,
							Body:          http.NoBody,
							ContentLength: tc.length,
							Request:       req,
						}
					},
				})

				resp, err := tr.RoundTrip(newRequest(t, "https://a.test/"))
				if err != nil {
					t.Fatal(err)
				}

				b, err := io.ReadAll(resp.Body)
				if err != nil || len(b) != 0 {
					t.Errorf("status %d: read %q, %v", tc.status, b, err)
				}

				if err := resp.Body.Close(); err != nil {
					t.Errorf("status %d: close: %v", tc.status, err)
				}

				if resp.ContentLength != tc.length || (tc.length == 0 && resp.Header.Get("Content-Length") != "0") {
					t.Errorf("status %d: length %d (%q), want %d", tc.status, resp.ContentLength, resp.Header.Get("Content-Length"), tc.length)
				}
			}
		})
	}

	// Typed navigations' redirects are not measured: the redirect's order
	// is beside the point here.
	t.Run("empty redirect", func(t *testing.T) {
		e := &fakeEngine{
			answer: func(req *http.Request) *http.Response {
				if req.URL.Path != "/r" {
					return nil
				}

				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location":         {"/done"},
						"Content-Encoding": {"gzip"},
						"Content-Length":   {"0"},
					},
					Body:    http.NoBody,
					Request: req,
				}
			},
		}

		client, err := NewClient(Config{
			Profile: anyRedirectOrder(profiles.Chrome()),
			engine:  e,
		})
		if err != nil {
			t.Fatal(err)
		}

		resp, err := client.Do(newRequest(t, "https://a.test/r"))
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
		if resp.Request.URL.Path != "/done" {
			t.Errorf("ended at %s, want /done", resp.Request.URL)
		}
	})
}

// anyRedirectOrder returns p without its redirected variants (the
// navigation's and its kinds'), so a redirect's request takes its kind's
// own order: for tests of the redirect rules on hops no capture shows
// (which p refuses as not measured).
func anyRedirectOrder(p *profile.Profile) *profile.Profile {
	redirected := func(v profile.Variant) bool {
		return v.Redirected
	}

	p.Variants = slices.DeleteFunc(p.Variants, redirected)

	for _, k := range p.Kinds {
		k.Variants = slices.DeleteFunc(k.Variants, redirected)
	}

	return p
}

// Authorization and Cookie from Config.Headers stay behind once a
// redirect chain leaves an origin, even when it comes back; same-origin
// redirects keep them. (Typed navigations' redirects are not measured:
// their order is beside the point here.)
func TestConfigCredentialsStayBehindCrossOriginRedirects(t *testing.T) {
	e := &fakeEngine{
		answer: func(req *http.Request) *http.Response {
			next := map[string]string{
				"https://a.test/cross": "https://b.test/back",
				"https://b.test/back":  "https://a.test/end",
				"https://a.test/same":  "https://a.test/end",
			}[req.URL.String()]
			if next == "" {
				return nil
			}

			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": {next}},
				Body:       http.NoBody,
				Request:    req,
			}
		},
	}

	client, err := NewClient(Config{
		Profile: anyRedirectOrder(profiles.Chrome()),
		Headers: http.Header{
			"Authorization": {"Bearer secret"},
			"Cookie":        {"sid=1"},
		},
		engine: e,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, start := range []string{"https://a.test/cross", "https://a.test/same"} {
		resp, err := client.Do(newRequest(t, start))
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
	}

	want := []bool{true, false, false, true, true}
	if len(e.sent) != len(want) {
		t.Fatalf("%d requests, want %d", len(e.sent), len(want))
	}

	for i, req := range e.sent {
		auth, cookie := req.Header.Get("Authorization") != "", req.Header.Get("Cookie") != ""
		if auth != want[i] || cookie != want[i] {
			t.Errorf("%s (request %d): Authorization %v, Cookie %v, want %v", req.URL, i+1, auth, cookie, want[i])
		}
	}
}
