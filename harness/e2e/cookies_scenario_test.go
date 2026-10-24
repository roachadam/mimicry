package e2e

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// The cookies page, walked through mimicry as the browser walked it (page
// A, a third-party setter, redirects across sites, a navigation to site C
// and its requests, a link and a form POST back to A), sends every probe
// as the browser did: the same header lines in the same order, with the
// same values and HTTP/2 weight, and so the browser's cookies in its
// order: SameSite on cross-site navigations, partitions under another
// top-level site, None without Secure, redirects, path and domain order.
func TestCookiesScenarioMatchesBrowser(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			if p.Kinds == nil {
				t.Fatalf("%s describes no request kinds", name)
			}
			want, err := capture.LoadPages(captureDir + name + ".cookies.pagetrace.json.gz")
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
		})
	}
}

// runCookiesPage walks the cookies page through mimicry as profile p.
func runCookiesPage(t *testing.T, p *profile.Profile) *capture.Page {
	t.Helper()
	s := startPage(t, p, server.Cookies)
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
