package e2e

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// Every probe of the cookies page through mimicry has the browser's header
// lines, order, values and HTTP/2 weight, and so its cookies in its order
// (SameSite, partitions, None without Secure, redirects, path and domain).
// The navigations back to A compare the referer and cookie slots end to end.
func TestCookiesScenarioMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			if p.Kinds == nil {
				t.Fatalf("%s describes no request kinds", name)
			}
			want, err := loadPages(t, p, "cookies", ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}
			got := runCookiesPage(t, p)
			for _, label := range []string{
				"c-setcookie.cs", "c-order.so",
				"c-redirect.cs-ss", "c-redirect.cs-ss.hop",
				"c-redirect.ss-so", "c-redirect.ss-so.hop",
				"c-redirect.cs-so", "c-redirect.cs-so.hop",
				"c-part.cs", "c-third.so",
				"c-nav-get.so", "c-nav-post.so",
			} {
				compareKind(t, got, want, label, false, false)
			}
			for _, path := range []string{"/k/c-nav-get.so", "/k/c-nav-post.so"} {
				g := requestFields(got, path, false, false)
				if g == nil || !hasHeader(g.lines, "referer") || !hasHeader(g.lines, "cookie") {
					t.Fatalf("%s: mimicry's navigation lacks referer or cookie", path)
				}
				for i, page := range want {
					if w := requestFields(page, path, false, false); w == nil || !slices.Equal(g.lines, w.lines) {
						t.Errorf("%s: sample %d differs", path, i+1)
					}
				}
			}
		})
	}
}

// hasHeader reports whether lines carry a header named name.
func hasHeader(lines []string, name string) bool {
	return slices.ContainsFunc(lines, func(l string) bool {
		n, _, _ := strings.Cut(l, ": ")
		return strings.EqualFold(n, name)
	})
}

// runCookiesPage walks the cookies page through mimicry as profile p.
func runCookiesPage(t *testing.T, p *profile.Profile) *capture.Page {
	t.Helper()
	s := startPage(t, p, server.Cookies)
	s.planEvery()
	const (
		siteA = "www.kinds-a.test"
		same  = "static.kinds-a.test"
		cross = "cdn.kinds-b.test"
		siteC = "www.kinds-c.test"
	)
	parse := func(u string) *url.URL {
		v, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}

		return v
	}
	// nav navigates from page from; the page's script does (location.href,
	// form.submit()), so without user activation: no Sec-Fetch-User.
	nav := func(method, target string, from *url.URL, h http.Header, body string) *url.URL {
		if from != nil {
			h = h.Clone()
			if h == nil {
				h = http.Header{}
			}
			h["Sec-Fetch-User"] = nil
		}
		s.send(method, target, mimicry.Request{
			Kind: mimicry.Navigate,
			From: from,
		}, h, body)

		return parse(target)
	}
	image := func(target string, from *url.URL) {
		s.send(http.MethodGet, target, mimicry.Request{
			Kind: mimicry.InjectedImage,
			From: from,
		}, nil, "")
	}

	pageA := nav(http.MethodGet, s.url(siteA, "/"), nil, nil, "")
	image(s.url(cross, "/k/c-setcookie.cs"), pageA)
	image(s.url(siteA, "/k/c-order.so"), pageA)
	image(s.url(cross, "/k/c-redirect.cs-ss"), pageA)
	image(s.url(same, "/k/c-redirect.ss-so"), pageA)
	image(s.url(cross, "/k/c-redirect.cs-so"), pageA)
	pageC := nav(http.MethodGet, s.url(siteC, "/c"), pageA, nil, "")
	image(s.url(cross, "/k/c-part.cs"), pageC)
	image(s.url(siteA, "/k/c-third.so"), pageC)
	pageGet := nav(http.MethodGet, s.url(siteA, "/k/c-nav-get.so"), pageC, nil, "")
	pagePost := nav(http.MethodGet, s.url(siteC, "/c-post"), pageGet, nil, "")
	nav(http.MethodPost, s.url(siteA, "/k/c-nav-post.so"), pagePost, http.Header{
		"Content-Type": {"application/x-www-form-urlencoded"},
	}, "a=1")

	return s.finish(s.url(siteA, "/done"))
}
