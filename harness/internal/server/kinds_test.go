package server

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// The kinds page sets its cookies, schedules every step on its host, answers
// each step as its API expects (redirecting across sites where asked,
// setting third-party cookies, allowing preflights), lands its form POST on
// a page that cleans every host up, and hands over the whole page at /done.
func TestKinds(t *testing.T) {
	client, port, captures := replayServer(t, Kinds)
	url := func(host, path string) string {
		return "https://" + host + ":" + port + path
	}

	resp, html := fetch(t, client, "GET", url(kindsHost, "/"), nil)
	if got := len(resp.Header.Values("Set-Cookie")); got != len(KindsCookies) {
		t.Errorf("document sets %d cookies, want %d", got, len(KindsCookies))
	}
	_, rest, _ := strings.Cut(html, "const steps = ")
	list, _, _ := strings.Cut(rest, ";\n")
	var steps []KindsStep
	if err := json.Unmarshal([]byte(list), &steps); err != nil {
		t.Fatalf("steps in document: %v", err)
	}
	want := KindsSteps()
	if len(steps) != len(want) {
		t.Fatalf("document schedules %d steps, want %d", len(steps), len(want))
	}
	for i, s := range steps {
		if wantURL := url(want[i].Host, "/k/"+want[i].Label); s.URL != wantURL {
			t.Errorf("step %d (%s) at %s, want %s", i, s.Label, s.URL, wantURL)
		}
	}
	for _, s := range KindsMarkup() {
		if s.Tag == "" {
			continue // requested by the page's CSS, checked below
		}
		if tag := fmt.Sprintf(s.Tag, url(s.Host, "/k/"+s.Label)); !strings.Contains(html, tag) {
			t.Errorf("document lacks %s", tag)
		}
	}
	_, css := fetch(t, client, "GET", url(kindsHost, "/k/style-fonts.so"), nil)
	for _, font := range []string{`url("/k/font-css.so")`, `url("` + url(kindsCrossSite, "/k/font-css.cs") + `")`} {
		if !strings.Contains(css, font) {
			t.Errorf("font stylesheet lacks %s:\n%s", font, css)
		}
	}
	if !strings.Contains(html, `f.action = "`+url(kindsHost, "/k/"+kindsFormLabel)+`"`) {
		t.Error("document does not end with the form POST")
	}
	if head, _, _ := strings.Cut(html, "</head>"); !strings.Contains(head, "script-blocking.so") || strings.Contains(head, "image-markup.so") {
		t.Error("blocking script must be in the head, the image in the body")
	}

	resp, _ = fetch(t, client, "GET", url(kindsCrossSite, "/k/image-setcookie.cs"), nil)
	if got := resp.Header.Values("Set-Cookie"); !slices.Equal(got, KindsThirdPartyCookies) {
		t.Errorf("third-party cookies %q", got)
	}
	resp, _ = fetch(t, client, "GET", url(kindsHost, "/k/fetch-redirect.so-cs"), nil)
	hop := url(kindsCrossSite, "/k/fetch-redirect.so-cs.hop")
	if resp.StatusCode != 302 || resp.Header.Get("Location") != hop {
		t.Fatalf("redirect: %s to %q, want 302 to %s", resp.Status, resp.Header.Get("Location"), hop)
	}
	resp, _ = fetch(t, client, "GET", hop, http.Header{"Origin": {"null"}})
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "null" {
		t.Errorf("redirect hop: %s %v", resp.Status, resp.Header)
	}
	resp, _ = fetch(t, client, "OPTIONS", url(kindsSameSite, "/k/fetch-custom.ss"), http.Header{
		"Origin":                         {url(kindsHost, "")},
		"Access-Control-Request-Headers": {"x-mimicry"},
	})
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Headers") != "x-mimicry" {
		t.Errorf("preflight: %s %v", resp.Status, resp.Header)
	}

	_, landing := fetch(t, client, "POST", url(kindsHost, "/k/"+kindsFormLabel), nil)
	for _, h := range []string{kindsHost, kindsSameSite, kindsCrossSite} {
		if !strings.Contains(landing, url(h, "/cleanup")) {
			t.Errorf("landing page does not clean up %s", h)
		}
	}
	resp, _ = fetch(t, client, "GET", url(kindsHost, "/cleanup"), nil)
	deletions := resp.Header.Values("Set-Cookie")
	for _, d := range []string{
		"k_site=; Path=/; Secure; Max-Age=0; Domain=" + kindsSiteA,
		"=; Path=/; Secure; Max-Age=0", // knoeq: a cookie without a name
		"k_quoted=; Path=/; Secure; Max-Age=0",
	} {
		if !slices.Contains(deletions, d) {
			t.Errorf("cleanup lacks %q: %q", d, deletions)
		}
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
	if wantHosts := []string{kindsCrossSite, kindsSameSite, kindsHost}; !slices.Equal(hosts, wantHosts) {
		t.Errorf("page traces from %v, want %v", hosts, wantHosts)
	}
}

// A page capture's ClientHello is the page's first connection's, a full
// handshake, even when /done arrives on a connection that resumed a session
// (as Chrome's later HTTP/1.1 connections do); every connection's resumption
// is recorded.
func TestPageCaptureUsesFirstHandshake(t *testing.T) {
	client, port, captures := replayServer(t, KindsHTTP1)
	tr := client.Transport.(*http.Transport)
	tr.TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(4)
	base := "https://" + kindsHost + ":" + port
	fetch(t, client, "GET", base+"/", nil)
	// A new connection, resuming the first one's session.
	tr.CloseIdleConnections()
	fetch(t, client, "GET", base+"/done", nil)

	var c *capture.Capture
	for c == nil {
		if got := receive(t, captures); got.Finished {
			c = got
		}
	}
	if c.HasExtension(profile.ExtPreSharedKey) {
		t.Error("page capture took the resumed connection's ClientHello")
	}
	resumed := make([]bool, 0, len(c.Page.Connections))
	for _, conn := range c.Page.Connections {
		resumed = append(resumed, conn.Resumed)
	}
	if !slices.Equal(resumed, []bool{false, true}) {
		t.Errorf("connections resumed %v, want [false true]", resumed)
	}
}
