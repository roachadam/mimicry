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
	"github.com/roachadam/mimicry/profiles"
)

// The cookies-edge page, walked through mimicry as the browser walked it
// (its navigations and same-origin fetches, over http:// or https:// as the
// browser sent each), sends every step the Cookie header each of the
// browser's samples sent: the __Host- and __Http- prefixes, nameless
// cookies, order after a value changes, schemeful SameSite, Secure cookies
// on http://localhost and the per-site limit on 200 cookies.
func TestCookiesEdgeMatchesBrowser(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			want, err := capture.LoadPages(captureDir + name + ".cookies-edge.pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}
			steps := server.CookiesEdgeSteps()
			plain := edgeSchemes(t, want, steps)
			got := runCookiesEdgePage(t, p, steps, plain)
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

// runCookiesEdgePage walks the cookies-edge page through mimicry as profile
// p: each navigation from the page before it, by script (no
// Sec-Fetch-User), each other step a same-origin fetch() with credentials
// "include" from the last navigation's page, over plain HTTP where plain
// says the browser used it.
func runCookiesEdgePage(t *testing.T, p *profile.Profile, steps []server.CookiesEdgeStep, plain map[string]bool) *capture.Page {
	t.Helper()
	s := startPage(t, p, server.CookiesEdge)
	target := func(st server.CookiesEdgeStep) *url.URL {
		scheme := "https"
		if plain[st.Label] {
			scheme = "http"
		}
		u, err := url.Parse(scheme + "://" + net.JoinHostPort(st.Host, s.port) + st.Path)
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
	return s.finish(page.JoinPath("..", "done").String())
}
