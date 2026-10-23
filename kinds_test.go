package mimicry

import (
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"testing"

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
		{name: "cross-origin fetch, include", req: Request{Kind: Fetch, From: other, Credentials: Include}, sent: true},
		{name: "same-origin fetch, omit", req: Request{Kind: Fetch, Credentials: Omit}, sent: false},
		{name: "cross-origin image", req: Request{Kind: InjectedImage, From: other}, sent: true},
		{name: "cross-origin image, crossorigin", req: Request{Kind: InjectedImage, From: other, CrossOrigin: Anonymous}, sent: false},
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
	err := send(t, s.tr, With(newRequest(t, s.url+"/", nil), Request{Kind: Script, Priority: High}))
	if !errors.Is(err, errNotMeasured) {
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
