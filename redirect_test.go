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
			client := redirectClient(t, anyRedirectOrder(profiles.Chrome155MacOS()), e)
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
		client := redirectClient(t, anyRedirectOrder(profiles.Chrome155MacOS()), e)
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

// A redirect's request takes the variant of how it was made as well:
// whether the redirect went to another origin, whether the request it
// answered carried Origin, and whether it changed the method (the
// captures' every hop: the kinds, cookies, cors, cors-edge and redirects2
// pages). A hop no capture shows, such as a no-cors image redirected
// within another site, is not measured rather than sent in another hop's
// order.
func TestRedirectHopConditions(t *testing.T) {
	page, err := url.Parse(corsPage)
	if err != nil {
		t.Fatal(err)
	}
	// hop is a request to the chain's last URL, made by following
	// redirects from the first, with method first and then GET.
	hop := func(t *testing.T, first string, urls ...string) *http.Request {
		t.Helper()
		var prev *http.Request
		for i, u := range urls {
			method := http.MethodGet
			if i == 0 {
				method = first
			}
			req, err := http.NewRequestWithContext(t.Context(), method, u, nil)
			if err != nil {
				t.Fatal(err)
			}
			if prev != nil {
				req.Response = &http.Response{
					Request: prev,
				}
			}
			prev = req
		}
		return prev
	}
	for _, name := range profiles.Names() {
		p, err := profiles.ByName(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			kind   Kind
			method string
			urls   []string
			// site, crossOrigin, before and changed are the variant's
			// condition; unmeasured: ErrNotMeasured.
			site                         string
			crossOrigin, before, changed bool
			unmeasured                   bool
		}{
			{name: "same-origin chain", kind: Fetch, method: http.MethodGet, urls: []string{"https://www.a.test/r", "https://www.a.test/r.next"}},
			{name: "POST answered 303", kind: Fetch, method: http.MethodPost, urls: []string{"https://www.a.test/r", "https://www.a.test/r.next"}, before: true, changed: true},
			{name: "to another site", kind: Fetch, method: http.MethodGet, urls: []string{"https://www.a.test/r", "https://api.b.test/r.next"}, site: profile.SiteCrossSite, crossOrigin: true},
			{name: "within another site", kind: Fetch, method: http.MethodGet, urls: []string{"https://api.b.test/r", "https://api.b.test/r.next"}, site: profile.SiteCrossSite, before: true},
			{name: "within the page's site", kind: Fetch, method: http.MethodGet, urls: []string{"https://static.a.test/r", "https://static.a.test/r.next"}, site: profile.SiteSameSite, before: true},
			{name: "back to the page's origin", kind: Fetch, method: http.MethodGet, urls: []string{"https://api.b.test/r", "https://www.a.test/r.next"}, crossOrigin: true, before: true},
			{name: "from one other site to another", kind: Fetch, method: http.MethodGet, urls: []string{"https://api.b.test/r", "https://api.c.test/r.next"}, site: profile.SiteCrossSite, crossOrigin: true, before: true},
			{name: "same-origin image", kind: Image, method: http.MethodGet, urls: []string{"https://www.a.test/i", "https://www.a.test/i.next"}},
			{name: "same-site new Image()", kind: InjectedImage, method: http.MethodGet, urls: []string{"https://static.a.test/i", "https://static.a.test/i.next"}, site: profile.SiteSameSite},
			{name: "no-cors image within another site", kind: Image, method: http.MethodGet, urls: []string{"https://img.b.test/i", "https://img.b.test/i.next"}, unmeasured: true},
		} {
			req := hop(t, tc.method, tc.urls...)
			s, err := resolve(p, req, Request{Kind: tc.kind, From: page}, page, nil)
			if tc.unmeasured {
				if !errors.Is(err, ErrNotMeasured) {
					t.Errorf("%s %s: got %v, want ErrNotMeasured", name, tc.name, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s %s: %v", name, tc.name, err)
				continue
			}
			names := func(hs []profile.Header) []string {
				out := make([]string, len(hs))
				for i, h := range hs {
					out[i] = h.Name
				}
				return out
			}
			got := names(s.plan.Headers)
			if !slices.ContainsFunc(p.Kinds[profileKinds[tc.kind]].Variants, func(v profile.Variant) bool {
				return v.Redirected && v.Method == "" && v.Site == tc.site && !v.PageSet && !v.Insecure &&
					v.CrossOriginRedirect == tc.crossOrigin && v.OriginBeforeRedirect == tc.before && v.MethodChangedByRedirect == tc.changed &&
					slices.Equal(names(v.Headers), got)
			}) {
				t.Errorf("%s %s: sent %q, not the variant for cross-origin redirect %v, Origin before %v, method changed %v", name, tc.name, got, tc.crossOrigin, tc.before, tc.changed)
			}
		}
	}
}

// navigateFrom sends a script's navigation (no Sec-Fetch-User) from
// corsPage through client.
func navigateFrom(t *testing.T, client *http.Client, target string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header["Sec-Fetch-User"] = nil
	page, _ := url.Parse(corsPage)
	resp, err := client.Do(With(req, Request{
		Kind: Navigate,
		From: page,
	}))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// A top-level navigation follows the profile's number of redirects
// (redirects.navigation_limit): Chrome requested the 19th redirect's URL
// and not the 20th, Firefox and Safari the 20th and not the 21st (the
// redirects2 capture's nav-chain.so); a fetch() follows 20 in every
// browser. Without the rule, the 20th is not measured.
func TestNavigationRedirectLimit(t *testing.T) {
	noRule := profiles.Chrome155MacOS()
	noRule.Redirects = nil
	for _, tc := range []struct {
		name  string
		p     *profile.Profile
		fetch bool
		// sent is how many requests go out; err the error's.
		sent int
		err  string
	}{
		{name: "chrome", p: profiles.Chrome155MacOS(), sent: 20, err: "stopped after 19 redirects"},
		{name: "firefox", p: profiles.Firefox158MacOS(), sent: 21, err: "stopped after 20 redirects"},
		{name: "safari 27", p: profiles.Safari27MacOS(), sent: 21, err: "stopped after 20 redirects"},
		{name: "safari 26", p: profiles.Safari26MacOS(), sent: 21, err: "stopped after 20 redirects"},
		{name: "chrome fetch", p: profiles.Chrome155MacOS(), fetch: true, sent: 21, err: "stopped after 20 redirects"},
		{name: "no rule", p: noRule, sent: 20, err: ErrNotMeasured.Error()},
	} {
		e := &redirectEngine{
			redirects: map[string]redirectAnswer{},
		}
		for i := range 25 {
			e.redirects["/hop"+strconv.Itoa(i)] = redirectAnswer{
				status: http.StatusFound,
				to:     "/hop" + strconv.Itoa(i+1),
			}
		}
		client := redirectClient(t, tc.p, e)
		var err error
		if tc.fetch {
			err = fetchFrom(t, client, http.MethodGet, "https://www.a.test/hop0", "")
		} else {
			err = navigateFrom(t, client, "https://www.a.test/hop0")
		}
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: got %v, want %s", tc.name, err, tc.err)
		}
		if len(e.sent) != tc.sent {
			t.Errorf("%s: sent %d requests, want %d", tc.name, len(e.sent), tc.sent)
		}
	}
}

// XHR, <img crossorigin> and <script crossorigin> to another origin follow
// a redirect only when it passes the CORS check, as fetch() does: every
// browser's redirects2 capture requested no next hop after a cross-site
// 302 without Access-Control-Allow-Origin, and requested it with one. A
// no-cors image is not checked.
func TestRedirectCORSCheckBeyondFetch(t *testing.T) {
	allow := http.Header{
		"Access-Control-Allow-Origin": {"https://www.a.test"},
	}
	for _, name := range profiles.Names() {
		for _, tc := range []struct {
			name    string
			r       Request
			header  http.Header
			refused bool
		}{
			{name: "xhr", r: Request{Kind: XHR}, header: allow},
			{name: "xhr without ACAO", r: Request{Kind: XHR}, refused: true},
			{name: "img crossorigin", r: Request{Kind: InjectedImage, CrossOrigin: CrossOriginAnonymous}, header: allow},
			{name: "img crossorigin without ACAO", r: Request{Kind: InjectedImage, CrossOrigin: CrossOriginAnonymous}, refused: true},
			{name: "script crossorigin", r: Request{Kind: InjectedScript, CrossOrigin: CrossOriginAnonymous}, header: allow},
			{name: "script crossorigin without ACAO", r: Request{Kind: InjectedScript, CrossOrigin: CrossOriginAnonymous}, refused: true},
		} {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			e := &redirectEngine{
				redirects: map[string]redirectAnswer{
					"/r": {status: http.StatusFound, header: tc.header},
				},
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://www.b.test/r", nil)
			if err != nil {
				t.Fatal(err)
			}
			r := tc.r
			r.From, _ = url.Parse(corsPage)
			resp, err := redirectClient(t, p, e).Do(With(req, r))
			if err == nil {
				_ = resp.Body.Close()
			}
			var cerr *CORSError
			switch {
			case tc.refused && (!errors.As(err, &cerr) || !cerr.Redirect):
				t.Errorf("%s %s: got %v, want a redirect's *CORSError", name, tc.name, err)
			case !tc.refused && err != nil:
				t.Errorf("%s %s: %v", name, tc.name, err)
			}
			want := 2
			if tc.refused {
				want = 1
			}
			if len(e.sent) != want {
				t.Errorf("%s %s: sent %d requests, want %d", name, tc.name, len(e.sent), want)
			}
		}
	}
}

// A cross-site PATCH answered 303 goes on as a GET, which Firefox and
// Safari preflight again because the PATCH was (redirects.hop_preflight
// inherit) and Chrome does not (own), as the redirects2 capture's
// m-patch-303.cs shows. Without the rule it is not measured.
func TestHopPreflight(t *testing.T) {
	noRule := profiles.Firefox158MacOS()
	noRule.Redirects = nil
	for _, tc := range []struct {
		name string
		p    *profile.Profile
		want []string
		err  error
	}{
		{name: "chrome", p: profiles.Chrome155MacOS(), want: []string{"OPTIONS /r", "PATCH /r", "GET /r.next"}},
		{name: "firefox", p: profiles.Firefox158MacOS(), want: []string{"OPTIONS /r", "PATCH /r", "OPTIONS /r.next", "GET /r.next"}},
		{name: "safari 27", p: profiles.Safari27MacOS(), want: []string{"OPTIONS /r", "PATCH /r", "OPTIONS /r.next", "GET /r.next"}},
		{name: "safari 26", p: profiles.Safari26MacOS(), want: []string{"OPTIONS /r", "PATCH /r", "OPTIONS /r.next", "GET /r.next"}},
		{name: "no rule", p: noRule, want: []string{"OPTIONS /r", "PATCH /r"}, err: ErrNotMeasured},
	} {
		e := &redirectEngine{
			redirects: map[string]redirectAnswer{
				"/r": {status: http.StatusSeeOther, header: allowAll()},
			},
		}
		err := fetchFrom(t, redirectClient(t, tc.p, e), http.MethodPatch, "https://www.b.test/r", "x")
		if (tc.err == nil && err != nil) || (tc.err != nil && !errors.Is(err, tc.err)) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.err)
		}
		got := make([]string, 0, len(e.sent))
		for _, s := range e.sent {
			got = append(got, s.method+" "+s.path)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: sent %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A navigation's redirect is sent as the captures show: a typed URL's
// (nav-redirect captures: same origin, same site, cross site, http:// to
// https://) and a script's from a page (redirects2: no Sec-Fetch-User)
// are measured; a link's from a page, which sends Sec-Fetch-User, is not
// where the browser sends it (Chrome, Firefox), nor is a page's
// navigation redirected to another origin.
func TestNavigationRedirectMeasured(t *testing.T) {
	for _, name := range profiles.Names() {
		p, err := profiles.ByName(name)
		if err != nil {
			t.Fatal(err)
		}
		sendsUser := valueOf(p.Headers, "sec-fetch-user") != ""
		for _, tc := range []struct {
			name, from, start, to string
			script                bool
			unmeasured            bool
		}{
			{name: "typed, same origin", start: "https://www.a.test/r", to: "/next"},
			{name: "typed, same site", start: "https://a.test/r", to: "https://www.a.test/next"},
			{name: "typed, cross site", start: "https://www.a.test/r", to: "https://www.b.test/next"},
			{name: "typed, http to https", start: "http://www.a.test/r", to: "https://www.a.test/next"},
			{name: "script, same origin", from: corsPage, start: "https://www.a.test/r", to: "/next", script: true},
			{name: "link, same origin", from: corsPage, start: "https://www.a.test/r", to: "/next", unmeasured: sendsUser},
			{name: "script, cross site", from: corsPage, start: "https://www.a.test/r", to: "https://www.b.test/next", script: true, unmeasured: true},
		} {
			start, _ := url.Parse(tc.start)
			e := &redirectEngine{
				redirects: map[string]redirectAnswer{
					start.Path: {status: http.StatusFound, to: tc.to},
				},
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.start, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.script {
				req.Header["Sec-Fetch-User"] = nil
			}
			var r Request
			if tc.from != "" {
				r.From, _ = url.Parse(tc.from)
			}
			resp, err := redirectClient(t, p, e).Do(With(req, r))
			if err == nil {
				_ = resp.Body.Close()
			}
			if tc.unmeasured != errors.Is(err, ErrNotMeasured) || (!tc.unmeasured && err != nil) {
				t.Errorf("%s %s: got %v, want ErrNotMeasured %v", name, tc.name, err, tc.unmeasured)
			}
		}
	}
}
