package server

import (
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
)

// The cookies page sets its cookies, loads the third-party setter, the
// order probe and the redirects, goes to site C, which navigates back to A
// by a link and by a form POST; the POST lands on a page cleaning every
// host up, each deletion matching how its cookie was set; /done hands over
// the page across all four hosts.
func TestCookies(t *testing.T) {
	client, port, captures := replayServer(t, Cookies)
	url := func(host, path string) string {
		return "https://" + host + ":" + port + path
	}
	follows := func(html string, urls ...string) {
		t.Helper()
		rest := html
		for _, u := range urls {
			_, after, ok := strings.Cut(rest, u)
			if !ok {
				t.Fatalf("page does not go to %s next:\n%s", u, html)
			}
			rest = after
		}
	}

	resp, html := fetch(t, client, "GET", url(kindsHost, "/"), nil)
	if got := resp.Header.Values("Set-Cookie"); !slices.Equal(got, CookiesSet) {
		t.Errorf("page A sets %q", got)
	}
	follows(html,
		url(kindsCrossSite, "/k/c-setcookie.cs"),
		"/k/c-order.so",
		url(kindsCrossSite, "/k/c-redirect.cs-ss"),
		url(kindsSameSite, "/k/c-redirect.ss-so"),
		url(kindsCrossSite, "/k/c-redirect.cs-so"),
		url(cookiesSiteC, "/c"),
	)
	resp, _ = fetch(t, client, "GET", url(kindsCrossSite, "/k/c-setcookie.cs"), nil)
	if got := resp.Header.Values("Set-Cookie"); !slices.Equal(got, CookiesThirdParty) {
		t.Errorf("third-party cookies %q", got)
	}
	for label, to := range CookiesRedirects {
		from := kindsCrossSite
		if strings.HasSuffix(label, ".ss-so") {
			from = kindsSameSite
		}
		resp, _ = fetch(t, client, "GET", url(from, "/k/"+label), nil)
		if hop := url(to, "/k/"+label+".hop"); resp.StatusCode != 302 || resp.Header.Get("Location") != hop {
			t.Errorf("%s: %s to %q, want 302 to %s", label, resp.Status, resp.Header.Get("Location"), hop)
		}
	}

	_, html = fetch(t, client, "GET", url(cookiesSiteC, "/c"), nil)
	follows(html, url(kindsCrossSite, "/k/c-part.cs"), url(kindsHost, "/k/c-third.so"), url(kindsHost, "/k/c-nav-get.so"))
	_, html = fetch(t, client, "GET", url(kindsHost, "/k/c-nav-get.so"), nil)
	follows(html, url(cookiesSiteC, "/c-post"))
	_, html = fetch(t, client, "GET", url(cookiesSiteC, "/c-post"), nil)
	follows(html, `f.method = "POST"`, url(kindsHost, "/k/c-nav-post.so"), "f.submit()")
	_, html = fetch(t, client, "POST", url(kindsHost, "/k/c-nav-post.so"), nil)
	for _, h := range []string{kindsHost, kindsSameSite, kindsCrossSite, cookiesSiteC} {
		if !strings.Contains(html, url(h, "/cleanup")) {
			t.Errorf("landing page does not clean up %s", h)
		}
	}

	resp, _ = fetch(t, client, "GET", url(kindsHost, "/cleanup"), nil)
	deletions := resp.Header.Values("Set-Cookie")
	for _, d := range []string{
		"co_dk=; Path=/k; Domain=" + kindsSiteA + "; Secure; Max-Age=0",
		"co_k=; Path=/k; Secure; Max-Age=0",
		"c_noneinsecure=; Path=/; Secure; Max-Age=0",
	} {
		if !slices.Contains(deletions, d) {
			t.Errorf("cleanup of A lacks %q: %q", d, deletions)
		}
	}
	if len(deletions) != len(CookiesSet) {
		t.Errorf("cleanup of A deletes %d cookies, want %d", len(deletions), len(CookiesSet))
	}
	resp, _ = fetch(t, client, "GET", url(kindsCrossSite, "/cleanup"), nil)
	if d := "cb_part=; Path=/; SameSite=None; Partitioned; Secure; Max-Age=0"; !slices.Contains(resp.Header.Values("Set-Cookie"), d) {
		t.Errorf("cleanup of the cross-site host lacks %q", d)
	}

	fetch(t, client, "GET", url(kindsHost, "/done"), nil)
	var page *capture.Page
	for page == nil {
		if c := receive(t, captures); c.Finished {
			page = c.Page
		}
	}
	hosts := make([]string, 0, len(page.H2))
	for _, tr := range page.H2 {
		hosts = append(hosts, tr.Host)
	}
	slices.Sort(hosts)
	if want := []string{kindsCrossSite, kindsSameSite, kindsHost, cookiesSiteC}; !slices.Equal(hosts, want) {
		t.Errorf("page traces from %v, want %v", hosts, want)
	}
}
