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

// The cookies page, walked through mimicry as the browser walked it (page
// A, a third-party setter, redirects across sites, a navigation to site C
// and its requests, a link and a form POST back to A), sends every probe
// as the browser did: the same header lines in the same order, with the
// same values and HTTP/2 weight, and so the browser's cookies in its
// order: SameSite on cross-site navigations, partitions under another
// top-level site, None without Secure, redirects, path and domain order.
// The page's navigations back to A (by location.href and a form POST, over HTTP/2) carry
// both Referer and Cookie in every browser sample, all alike: mimicry's
// must be each sample's, HPACK-decoded line for line, which is where the
// navigation's referer and cookie slots are compared end to end.
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
