package mimicry

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// sentRequest is what redirectEngine saw of a request.
type sentRequest struct {
	method, path, body, contentType string
	// origin is the Origin the plan sends; "" for none.
	origin string
}

// redirectEngine answers a path in redirects with that status, its
// Location the path's .next (or to), and header; anything else 200 (or
// preflights 204 with allowAll). It records every request, body read.
type redirectEngine struct {
	mu        sync.Mutex
	sent      []sentRequest
	redirects map[string]redirectAnswer
}

type redirectAnswer struct {
	status int
	to     string
	header http.Header
}

func (e *redirectEngine) RoundTrip(req *http.Request) (*http.Response, error) {
	s := sentRequest{
		method:      req.Method,
		path:        req.URL.Path,
		contentType: req.Header.Get("Content-Type"),
	}
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		s.body = string(b)
	}
	if plan := headerorder.PlanFrom(req.Context()); plan != nil {
		for _, h := range plan.Headers {
			if strings.EqualFold(h.Name, "origin") {
				s.origin = h.Value
			}
		}
	}
	e.mu.Lock()
	e.sent = append(e.sent, s)
	e.mu.Unlock()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       http.NoBody,
		Request:    req,
	}
	if req.Method == http.MethodOptions {
		resp.StatusCode = http.StatusNoContent
		resp.Header = allowAll()
		return resp, nil
	}
	if a, ok := e.redirects[req.URL.Path]; ok {
		resp.StatusCode = a.status
		resp.Header = a.header.Clone()
		if resp.Header == nil {
			resp.Header = http.Header{}
		}
		to := a.to
		if to == "" {
			to = req.URL.Path + ".next"
		}
		resp.Header.Set("Location", to)
	}
	return resp, nil
}

func (*redirectEngine) CloseIdleConnections() {}

func redirectClient(t *testing.T, p *profile.Profile, e *redirectEngine) *http.Client {
	t.Helper()
	client, err := NewClient(Config{
		Profile: p,
		engine:  e,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// fetchFrom sends a fetch() from corsPage through client.
func fetchFrom(t *testing.T, client *http.Client, method, target, body string) error {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, target, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	}
	page, _ := url.Parse(corsPage)
	resp, err := client.Do(With(req, Request{
		Kind: Fetch,
		From: page,
	}))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// A redirected fetch() goes on as every captured browser's did (the
// cors-edge capture's m-*.so steps): PUT and DELETE answered 301 or 302
// keep their method, body and Content-Type, which http.Client drops; POST
// answered 301 or 302, and anything answered 303, go on as a GET without
// them; 307 and 308 keep everything.
func TestRedirectMethods(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPost, http.MethodPatch} {
		for _, status := range []int{301, 302, 303, 307, 308} {
			e := &redirectEngine{
				redirects: map[string]redirectAnswer{
					"/r": {
						status: status,
					},
				},
			}
			client := redirectClient(t, profiles.Chrome155MacOS(), e)
			if err := fetchFrom(t, client, method, "https://www.a.test/r", "x"); err != nil {
				t.Fatalf("%s %d: %v", method, status, err)
			}
			want := sentRequest{
				method:      method,
				path:        "/r.next",
				body:        "x",
				contentType: "text/plain;charset=UTF-8",
				origin:      "https://www.a.test",
			}
			if status == 303 || (method == http.MethodPost && status < 303) {
				want = sentRequest{
					method: http.MethodGet,
					path:   "/r.next",
				}
			}
			if len(e.sent) != 2 || e.sent[1] != want {
				t.Errorf("%s answered %d: sent %+v, want the hop %+v", method, status, e.sent, want)
			}
		}
	}
}

// A body kept on one hop stays on the next: http.Client never sends a body
// again once it dropped it, whatever the status.
func TestRedirectKeepsBodyAlongTheChain(t *testing.T) {
	e := &redirectEngine{
		redirects: map[string]redirectAnswer{
			"/r":      {status: 301},
			"/r.next": {status: 307},
		},
	}
	client := redirectClient(t, profiles.Chrome155MacOS(), e)
	if err := fetchFrom(t, client, http.MethodPut, "https://www.a.test/r", "x"); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(e.sent))
	for _, s := range e.sent {
		got = append(got, s.method+" "+s.path+" "+s.body+" "+s.contentType)
	}
	want := []string{
		"PUT /r x text/plain;charset=UTF-8",
		"PUT /r.next x text/plain;charset=UTF-8",
		"PUT /r.next.next x text/plain;charset=UTF-8",
	}
	if !slices.Equal(got, want) {
		t.Errorf("sent %q, want %q", got, want)
	}
}

// A body that cannot be read again (no GetBody) cannot follow a redirect
// that keeps it: an error rather than the GET http.Client would send.
func TestRedirectBodyWithoutGetBody(t *testing.T) {
	e := &redirectEngine{
		redirects: map[string]redirectAnswer{
			"/r": {status: 301},
		},
	}
	client := redirectClient(t, profiles.Chrome155MacOS(), e)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "https://www.a.test/r", io.NopCloser(strings.NewReader("x")))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := url.Parse(corsPage)
	resp, err := client.Do(With(req, Request{
		Kind: Fetch,
		From: page,
	}))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("followed the redirect without the body")
	}
	if !strings.Contains(err.Error(), "GetBody") {
		t.Errorf("got %v, want the missing GetBody", err)
	}
	if len(e.sent) != 1 {
		t.Errorf("sent %+v, want only the PUT", e.sent)
	}
}

// The GET a redirect makes of a same-origin POST, PUT or DELETE carries the
// Origin the request sent in Safari (redirects.method_change_origin keep),
// none in Chrome and Firefox (drop), as their cors-edge captures show; a
// profile without the rule cannot send it. A GET redirected to a GET
// sends none either way.
func TestRedirectToGetOrigin(t *testing.T) {
	noRule := profiles.Chrome155MacOS()
	noRule.Redirects = nil
	for _, tc := range []struct {
		name   string
		p      *profile.Profile
		method string
		want   string
		err    error
	}{
		{name: "chrome POST", p: profiles.Chrome155MacOS(), method: http.MethodPost},
		{name: "firefox PUT", p: profiles.Firefox158MacOS(), method: http.MethodPut},
		{name: "safari 27 POST", p: profiles.Safari27MacOS(), method: http.MethodPost, want: "https://www.a.test"},
		{name: "safari 26 POST", p: profiles.Safari26MacOS(), method: http.MethodPost, want: "https://www.a.test"},
		{name: "safari 27 GET", p: profiles.Safari27MacOS(), method: http.MethodGet},
		{name: "no rule", p: noRule, method: http.MethodPost, err: ErrNotMeasured},
	} {
		e := &redirectEngine{
			redirects: map[string]redirectAnswer{
				"/r": {status: http.StatusSeeOther},
			},
		}
		client := redirectClient(t, tc.p, e)
		body := "x"
		if tc.method == http.MethodGet {
			body = ""
		}
		err := fetchFrom(t, client, tc.method, "https://www.a.test/r", body)
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Errorf("%s: got %v, want %v", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(e.sent) != 2 || e.sent[1].method != http.MethodGet || e.sent[1].origin != tc.want {
			t.Errorf("%s: sent %+v, want a GET hop with Origin %q", tc.name, e.sent, tc.want)
		}
	}
}

// A chain stops after 20 redirects: every captured browser requested the
// 20th redirect's URL, never the 21st. NewClient lifts http.Client's
// default limit of 10.
func TestRedirectLimit(t *testing.T) {
	e := &redirectEngine{
		redirects: map[string]redirectAnswer{},
	}
	for i := range 25 {
		e.redirects["/hop"+strconv.Itoa(i)] = redirectAnswer{
			status: http.StatusFound,
			to:     "/hop" + strconv.Itoa(i+1),
		}
	}
	client := redirectClient(t, profiles.Chrome155MacOS(), e)
	err := fetchFrom(t, client, http.MethodGet, "https://www.a.test/hop0", "")
	if err == nil || !strings.Contains(err.Error(), "stopped after 20 redirects") {
		t.Errorf("got %v, want stopped after 20 redirects", err)
	}
	if len(e.sent) != 21 || e.sent[20].path != "/hop20" {
		t.Errorf("sent %d requests, want 21, the last /hop20", len(e.sent))
	}
}

// A CORS fetch() to another origin follows a redirect only when it allows
// the page's origin, as every captured browser did (the cors-edge capture's
// noacao-redirect steps, with and without a preflight): otherwise a
// *CORSError and no next hop, unless IgnoreCORS. Same-origin fetches and
// navigations are not CORS-checked; credentials need the exact origin and
// Access-Control-Allow-Credentials.
func TestRedirectCORSCheck(t *testing.T) {
	allow := http.Header{
		"Access-Control-Allow-Origin": {"https://www.a.test"},
	}
	star := http.Header{
		"Access-Control-Allow-Origin": {"*"},
	}
	for _, tc := range []struct {
		name    string
		target  string
		header  http.Header
		headers http.Header
		r       Request
		want    []string
		refused bool
	}{
		{name: "allowed", target: "https://api.b.test/r", header: allow, want: []string{"GET /r", "GET /r.next"}},
		{name: "wildcard", target: "https://api.b.test/r", header: star, want: []string{"GET /r", "GET /r.next"}},
		{name: "no ACAO", target: "https://api.b.test/r", want: []string{"GET /r"}, refused: true},
		{name: "no ACAO, preflighted", target: "https://api.b.test/r", headers: http.Header{"X-A": {"1"}}, want: []string{"OPTIONS /r", "GET /r"}, refused: true},
		{name: "no ACAO, IgnoreCORS", target: "https://api.b.test/r", r: Request{IgnoreCORS: true}, want: []string{"GET /r", "GET /r.next"}},
		{name: "wildcard with credentials", target: "https://api.b.test/r", header: star, r: Request{Credentials: CredentialsInclude}, want: []string{"GET /r"}, refused: true},
		{name: "credentials without ACAC", target: "https://api.b.test/r", header: allow, r: Request{Credentials: CredentialsInclude}, want: []string{"GET /r"}, refused: true},
		{name: "same-origin", target: "https://www.a.test/r", want: []string{"GET /r", "GET /r.next"}},
		{name: "no-cors", target: "https://api.b.test/r", r: Request{NoCORS: true}, want: []string{"GET /r", "GET /r.next"}},
	} {
		e := &redirectEngine{
			redirects: map[string]redirectAnswer{
				"/r": {status: http.StatusFound, header: tc.header},
			},
		}
		client := redirectClient(t, profiles.Chrome155MacOS(), e)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range tc.headers {
			req.Header[k] = v
		}
		r := tc.r
		r.Kind = Fetch
		r.From, _ = url.Parse(corsPage)
		resp, err := client.Do(With(req, r))
		if err == nil {
			_ = resp.Body.Close()
		}
		var cerr *CORSError
		switch {
		case tc.refused && (!errors.As(err, &cerr) || !cerr.Redirect || cerr.Status != http.StatusFound):
			t.Errorf("%s: got %v, want a redirect's *CORSError", tc.name, err)
		case !tc.refused && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		}
		var got []string
		for _, s := range e.sent {
			got = append(got, s.method+" "+s.path)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: sent %q, want %q", tc.name, got, tc.want)
		}
	}
}
