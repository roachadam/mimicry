package mimicry

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// headersSeen serves h2 and records each request's headers.
func headersSeen(t *testing.T) (testServer, *[]http.Header) {
	t.Helper()
	var seen []http.Header
	s := server(t, true, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Clone())
		w.WriteHeader(http.StatusNoContent)
	})
	return s, &seen
}

func send(t *testing.T, rt http.RoundTripper, req *http.Request) error {
	t.Helper()
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func newRequest(t *testing.T, url string, h http.Header) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range h {
		req.Header[k] = v
	}
	return req
}

// Config.Headers add the caller's own headers to every request: they
// override the profile's defaults (an empty value suppresses one) and yield
// to the request's.
func TestConfigHeaders(t *testing.T) {
	s, seen := headersSeen(t)
	tr, err := NewTransport(Config{
		Profile: profiles.Chrome155MacOS(),
		RootCAs: s.pool,
		Headers: http.Header{
			"X-Api-Key":       {"config"},
			"Accept-Language": nil,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	for _, h := range []http.Header{nil, {"X-Api-Key": {"request"}}} {
		if err := send(t, tr, newRequest(t, s.url+"/", h)); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range []string{"config", "request"} {
		got := (*seen)[i]
		if got.Get("X-Api-Key") != want {
			t.Errorf("request %d: X-Api-Key %q, want %q", i+1, got.Get("X-Api-Key"), want)
		}
		if v, ok := got["Accept-Language"]; ok {
			t.Errorf("request %d: Accept-Language %q, want none", i+1, v)
		}
	}
}

// Exact sends the request's own headers and nothing of the profile's.
func TestExact(t *testing.T) {
	s, seen := headersSeen(t)
	if err := send(t, s.tr, Exact(newRequest(t, s.url+"/", http.Header{"X-Own": {"1"}}))); err != nil {
		t.Fatal(err)
	}
	if names := slices.Collect(maps.Keys((*seen)[0])); !slices.Equal(names, []string{"X-Own"}) {
		t.Errorf("sent %v, want only X-Own", (*seen)[0])
	}
}

// A request carries cookies only when its credentials mode lets it: a
// cross-origin fetch() by default does not, one with credentials "include"
// does, and an image (no-cors) always does.
func TestCredentialsDecideCookies(t *testing.T) {
	s, seen := headersSeen(t)
	other, err := url.Parse("https://other.example/")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		req  Request
		sent bool
	}{
		{name: "cross-origin fetch", req: Request{Kind: Fetch, From: other}, sent: false},
		{name: "cross-origin fetch, include", req: Request{Kind: Fetch, From: other, Credentials: CredentialsInclude}, sent: true},
		{name: "same-origin fetch, omit", req: Request{Kind: Fetch, Credentials: CredentialsOmit}, sent: false},
		{name: "cross-origin image", req: Request{Kind: InjectedImage, From: other}, sent: true},
		{name: "cross-origin image, crossorigin", req: Request{Kind: InjectedImage, From: other, CrossOrigin: CrossOriginAnonymous}, sent: false},
	}
	for _, c := range cases {
		req := With(newRequest(t, s.url+"/", http.Header{"Cookie": {"a=1"}}), c.req)
		if err := send(t, s.tr, req); err != nil {
			t.Fatal(err)
		}
	}
	for i, c := range cases {
		if got := (*seen)[i].Get("Cookie") != ""; got != c.sent {
			t.Errorf("%s: cookie sent %v, want %v", c.name, got, c.sent)
		}
	}
}

// A fetchpriority the profile never measured for a kind is an error, not a
// guess.
func TestUnmeasuredPriority(t *testing.T) {
	s, _ := headersSeen(t)
	err := send(t, s.tr, With(newRequest(t, s.url+"/", nil), Request{Kind: Script, Priority: PriorityHigh}))
	if !errors.Is(err, ErrNotMeasured) {
		t.Fatalf("got %v, want a not-measured error", err)
	}
}

// Without From, a request of another kind comes from the transport's last
// navigation.
func TestKindsComeFromLastNavigation(t *testing.T) {
	s, seen := headersSeen(t)
	for _, req := range []*http.Request{
		newRequest(t, s.url+"/page?q=1", nil),
		As(newRequest(t, s.url+"/api", nil), Fetch),
	} {
		if err := send(t, s.tr, req); err != nil {
			t.Fatal(err)
		}
	}
	got := (*seen)[1]
	if got.Get("Referer") != s.url+"/page?q=1" || got.Get("Sec-Fetch-Site") != "same-origin" || got.Get("Sec-Fetch-Dest") != "empty" {
		t.Errorf("fetch after navigating to /page?q=1: Referer %q, Sec-Fetch-Site %q, Sec-Fetch-Dest %q", got.Get("Referer"), got.Get("Sec-Fetch-Site"), got.Get("Sec-Fetch-Dest"))
	}
}

// A request to a non-secure origin (plain http:// to anything but
// loopback) takes the browser's non-secure lists: no Sec-Fetch-* and a
// shorter Accept-Encoding. Loopback hosts are potentially trustworthy, so
// plain http:// to them stays a secure-origin request.
func TestNonSecureOriginsAreNotLoopback(t *testing.T) {
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tr, err := NewTransport(Config{
		Profile: profiles.Chrome155MacOS(),
		// Every host name below is this server.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	for _, tc := range []struct {
		host   string
		secure bool
	}{
		{"localhost", true},
		{"app.localhost", true},
		{"127.0.0.2", true},
		{"[::1]", true},
		{"example.test", false},
		{"192.168.1.10", false},
	} {
		if err := send(t, tr, newRequest(t, "http://"+tc.host+":"+port+"/", nil)); err != nil {
			t.Fatal(err)
		}
		gotSecure := seen.Get("Sec-Fetch-Mode") != "" && strings.Contains(seen.Get("Accept-Encoding"), "br")
		if gotSecure != tc.secure || (!tc.secure && seen.Get("Sec-Fetch-Dest") != "") {
			t.Errorf("http://%s: Sec-Fetch-Mode %q, Accept-Encoding %q; want a secure-origin request %v", tc.host, seen.Get("Sec-Fetch-Mode"), seen.Get("Accept-Encoding"), tc.secure)
		}
	}
}

// Requests no browser makes, or none measured, are errors before anything
// is sent: a no-cors fetch() with a method other than GET, HEAD or POST, a
// beacon that is not a POST, a stylesheet loading anything but fonts and
// images, the transport's own preflight kind, a navigation POST without
// the page whose form sends it, and an Origin-sending request whose
// variant is not measured (a fallback list would drop Origin). A body that
// cannot be read again (no GetBody) cannot follow a redirect that keeps
// it: an error after the first request rather than the GET http.Client
// would send.
func TestRefusedRequests(t *testing.T) {
	page, err := url.Parse("https://www.a.test/")
	if err != nil {
		t.Fatal(err)
	}
	css := page.JoinPath("style.css")
	noFormPost := func() *profile.Profile {
		p := profiles.Chrome155MacOS()
		p.Variants = slices.DeleteFunc(p.Variants, func(v profile.Variant) bool {
			return v.Method == http.MethodPost && !v.Insecure
		})
		return p
	}
	for _, tc := range []struct {
		name    string
		profile func() *profile.Profile
		method  string
		url     string
		body    io.Reader
		// r is the request's Request; nil sends it without one.
		r           *Request
		follow      bool
		sent        int
		notMeasured bool
	}{
		{name: "no-cors PUT", method: http.MethodPut, r: &Request{Kind: Fetch, NoCORS: true}},
		{name: "beacon GET", method: http.MethodGet, r: &Request{Kind: Beacon}},
		{name: "stylesheet script", method: http.MethodGet, r: &Request{Kind: Script, Stylesheet: css}},
		{name: "stylesheet navigation", method: http.MethodGet, r: &Request{Kind: Navigate, Stylesheet: css}},
		{name: "preflight", method: http.MethodOptions, r: &Request{Kind: kindPreflight}},
		{name: "navigation POST without From", method: http.MethodPost, url: "https://a.test/submit", body: strings.NewReader("a=1")},
		{name: "form POST without its variant", profile: noFormPost, method: http.MethodPost, url: "https://www.a.test/submit", body: strings.NewReader("a=1"), r: &Request{}, notMeasured: true},
		{name: "redirected body without GetBody", method: http.MethodPut, url: "https://www.a.test/r", body: io.NopCloser(strings.NewReader("x")), r: &Request{Kind: Fetch}, follow: true, sent: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := profiles.Chrome155MacOS()
			if tc.profile != nil {
				p = tc.profile()
			}
			e := &fakeEngine{answer: func(req *http.Request) *http.Response {
				if req.URL.Path != "/r" {
					return nil
				}
				return &http.Response{
					StatusCode: http.StatusMovedPermanently,
					Header:     http.Header{"Location": {"/r.next"}},
					Body:       http.NoBody,
					Request:    req,
				}
			}}
			target := tc.url
			if target == "" {
				target = "https://www.a.test/x"
			}
			req, err := http.NewRequestWithContext(t.Context(), tc.method, target, tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if tc.body != nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if tc.r != nil {
				tc.r.From = page
				req = With(req, *tc.r)
			}
			if tc.follow {
				client, err := NewClient(Config{Profile: p, engine: e})
				if err != nil {
					t.Fatal(err)
				}
				resp, err := client.Do(req)
				if err == nil {
					_ = resp.Body.Close()
				}
				if err == nil || !strings.Contains(err.Error(), "GetBody") {
					t.Errorf("got %v, want the missing GetBody", err)
				}
			} else {
				err = send(t, fakeTransport(t, Config{Profile: p}, e), req)
				if err == nil {
					t.Error("sent, want an error")
				}
				if tc.notMeasured && !errors.Is(err, ErrNotMeasured) {
					t.Errorf("got %v, want ErrNotMeasured", err)
				}
			}
			if len(e.sent) != tc.sent {
				t.Errorf("%d sent, want %d", len(e.sent), tc.sent)
			}
		})
	}
}

// A variant measured over HTTP/2 only has no HTTP/1.1 list: on an
// HTTP/1.1 connection the request is not measured, never sent in a guessed
// order.
func TestHTTP2OnlyVariantOverHTTP1(t *testing.T) {
	var served atomic.Int32
	s := server(t, false, func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	page, err := url.Parse("https://www.a.test/form")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url+"/submit", strings.NewReader("a=1"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// A cross-site form POST: every profile measured it over HTTP/2 only.
	if err := send(t, s.tr, With(req, Request{From: page})); !errors.Is(err, ErrNotMeasured) {
		t.Errorf("cross-site form POST over HTTP/1.1: got %v, want ErrNotMeasured", err)
	}
	if n := served.Load(); n != 0 {
		t.Errorf("server saw %d requests, want none", n)
	}
}
