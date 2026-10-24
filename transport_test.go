package mimicry

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/headerorder"
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
		pool:  pool,
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
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			errs <- fetchOK(t.Context(), client, ts.url)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if n := ts.conns.Load(); n != 1 {
		t.Fatalf("%d connections, want 1", n)
	}
}

// fetchOK GETs url and checks the body is "ok", for goroutines, which must
// not call t.Fatal.
func fetchOK(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if string(b) != "ok" {
		return fmt.Errorf("body %q", b)
	}
	return nil
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
		cfg.Profile = profiles.Chrome155MacOS()
	}
	cfg.engine = e
	tr, err := NewTransport(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// valuesOf returns h's values for name in any case.
func valuesOf(h http.Header, name string) []string {
	var out []string
	for k, v := range h {
		if strings.EqualFold(k, name) {
			out = append(out, v...)
		}
	}
	return out
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
// works.
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
		t.Run(tc.name, func(t *testing.T) {
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
			resp, err := tr.RoundTrip(newRequest(t, "https://a.test/", nil))
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
}

// Responses without a body (204, 304, Content-Length 0) are left as they
// are, whatever their Content-Encoding: nothing to decode.
func TestEmptyEncodedBodies(t *testing.T) {
	for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
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
			resp, err := tr.RoundTrip(newRequest(t, "https://a.test/", nil))
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(resp.Body)
			if err != nil || len(b) != 0 {
				t.Errorf("%s, status %d: read %q, %v", enc, tc.status, b, err)
			}
			if err := resp.Body.Close(); err != nil {
				t.Errorf("%s, status %d: close: %v", enc, tc.status, err)
			}
			if resp.ContentLength != tc.length || (tc.length == 0 && resp.Header.Get("Content-Length") != "0") {
				t.Errorf("%s, status %d: length %d (%q), want %d", enc, tc.status, resp.ContentLength, resp.Header.Get("Content-Length"), tc.length)
			}
		}
	}
}

// An empty redirect with a Content-Encoding is followed: http.Client reads
// and closes its body.
func TestEmptyEncodedRedirect(t *testing.T) {
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
		Profile: profiles.Chrome155MacOS(),
		engine:  e,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(newRequest(t, "https://a.test/r", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Request.URL.Path != "/done" {
		t.Errorf("ended at %s, want /done", resp.Request.URL)
	}
}

// Accept-Encoding set in Config.Headers is the caller's choice: the body
// is not decoded.
func TestConfigAcceptEncodingKeepsBody(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = io.WriteString(zw, "text")
	_ = zw.Close()
	tr := fakeTransport(t, Config{
		Headers: http.Header{
			"accept-encoding": {"gzip"},
		},
	}, &fakeEngine{
		answer: func(req *http.Request) *http.Response {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Encoding": {"gzip"}},
				Body:          io.NopCloser(bytes.NewReader(buf.Bytes())),
				ContentLength: int64(buf.Len()),
				Request:       req,
			}
		},
	})
	resp, err := tr.RoundTrip(newRequest(t, "https://a.test/", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Equal(b, buf.Bytes()) || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("body %q, Content-Encoding %q: decoded", b, resp.Header.Get("Content-Encoding"))
	}
}

// NewTransport validates the profile, whatever the engine, and keeps a
// copy: changing the caller's profile afterwards changes nothing.
func TestTransportOwnsProfileCopy(t *testing.T) {
	bad := profiles.Chrome155MacOS()
	bad.Browser = ""
	if _, err := NewTransport(Config{
		Profile: bad,
		engine:  &fakeEngine{},
	}); err == nil {
		t.Error("NewTransport took an invalid profile")
	}
	p := profiles.Chrome155MacOS()
	e := &fakeEngine{}
	tr := fakeTransport(t, Config{
		Profile: p,
	}, e)
	delete(p.Kinds, profile.KindFetch)
	if err := send(t, tr, As(newRequest(t, "https://a.test/api", nil), Fetch)); err != nil {
		t.Errorf("fetch after the caller's profile lost its fetch kind: %v", err)
	}
}

// trackedBody records whether it was closed.
type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

// RoundTrip closes the request's body when it fails before sending it, as
// the http.RoundTripper contract asks: a request the profile cannot
// describe, or one its CORS preflight refuses.
func TestBodyClosedOnError(t *testing.T) {
	page, err := url.Parse(corsPage)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		r    Request
	}{
		{name: "not measured", r: Request{Kind: Script, Priority: PriorityHigh, From: page}},
		{name: "CORS refused", r: Request{Kind: Fetch, From: page}},
	} {
		tr := corsTransport(t, &corsEngine{
			status: http.StatusForbidden,
			answer: allowAll(),
		}, nil)
		body := &trackedBody{
			Reader: strings.NewReader("x"),
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "https://api.b.test/x", body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tr.RoundTrip(With(req, tc.r)); err == nil {
			t.Fatalf("%s: no error", tc.name)
		}
		if !body.closed {
			t.Errorf("%s: body not closed", tc.name)
		}
	}
}

// Config.Headers are the page's in every respect: the same header picks
// the same order from Config.Headers as from the request, follows the
// request's credentials (Cookie), matches the request's in any case, and
// yields to it.
func TestConfigHeadersAreThePagesHeaders(t *testing.T) {
	page, err := url.Parse(corsPage)
	if err != nil {
		t.Fatal(err)
	}
	order := func(config, own http.Header) []string {
		e := &fakeEngine{}
		tr := fakeTransport(t, Config{
			Profile: profiles.Safari27MacOS(),
			Headers: config,
		}, e)
		if err := send(t, tr, With(newRequest(t, "https://www.a.test/api", own), Request{Kind: Fetch, From: page})); err != nil {
			t.Fatal(err)
		}
		list := headerorder.PlanFrom(e.sent[0].Context()).Headers
		names := make([]string, 0, len(list))
		for _, h := range list {
			names = append(names, h.Name)
		}
		return names
	}
	if a, b := order(http.Header{"X-Api-Key": {"k"}}, nil), order(nil, http.Header{"X-Api-Key": {"k"}}); !slices.Equal(a, b) {
		t.Errorf("order with X-Api-Key in Config.Headers %v, on the request %v", a, b)
	}

	for _, tc := range []struct {
		name   string
		r      Request
		cookie bool
	}{
		{name: "credentials omit", r: Request{Kind: Fetch, From: page, Credentials: CredentialsOmit}},
		{name: "credentials include", r: Request{Kind: Fetch, From: page, Credentials: CredentialsInclude}, cookie: true},
	} {
		e := &fakeEngine{}
		tr := fakeTransport(t, Config{
			Headers: http.Header{"Cookie": {"sid=1"}},
		}, e)
		if err := send(t, tr, With(newRequest(t, "https://api.b.test/x", nil), tc.r)); err != nil {
			t.Fatal(err)
		}
		if got := len(valuesOf(e.sent[0].Header, "Cookie")) > 0; got != tc.cookie {
			t.Errorf("%s: Config.Headers Cookie sent %v, want %v", tc.name, got, tc.cookie)
		}
	}

	for _, tc := range []struct {
		config, own string
	}{
		{config: "x-api-key", own: "X-Api-Key"},
		{config: "X-Api-Key", own: "x-api-key"},
	} {
		e := &fakeEngine{}
		tr := fakeTransport(t, Config{
			Headers: http.Header{tc.config: {"config"}},
		}, e)
		if err := send(t, tr, newRequest(t, "https://a.test/", http.Header{tc.own: {"request"}})); err != nil {
			t.Fatal(err)
		}
		if got := valuesOf(e.sent[0].Header, "X-Api-Key"); !slices.Equal(got, []string{"request"}) {
			t.Errorf("Config %s, request %s: sent %q, want only the request's", tc.config, tc.own, got)
		}
	}
}

// Authorization and Cookie from Config.Headers stay behind once a
// redirect chain leaves an origin, even when it comes back; same-origin
// redirects keep them.
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
		Profile: profiles.Chrome155MacOS(),
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
		resp, err := client.Do(newRequest(t, start, nil))
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

// A request's Cookie header replaces the jar's cookies in any case; set
// with no values, it sends none.
func TestCookieHeaderReplacesJar(t *testing.T) {
	p := profiles.Chrome155MacOS()
	jar, err := cookies.New(cookies.Config{
		Rules: p.Cookies,
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse("https://a.test/")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(cookies.Request{
		URL:        u,
		Method:     http.MethodGet,
		Navigation: true,
	}, []string{"sid=1"})
	for _, tc := range []struct {
		h    http.Header
		want []string
	}{
		{h: nil, want: []string{"sid=1"}},
		{h: http.Header{"Cookie": nil}},
		{h: http.Header{"Cookie": {}}},
		{h: http.Header{"cookie": nil}},
		{h: http.Header{"cookie": {"own=1"}}, want: []string{"own=1"}},
	} {
		e := &fakeEngine{}
		tr := fakeTransport(t, Config{
			Profile: p,
			Cookies: jar,
		}, e)
		if err := send(t, tr, newRequest(t, u.String(), tc.h)); err != nil {
			t.Fatal(err)
		}
		if got := valuesOf(e.sent[0].Header, "Cookie"); !slices.Equal(got, tc.want) {
			t.Errorf("request Cookie %q: sent %q, want %q", tc.h, got, tc.want)
		}
	}
}
