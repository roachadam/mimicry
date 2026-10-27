package e2e

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// Every step of the cookies-edge page through mimicry sends each sample's
// Cookie header: prefixes, nameless cookies, order, schemeful SameSite,
// Secure on http://localhost, and the per-site limit.
func TestCookiesEdgeMatchesBrowser(t *testing.T) {
	matchCookiesEdgePage(t, server.CookiesEdge, server.CookiesEdgeSteps())
}

// matchCookiesEdgePage walks the page of sc (a cookies-edge scenario, its
// steps) through mimicry as every profile and compares each step's Cookie
// header with every sample of the browser's capture of sc.
func matchCookiesEdgePage(t *testing.T, sc *server.Scenario, steps []server.CookiesEdgeStep) {
	t.Helper()
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			want, err := loadPages(t, p, sc.Name, ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}
			plain := edgeSchemes(t, want, steps)
			got := runCookiesEdgePage(t, p, sc, steps, plain)
			for _, st := range steps {
				g, ok := edgeCookie(got, st, plain[st.Label])
				if !ok {
					t.Errorf("%s: mimicry did not send it", st.Label)
					continue
				}
				for i, w := range want {
					if wc, _ := edgeCookie(w, st, plain[st.Label]); wc != g {
						t.Errorf("%s: Cookie\n%q\nbrowser sample %d:\n%q", st.Label, g, i, wc)
					}
				}
			}
		})
	}
}

// edgeSchemes returns, for each step, whether the browser sent it over
// plain HTTP: every sample must agree and have sent it. A Plain step the
// browser sent over HTTPS (Safari's automatic HTTPS upgrade, which reaches
// TLS on the page's one port) is replayed as the browser sent it, and
// logged.
func edgeSchemes(t *testing.T, pages []*capture.Page, steps []server.CookiesEdgeStep) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, st := range steps {
		for i, page := range pages {
			_, h1 := edgeCookie(page, st, true)
			_, h2 := edgeCookie(page, st, false)
			if h1 == h2 {
				t.Fatalf("%s: browser sample %d sent it over plain HTTP %v and HTTPS %v", st.Label, i, h1, h2)
			}
			if prev, ok := out[st.Label]; ok && prev != h1 {
				t.Fatalf("%s: browser samples differ in scheme", st.Label)
			}
			out[st.Label] = h1
		}
		if st.Plain && !out[st.Label] {
			t.Logf("%s: the browser upgraded it to https://, replayed so", st.Label)
		}
	}
	return out
}

// edgeCookie returns the Cookie header page's request for st carried (its
// HTTP/2 crumbs joined), over plain HTTP/1.1 or HTTPS (HTTP/2), and whether
// the request is there.
func edgeCookie(page *capture.Page, st server.CookiesEdgeStep, plain bool) (string, bool) {
	sameHost := func(h string) bool {
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		}
		return h == st.Host
	}
	if plain {
		for _, r := range page.H1 {
			_, rest, _ := strings.Cut(r.Line, " ")
			target, _, _ := strings.Cut(rest, " ")
			if target != st.Path || !sameHost(r.Host) {
				continue
			}
			var crumbs []string
			for _, l := range r.Headers {
				if k, v, _ := strings.Cut(l, ":"); strings.EqualFold(k, "cookie") {
					crumbs = append(crumbs, strings.TrimSpace(v))
				}
			}
			return strings.Join(crumbs, "; "), true
		}
		return "", false
	}
	for _, tr := range page.H2 {
		if !sameHost(tr.Host) {
			continue
		}
		for _, r := range tr.Requests {
			if r.Path != st.Path {
				continue
			}
			var crumbs []string
			for _, ins := range r.Instructions {
				if ins.Name == "cookie" {
					crumbs = append(crumbs, ins.Value)
				}
			}
			return strings.Join(crumbs, "; "), true
		}
	}
	return "", false
}

// runCookiesEdgePage walks a cookies-edge page (sc, its steps) through
// mimicry as profile p: each navigation from the page before it, by script
// (no Sec-Fetch-User), each other step a same-origin fetch() with
// credentials "include" from the last navigation's page, over plain HTTP
// where plain says the browser used it, a Plain step on the scenario's
// plain port if it has one.
func runCookiesEdgePage(t *testing.T, p *profile.Profile, sc *server.Scenario, steps []server.CookiesEdgeStep, plain map[string]bool) *capture.Page {
	t.Helper()
	s := startPage(t, p, sc)
	s.planEvery()
	target := func(st server.CookiesEdgeStep) *url.URL {
		scheme, port := "https", s.port
		if plain[st.Label] {
			scheme = "http"
		}
		if st.Plain && s.plainPort != "" {
			if !plain[st.Label] {
				t.Fatalf("%s: the capture has it over https://, on a port without TLS", st.Label)
			}
			port = s.plainPort
		}
		u, err := url.Parse(scheme + "://" + net.JoinHostPort(st.Host, port) + st.Path)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	var page *url.URL
	for _, st := range steps {
		u := target(st)
		if st.Nav {
			var h http.Header
			if page != nil {
				h = http.Header{
					"Sec-Fetch-User": nil,
				}
			}
			s.send(http.MethodGet, u.String(), mimicry.Request{
				Kind: mimicry.Navigate,
				From: page,
			}, h, "")
			page = u
			continue
		}
		s.send(http.MethodGet, u.String(), mimicry.Request{
			Kind:        mimicry.Fetch,
			From:        page,
			Credentials: mimicry.CredentialsInclude,
		}, nil, "")
	}
	return s.finish(page.ResolveReference(&url.URL{Path: "/done"}).String())
}
