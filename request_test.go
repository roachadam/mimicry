package mimicry

import (
	"errors"
	"io"
	"net/http"
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

func newRequest(t *testing.T, url string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	return req
}

// Without From, a request of another kind comes from the transport's last
// navigation.
func TestKindsComeFromLastNavigation(t *testing.T) {
	s, seen := headersSeen(t)
	for _, req := range []*http.Request{
		newRequest(t, s.url+"/page?q=1"),
		As(newRequest(t, s.url+"/api"), Fetch),
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

// Requests no browser makes, or none measured, fail before anything is
// sent (each case names its reason); a body without GetBody fails at a
// redirect that keeps it, rather than becoming http.Client's GET.
func TestRefusedRequests(t *testing.T) {
	page, err := url.Parse("https://www.a.test/")
	if err != nil {
		t.Fatal(err)
	}

	css := page.JoinPath("style.css")
	noFormPost := func() *profile.Profile {
		p := profiles.Chrome()

		p.Variants = slices.DeleteFunc(p.Variants, func(v profile.Variant) bool {
			return v.Method == http.MethodPost && !v.Insecure
		})

		return p
	}
	// scriptFormPost: form POSTs measured only as a script submits them,
	// without Sec-Fetch-User.
	scriptFormPost := func() *profile.Profile {
		p := profiles.Chrome()
		for i, v := range p.Variants {
			if v.Method == http.MethodPost {
				p.Variants[i].Headers = slices.DeleteFunc(slices.Clone(v.Headers), func(h profile.Header) bool {
					return h.Name == "sec-fetch-user"
				})
			}
		}

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
		{name: "preflight", method: http.MethodOptions, r: &Request{Kind: Preflight}},
		{name: "navigation POST without From", method: http.MethodPost, url: "https://a.test/submit", body: strings.NewReader("a=1")},
		{name: "form POST without its variant", profile: noFormPost, method: http.MethodPost, url: "https://www.a.test/submit", body: strings.NewReader("a=1"), r: &Request{}, notMeasured: true},
		{name: "clicked form POST measured only by script", profile: scriptFormPost, method: http.MethodPost, url: "https://www.a.test/submit", body: strings.NewReader("a=1"), r: &Request{}, notMeasured: true},
		{name: "unmeasured fetchpriority", method: http.MethodGet, r: &Request{Kind: Script, Priority: PriorityHigh}, notMeasured: true},
		{name: "redirected body without GetBody", method: http.MethodPut, url: "https://www.a.test/r", body: io.NopCloser(strings.NewReader("x")), r: &Request{Kind: Fetch}, follow: true, sent: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := profiles.Chrome()
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

// A variant measured over HTTP/2 only has no HTTP/1.1 list, and one whose
// HTTP/1.1 list was measured on a script's navigations has no slot for a
// user's Sec-Fetch-User: on an HTTP/1.1 connection the request is not
// measured, never sent in a guessed order.
func TestHTTP2OnlyVariantOverHTTP1(t *testing.T) {
	var served atomic.Int32

	s := server(t, false, func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})

	for _, tc := range []struct {
		name string
		p    *profile.Profile
		page string
	}{
		// A cross-site form POST: every profile measured it over HTTP/2
		// only.
		{name: "cross-site form POST", p: profiles.Chrome(), page: "https://www.a.test/form"},
		// Firefox's same-origin form POST over HTTP/1.1 was a script's
		// (kinds-http1); its user's was measured over HTTP/2 only.
		{name: "Firefox's clicked form POST", p: profiles.Firefox(), page: s.url + "/form"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := url.Parse(tc.page)
			if err != nil {
				t.Fatal(err)
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url+"/submit", strings.NewReader("a=1"))
			if err != nil {
				t.Fatal(err)
			}

			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			srvTr, ok := s.srv.Client().Transport.(*http.Transport)
			if !ok {
				t.Fatalf("httptest client Transport is %T", s.srv.Client().Transport)
			}

			tr, err := NewTransport(Config{
				Profile: tc.p,
				RootCAs: srvTr.TLSClientConfig.RootCAs,
			})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(tr.CloseIdleConnections)

			if err := send(t, tr, With(req, Request{From: page})); !errors.Is(err, ErrNotMeasured) {
				t.Errorf("got %v, want ErrNotMeasured", err)
			}
		})
	}

	if n := served.Load(); n != 0 {
		t.Errorf("server saw %d requests, want none", n)
	}
}

// TestAsPreflight checks a request made as a Preflight is refused: only
// the transport sends preflights.
func TestAsPreflight(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, "https://api.a.test/", nil)
	if err != nil {
		t.Fatal(err)
	}

	tr := fakeTransport(t, Config{}, &fakeEngine{})
	if err := send(t, tr, As(req, Preflight)); err == nil || !strings.Contains(err.Error(), "as a Preflight") {
		t.Errorf("got %v, want the refused Preflight", err)
	}
}
