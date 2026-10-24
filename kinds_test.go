package mimicry

import (
	"context"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/roachadam/mimicry/internal/headerorder"
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

// A request carrying a header its page set takes the variant measured with
// one, and one without takes the variant measured without: the two can
// order the same headers differently.
func TestPageSetVariants(t *testing.T) {
	p := profiles.Chrome155MacOS()
	fetch := p.Kinds[profile.KindFetch]
	plain := slices.Clone(fetch.Headers)
	slices.Reverse(plain)
	if !slices.ContainsFunc(plain, func(h profile.Header) bool {
		return h.Name == "origin"
	}) {
		plain = append(plain, profile.Header{
			Name: "origin",
		})
	}
	withPage := append(slices.Clone(plain), profile.Header{
		Name: "x-a",
	})
	// The test's own variants replace any measured ones.
	fetch.Variants = slices.DeleteFunc(fetch.Variants, func(v profile.Variant) bool {
		return v.Method == http.MethodPut && v.Site == profile.SiteCrossSite && !v.Redirected && !v.Insecure
	})
	for _, v := range []struct {
		pageSet bool
		list    []profile.Header
	}{
		{pageSet: false, list: plain},
		{pageSet: true, list: withPage},
	} {
		fetch.Variants = append(fetch.Variants, profile.Variant{
			Method:      http.MethodPut,
			Site:        profile.SiteCrossSite,
			CORSChecked: true,
			PageSet:     v.pageSet,
			Headers:     v.list,
			HTTP1:       fetch.HTTP1,
		})
	}
	page, err := url.Parse("https://www.a.test/")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		h    http.Header
		want []profile.Header
	}{
		{h: nil, want: plain},
		// A suppressed default is not a header the page set.
		{h: http.Header{"Sec-Fetch-User": nil}, want: plain},
		{h: http.Header{"X-A": {"1"}}, want: withPage},
	} {
		e := &fakeEngine{
			answer: func(req *http.Request) *http.Response {
				if req.Method != http.MethodOptions {
					return nil
				}
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Header:     allowAll(),
					Body:       http.NoBody,
					Request:    req,
				}
			},
		}
		tr := fakeTransport(t, Config{
			Profile: p,
		}, e)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "https://api.b.test/x", nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range tc.h {
			req.Header[k] = v
		}
		if err := send(t, tr, With(req, Request{Kind: Fetch, From: page})); err != nil {
			t.Fatal(err)
		}
		plan := headerorder.PlanFrom(e.sent[len(e.sent)-1].Context())
		if plan == nil || !slices.EqualFunc(plan.Headers, tc.want, func(a, b profile.Header) bool {
			return a.Name == b.Name
		}) {
			t.Errorf("headers %v: plan %+v, want the variant measured that way", tc.h, plan)
		}
	}
}

// With a header table (Chrome's), a fetch() or XHR carrying headers its
// page set takes the list of its condition without them and the table:
// the plan has no slot, and fetch() sends the page's names lowercase. A
// redirect's such request takes the table only where the captures show it
// ordering one (HeaderTable.Redirected, Chrome's); otherwise it is not
// measured unless a variant was captured for it.
func TestHeaderTablePlans(t *testing.T) {
	p := profiles.Chrome155MacOS()
	if p.Context.HeaderTable == nil {
		t.Fatal("chrome-155-macos has no header table")
	}
	page, err := url.Parse("https://www.a.test/")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind  Kind
		lower bool
	}{
		{
			kind:  Fetch,
			lower: true,
		},
		{
			kind: XHR,
		},
	} {
		e := &fakeEngine{}
		tr := fakeTransport(t, Config{
			Profile: p,
		}, e)
		req := newRequest(t, "https://www.a.test/x", http.Header{"X-A": {"1"}})
		if err := send(t, tr, With(req, Request{Kind: tc.kind, From: page})); err != nil {
			t.Fatal(err)
		}
		plan := headerorder.PlanFrom(e.sent[len(e.sent)-1].Context())
		if plan == nil || plan.Table == nil || !slices.Equal(plan.Table.Own, p.Context.HeaderTable.Own) || plan.LowerOwn != tc.lower || slices.ContainsFunc(plan.Headers, func(h profile.Header) bool {
			return h.Name == profile.OtherHeaders
		}) {
			t.Errorf("%s: plan %+v, want the table, lowercase %v, no slot", tc.kind, plan, tc.lower)
		}
	}

	for _, redirected := range []bool{true, false} {
		p := profiles.Chrome155MacOS()
		if !p.Context.HeaderTable.Redirected {
			t.Fatal("chrome-155-macos's header table does not order redirects")
		}
		p.Context.HeaderTable.Redirected = redirected
		e := &fakeEngine{}
		tr := fakeTransport(t, Config{
			Profile: p,
		}, e)
		first := newRequest(t, "https://www.a.test/old", nil)
		hop := newRequest(t, "https://www.a.test/new", http.Header{"X-A": {"1"}})
		hop.Response = &http.Response{
			Request: first,
		}
		err := send(t, tr, With(hop, Request{Kind: Fetch, From: page}))
		switch {
		case !redirected && (!errors.Is(err, ErrNotMeasured) || len(e.sent) > 0):
			t.Errorf("redirected with a page header, no table for redirects: %v, %d sent; want ErrNotMeasured", err, len(e.sent))
		case redirected && err != nil:
			t.Errorf("redirected with a page header: %v", err)
		case redirected && headerorder.PlanFrom(e.sent[0].Context()).Table == nil:
			t.Errorf("redirected with a page header: plan %+v, want the table", headerorder.PlanFrom(e.sent[0].Context()))
		}
	}
}

// A header the page set twice, in any spelling, goes out as one, its
// values joined by ", " under the first spelling, as fetch() and XHR send
// it; one value is left alone.
func TestPageHeadersCombine(t *testing.T) {
	h := http.Header{
		"X-Dup":   {"1"},
		"x-dup":   {"2"},
		"X-Twice": {"a", "b"},
		"X-One":   {"1"},
		"Accept":  {"x", "y"},
	}
	combinePageHeaders(h, profiles.Chrome155MacOS().Kinds[profile.KindFetch].Headers)
	want := http.Header{
		"X-Dup":   {"1, 2"},
		"X-Twice": {"a, b"},
		"X-One":   {"1"},
		"Accept":  {"x", "y"},
	}
	if !maps.EqualFunc(h, want, slices.Equal) {
		t.Errorf("got %v, want %v", h, want)
	}
}

// Requests no browser can make are errors, and nothing is sent: a no-cors
// fetch() with a method other than GET, HEAD or POST, a beacon that is not
// a POST, a stylesheet loading anything but fonts and images, and the
// transport's own preflight kind.
func TestImpossibleRequests(t *testing.T) {
	page, err := url.Parse("https://www.a.test/")
	if err != nil {
		t.Fatal(err)
	}
	css := page.JoinPath("style.css")
	for _, tc := range []struct {
		name     string
		method   string
		r        Request
		possible bool
	}{
		{name: "no-cors PUT", method: http.MethodPut, r: Request{Kind: Fetch, NoCORS: true}},
		{name: "no-cors POST", method: http.MethodPost, r: Request{Kind: Fetch, NoCORS: true}, possible: true},
		{name: "beacon GET", method: http.MethodGet, r: Request{Kind: Beacon}},
		{name: "beacon POST", method: http.MethodPost, r: Request{Kind: Beacon}, possible: true},
		{name: "stylesheet script", method: http.MethodGet, r: Request{Kind: Script, Stylesheet: css}},
		{name: "stylesheet navigation", method: http.MethodGet, r: Request{Kind: Navigate, Stylesheet: css}},
		{name: "stylesheet font", method: http.MethodGet, r: Request{Kind: Font, Stylesheet: css}, possible: true},
		{name: "stylesheet image", method: http.MethodGet, r: Request{Kind: Image, Stylesheet: css}, possible: true},
		{name: "preflight", method: http.MethodOptions, r: Request{Kind: kindPreflight}},
	} {
		e := &fakeEngine{}
		tr := fakeTransport(t, Config{}, e)
		req, err := http.NewRequestWithContext(t.Context(), tc.method, "https://www.a.test/x", nil)
		if err != nil {
			t.Fatal(err)
		}
		tc.r.From = page
		err = send(t, tr, With(req, tc.r))
		if (err == nil) != tc.possible || (len(e.sent) > 0) != tc.possible {
			t.Errorf("%s: err %v, %d sent; want possible %v", tc.name, err, len(e.sent), tc.possible)
		}
	}
}

// A typed navigation that matches a navigation variant takes it: its site
// relation is none.
func TestTypedNavigationTakesItsVariant(t *testing.T) {
	p := profiles.Chrome155MacOS()
	list := slices.Clone(p.Headers)
	slices.Reverse(list)
	p.Variants = append(p.Variants, profile.Variant{
		Site:        profile.SiteNone,
		CORSChecked: true,
		PageSet:     true,
		Headers:     list,
		HTTP1:       p.HTTP1.Headers,
	})
	e := &fakeEngine{}
	tr := fakeTransport(t, Config{
		Profile: p,
	}, e)
	// A header the navigation list does not name makes it CORS-checked.
	if err := send(t, tr, newRequest(t, "https://a.test/", http.Header{"X-A": {"1"}})); err != nil {
		t.Fatal(err)
	}
	plan := headerorder.PlanFrom(e.sent[0].Context())
	if plan == nil || !slices.EqualFunc(plan.Headers, list, func(a, b profile.Header) bool {
		return a.Name == b.Name
	}) {
		t.Errorf("plan %+v, want the variant's list", plan)
	}
}

// A request that sends Origin but matches no variant, so that only a
// fallback list without an Origin slot is left, is not measured: it would
// drop Origin. An exact variant is the browser's.
func TestOriginNeedsAMeasuredList(t *testing.T) {
	page, err := url.Parse("https://a.test/form")
	if err != nil {
		t.Fatal(err)
	}
	post := func(p *profile.Profile) error {
		tr := fakeTransport(t, Config{
			Profile: p,
		}, &fakeEngine{})
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://a.test/submit", strings.NewReader("a=1"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return send(t, tr, With(req, Request{From: page}))
	}
	if err := post(profiles.Chrome155MacOS()); err != nil {
		t.Errorf("same-origin form POST, measured: %v", err)
	}
	p := profiles.Chrome155MacOS()
	p.Variants = slices.DeleteFunc(p.Variants, func(v profile.Variant) bool {
		return v.Method == http.MethodPost && !v.Insecure
	})
	if err := post(p); !errors.Is(err, ErrNotMeasured) {
		t.Errorf("form POST without its variant: got %v, want ErrNotMeasured", err)
	}
}

// Only a page's form makes a navigation that is not a GET: without From
// it is refused before anything is sent, while a typed GET goes.
func TestNavigationWithoutPageIsAGet(t *testing.T) {
	e := &fakeEngine{}
	tr := fakeTransport(t, Config{
		Profile: profiles.Chrome155MacOS(),
	}, e)
	post, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://a.test/submit", strings.NewReader("a=1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := send(t, tr, post); err == nil {
		t.Error("POST navigation without From: sent, want an error")
	}
	if err := send(t, tr, newRequest(t, "https://a.test/", nil)); err != nil {
		t.Errorf("typed GET navigation: %v", err)
	}
	if len(e.sent) != 1 {
		t.Errorf("engine got %d requests, want only the GET", len(e.sent))
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
