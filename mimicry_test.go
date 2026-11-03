package mimicry

import (
	"bytes"
	"compress/flate"
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
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// testServer is a test server and a Transport that trusts it.
type testServer struct {
	url  string
	srv  *httptest.Server
	tr   *Transport
	pool *x509.CertPool
}

// server starts a TLS server for localhost speaking HTTP/2 when h2 is set and
// HTTP/1.1 otherwise.
func server(t *testing.T, h2 bool, handler http.HandlerFunc) testServer {
	t.Helper()

	cert, pool := testutil.Certificate(t, "localhost")
	srv := httptest.NewUnstartedServer(handler)

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
		Profile: profiles.ChromeMacOS(),
		RootCAs: pool,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(tr.CloseIdleConnections)

	return testServer{
		url:  "https://localhost:" + port,
		srv:  srv,
		tr:   tr,
		pool: pool,
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

// encodeBody is b in enc: a Content-Encoding, or "raw deflate" (RFC 1951).
func encodeBody(t *testing.T, enc string, b []byte) []byte {
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
	case "raw deflate":
		fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
		if err != nil {
			t.Fatal(err)
		}

		w = fw

	case "raw deflate, zlib-like":
		// Stored blocks whose first two bytes pass zlib's CM and FCHECK
		// checks but not CINFO: a non-final block of n bytes after a
		// first byte with CINFO over 7, then the rest in a final one.
		for b0 := 0x88; b0 <= 0xf8; b0 += 0x10 {
			for n := range len(b) + 1 {
				if (b0<<8|n)%31 != 0 {
					continue
				}

				buf.Write([]byte{byte(b0), byte(n), 0, ^byte(n), 0xff})
				buf.Write(b[:n])

				rest := len(b) - n
				buf.Write([]byte{0x01, byte(rest), byte(rest >> 8), ^byte(rest), ^byte(rest >> 8)})
				buf.Write(b[n:])

				return buf.Bytes()
			}
		}

		t.Fatalf("no zlib-like stored block for %d bytes", len(b))

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

// A body in one encoding mimicry decodes is decoded whoever set
// Accept-Encoding, which changes only what goes on the wire; with
// DisableDecompression, in an encoding it does not decode, or in several,
// the body is the server's bytes and its headers are intact. A status read
// alone never starts a decompressor. Over HTTP/1.1 and HTTP/2 alike, deflate
// is read zlib-wrapped or raw, as the browsers read it.
func TestDecompression(t *testing.T) {
	const text = "hello, compressed world"

	chrome := profiles.ChromeMacOS()

	type answer struct {
		encs []string
		wire []byte
	}

	var (
		next atomic.Pointer[answer]
		sent atomic.Pointer[string]
	)

	for _, h2 := range []bool{false, true} {
		proto := "HTTP/1.1"
		if h2 {
			proto = "HTTP/2"
		}

		t.Run(proto, func(t *testing.T) {
			ts := server(t, h2, func(w http.ResponseWriter, r *http.Request) {
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
				// headers are Config.Header; header the request's own.
				headers http.Header
				header  http.Header
				raw     bool
				// wantAE, when set, is the Accept-Encoding the server receives.
				wantAE  string
				decoded bool
			}{
				{name: "gzip", encs: []string{"gzip"}, decoded: true},
				{name: "deflate", encs: []string{"deflate"}, decoded: true},
				{name: "raw deflate", encs: []string{"raw deflate"}, label: []string{"deflate"}, decoded: true},
				{name: "raw deflate, zlib-like", encs: []string{"raw deflate, zlib-like"}, label: []string{"deflate"}, decoded: true},
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
					name:    "Config.Header Accept-Encoding",
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
						wire = encodeBody(t, enc, wire)
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
						Header:               tc.headers,
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
		})
	}

	// The decompressor reads the body as it opens: a caller that reads
	// only the status and closes must not have it read anything.
	t.Run("status only", func(t *testing.T) {
		for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
			src := &countingBody{
				r: bytes.NewReader(encodeBody(t, enc, []byte(text))),
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

	if cfg.Profile == (profile.Profile{}) {
		cfg.Profile = profiles.ChromeMacOS()
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
			Profile: anyRedirectOrder(profiles.ChromeMacOS()),
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
func anyRedirectOrder(p profile.Profile) profile.Profile {
	redirected := func(v profile.Variant) bool {
		return v.Redirected
	}

	return p.With(func(s *profile.Spec) {
		s.Variants = slices.DeleteFunc(s.Variants, redirected)

		for _, k := range s.Kinds {
			k.Variants = slices.DeleteFunc(k.Variants, redirected)
		}
	})
}

// Credentials stay behind once a redirect chain leaves an origin, even
// when it comes back; same-origin redirects keep them. Config.Header's
// Authorization and Cookie stay behind on any kind; a fetch's own
// Authorization as the Fetch standard's HTTP-redirect fetch says, before
// the preflight it would need: a subdomain is another origin, which
// http.Client's rule lets it reach (issue #51). (Typed navigations'
// redirects are not measured: their order is beside the point here.)
func TestCredentialsStayBehindCrossOriginRedirects(t *testing.T) {
	next := map[string]string{
		"https://a.test/cross": "https://b.test/back",
		"https://b.test/back":  "https://a.test/end",
		"https://a.test/same":  "https://a.test/end",
		"https://a.test/sub":   "https://sub.a.test/end",
		"https://a.test/site":  "https://b.test/end",
	}
	config := http.Header{
		"Authorization": {"Bearer secret"},
		"Cookie":        {"sid=1"},
	}
	own := http.Header{
		"Authorization": {"Bearer secret"},
	}

	for _, tc := range []struct {
		name  string
		start string
		// fetch: a fetch() from page https://a.test/ with its own
		// Authorization; a typed navigation with config's otherwise.
		fetch bool
		// sent is each request sent: method, URL, Authorization, Cookie.
		sent []string
	}{
		{
			name:  "config, cross-origin and back",
			start: "https://a.test/cross",
			sent:  []string{"GET https://a.test/cross Bearer secret sid=1", "GET https://b.test/back  ", "GET https://a.test/end  "},
		},
		{
			name:  "config, same-origin",
			start: "https://a.test/same",
			sent:  []string{"GET https://a.test/same Bearer secret sid=1", "GET https://a.test/end Bearer secret sid=1"},
		},
		{
			name:  "fetch, same-origin",
			start: "https://a.test/same",
			fetch: true,
			sent:  []string{"GET https://a.test/same Bearer secret ", "GET https://a.test/end Bearer secret "},
		},
		{
			name:  "fetch, subdomain",
			start: "https://a.test/sub",
			fetch: true,
			sent:  []string{"GET https://a.test/sub Bearer secret ", "GET https://sub.a.test/end  "},
		},
		{
			name:  "fetch, cross-site",
			start: "https://a.test/site",
			fetch: true,
			sent:  []string{"GET https://a.test/site Bearer secret ", "GET https://b.test/end  "},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &fakeEngine{
				answer: func(req *http.Request) *http.Response {
					if req.Method == http.MethodOptions {
						// Any preflight from the page is allowed: the
						// test sees it sent.
						return &http.Response{
							StatusCode: http.StatusNoContent,
							Header: http.Header{
								"Access-Control-Allow-Origin":  {"https://a.test"},
								"Access-Control-Allow-Headers": {"authorization"},
							},
							Body:    http.NoBody,
							Request: req,
						}
					}

					to := next[req.URL.String()]
					if to == "" {
						return nil
					}

					return &http.Response{
						StatusCode: http.StatusFound,
						Header:     http.Header{"Location": {to}},
						Body:       http.NoBody,
						Request:    req,
					}
				},
			}

			cfg := Config{
				Profile: anyRedirectOrder(profiles.ChromeMacOS()),
				engine:  e,
			}
			req := newRequest(t, tc.start)

			if tc.fetch {
				maps.Copy(req.Header, own)

				req = With(req, Request{
					Kind: Fetch,
					From: &url.URL{
						Scheme: "https",
						Host:   "a.test",
						Path:   "/",
					},
				})
			} else {
				cfg.Header = config
			}

			client, err := NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}

			_ = resp.Body.Close()

			sent := make([]string, 0, len(e.sent))
			for _, req := range e.sent {
				sent = append(sent, req.Method+" "+req.URL.String()+" "+req.Header.Get("Authorization")+" "+req.Header.Get("Cookie"))
			}

			if !slices.Equal(sent, tc.sent) {
				t.Errorf("sent\n%s\nwant\n%s", strings.Join(sent, "\n"), strings.Join(tc.sent, "\n"))
			}
		})
	}
}

// A decoded body whose server ends the response after the compressed
// stream (HTTP/2 END_STREAM in a later empty DATA frame, HTTP/1.1's
// terminal chunk) is read in full at once, without waiting for that end;
// closed, it is drained in the background, so over HTTP/2 the client sends
// no RST_STREAM and over HTTP/1.1 it reuses the connection, as browsers do.
// A server that never ends the response costs neither the read nor the
// Close a wait, and the drain still finishes.
func TestDecodedBodyEnds(t *testing.T) {
	const text = "hello, compressed world"

	encs := []string{"gzip", "deflate", "br", "zstd"}

	// readsToEnd: the gzip (multistream) and zstd readers look for another
	// member or frame, so their own io.EOF waits for the body's; the test
	// ends those responses before reading them.
	readsToEnd := map[string]bool{
		"gzip": true,
		"zstd": true,
	}

	// readAndClose reads resp in full and closes it, both before the
	// server ends the response, and returns the decoder's drain.
	readAndClose := func(t *testing.T, enc string, resp *http.Response) chan struct{} {
		t.Helper()

		b, err := io.ReadAll(resp.Body)
		if err != nil || string(b) != text || !resp.Uncompressed {
			t.Errorf("%s: read %q, %v, Uncompressed %v", enc, b, err, resp.Uncompressed)
		}

		if err := resp.Body.Close(); err != nil {
			t.Errorf("%s: close: %v", enc, err)
		}

		d, ok := resp.Body.(*decoder)
		if !ok || d.drained == nil {
			t.Fatalf("%s: body %T not draining", enc, resp.Body)
		}

		return d.drained
	}

	waitDrained := func(t *testing.T, enc string, drained chan struct{}) {
		t.Helper()

		select {
		case <-drained:
		case <-time.After(5 * drainTimeout):
			t.Fatalf("%s: drain still running", enc)
		}
	}

	t.Run("HTTP/2", func(t *testing.T) {
		srv := startLateEndH2(t, text)
		client := &http.Client{
			Transport: srv.tr,
		}

		for _, enc := range encs {
			resp, err := client.Do(newRequest(t, srv.url+"/"+enc))
			if err != nil {
				t.Fatal(err)
			}

			end := <-srv.ends
			if readsToEnd[enc] {
				end()
			}

			drained := readAndClose(t, enc, resp)

			if !readsToEnd[enc] {
				end()
			}

			waitDrained(t, enc, drained)
		}

		// The server reads frames in order: any reset sent has arrived by
		// the time this request has.
		_, _ = get(t, client, srv.url+"/sync", nil)

		if got := srv.get(); len(got) != 0 {
			t.Errorf("RST_STREAMs received: %v; want none", got)
		}

		// Never ended: the drain gives up after drainTimeout.
		resp, err := client.Do(newRequest(t, srv.url+"/br"))
		if err != nil {
			t.Fatal(err)
		}

		waitDrained(t, "never ended", readAndClose(t, "br", resp))
	})

	t.Run("HTTP/1.1", func(t *testing.T) {
		var (
			mu    sync.Mutex
			conns map[string]bool
		)

		// step ends each response the test lets end.
		step := make(chan struct{})

		ts := server(t, false, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()

			conns[r.RemoteAddr] = true
			mu.Unlock()

			enc := strings.TrimPrefix(r.URL.Path, "/")

			w.Header().Set("Content-Encoding", enc)

			_, _ = w.Write(encodeBody(t, enc, []byte(text)))

			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

			select {
			case <-step:
			case <-r.Context().Done():
			}
		})

		do := func(t *testing.T, tr *Transport, enc string, end bool) chan struct{} {
			t.Helper()

			resp, err := (&http.Client{Transport: tr}).Do(newRequest(t, ts.url+"/"+enc))
			if err != nil {
				t.Fatal(err)
			}

			if resp.TransferEncoding == nil {
				t.Errorf("%s: Transfer-Encoding %q; want chunked", enc, resp.TransferEncoding)
			}

			if end {
				step <- struct{}{}
			}

			return readAndClose(t, enc, resp)
		}

		for _, enc := range encs {
			mu.Lock()

			conns = map[string]bool{}
			mu.Unlock()

			tr, err := NewTransport(Config{
				Profile: profiles.ChromeMacOS(),
				RootCAs: ts.pool,
			})
			if err != nil {
				t.Fatal(err)
			}

			for range 2 {
				drained := do(t, tr, enc, readsToEnd[enc])

				if !readsToEnd[enc] {
					step <- struct{}{}
				}

				waitDrained(t, enc, drained)
			}

			tr.CloseIdleConnections()

			mu.Lock()

			n := len(conns)
			mu.Unlock()

			if n != 1 {
				t.Errorf("%s: %d connections for two requests; want 1", enc, n)
			}
		}

		// Never ended: the drain gives up after drainTimeout, closing the
		// connection, which ends the handler.
		tr, err := NewTransport(Config{
			Profile: profiles.ChromeMacOS(),
			RootCAs: ts.pool,
		})
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(tr.CloseIdleConnections)

		waitDrained(t, "never ended", do(t, tr, "br", false))
	})
}

// lateEndH2 is a raw HTTP/2 server answering /<encoding> with text in that
// encoding and no Content-Length, then sending ends the function that ends
// the stream in an empty DATA frame, and recording the RST_STREAMs it
// receives.
type lateEndH2 struct {
	url  string
	tr   *Transport
	ends chan func()

	mu     sync.Mutex
	resets map[uint32]http2.ErrCode
}

func (s *lateEndH2) get() map[uint32]http2.ErrCode {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.resets)
}

func startLateEndH2(t *testing.T, text string) *lateEndH2 {
	t.Helper()

	cert, pool := testutil.Certificate(t, "localhost")

	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = ln.Close()
	})

	tl := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{
			cert,
		},
		NextProtos: []string{
			"h2",
		},
	})

	tr, err := NewTransport(Config{
		Profile:      profiles.ChromeMacOS(),
		RootCAs:      pool,
		DisableHTTP3: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(tr.CloseIdleConnections)

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	s := &lateEndH2{
		url:    "https://localhost:" + port,
		tr:     tr,
		ends:   make(chan func(), 16),
		resets: map[uint32]http2.ErrCode{},
	}

	bodies := map[string][]byte{}
	for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
		bodies["/"+enc] = encodeBody(t, enc, []byte(text))
	}

	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}

			go s.serve(c, bodies)
		}
	}()

	return s
}

func (s *lateEndH2) serve(c net.Conn, bodies map[string][]byte) {
	defer func() {
		_ = c.Close()
	}()

	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(c, preface); err != nil {
		return
	}

	var wmu sync.Mutex

	fr := http2.NewFramer(c, c)

	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)

	if err := fr.WriteSettings(); err != nil {
		return
	}

	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return
		}

		switch f := f.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				wmu.Lock()

				err := fr.WriteSettingsAck()
				wmu.Unlock()

				if err != nil {
					return
				}
			}

		case *http2.RSTStreamFrame:
			s.mu.Lock()

			s.resets[f.StreamID] = f.ErrCode
			s.mu.Unlock()

		case *http2.MetaHeadersFrame:
			path := f.PseudoValue("path")
			body := bodies[path]

			var block bytes.Buffer

			enc := hpack.NewEncoder(&block)

			_ = enc.WriteField(hpack.HeaderField{
				Name:  ":status",
				Value: "200",
			})

			if body != nil {
				_ = enc.WriteField(hpack.HeaderField{
					Name:  "content-encoding",
					Value: strings.TrimPrefix(path, "/"),
				})
			}

			wmu.Lock()

			err := fr.WriteHeaders(http2.HeadersFrameParam{
				StreamID:      f.StreamID,
				BlockFragment: block.Bytes(),
				EndStream:     body == nil,
				EndHeaders:    true,
			})
			if err == nil && body != nil {
				err = fr.WriteData(f.StreamID, false, body)
			}

			wmu.Unlock()

			if err != nil {
				return
			}

			if body != nil {
				id := f.StreamID
				s.ends <- func() {
					wmu.Lock()

					_ = fr.WriteData(id, true, nil)

					wmu.Unlock()
				}
			}
		}
	}
}
