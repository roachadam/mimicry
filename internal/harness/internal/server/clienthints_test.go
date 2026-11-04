package server

import (
	"strings"
	"testing"
)

// TestClientHintsPageChains checks that the client-hints page runs to the
// end: every step is made by a document the page reaches (/ or an earlier
// navigation), each document leads to at most one next navigation, no
// two steps share a URL, and the delegation headers name the third party
// on the page's port. A broken chain stalls every capture until its
// timeout.
func TestClientHintsPageChains(t *testing.T) {
	reached := map[string]bool{
		"/": true,
	}
	navigates := map[string]int{}
	urls := map[string]bool{}

	for _, s := range hintSteps() {
		if !reached[s.Page] {
			t.Errorf("%s: made by %s, which no earlier step navigates to", s.Label, s.Page)
		}

		url := s.Host + s.Path()
		if urls[url] {
			t.Errorf("%s: %s is another step's URL too", s.Label, url)
		}

		urls[url] = true

		if s.Kind == "navigate" {
			navigates[s.Page]++

			reached[s.Path()] = true
		}
	}

	for page, n := range navigates {
		if n > 1 {
			t.Errorf("%s navigates %d times", page, n)
		}
	}

	const port = "8443"

	third := "https://" + chThird + ":" + port

	for _, tc := range []struct {
		host, path, header string
	}{
		{chHost, "/h3/deleg.nav", "permissions-policy"},
		{chHost, "/h3/optin.nav", "accept-ch"},
		{chCritical, "/h3/crit.nav", "critical-ch"},
		{chHost, "/h3/clear.nav", "clear-site-data"},
	} {
		var value string

		r := request{
			method: "GET",
			host:   tc.host + ":" + port,
			path:   tc.path,
		}
		for _, h := range ClientHints.answer(r).headers {
			if h.Name == tc.header {
				value = h.Value
			}
		}

		if value == "" || strings.Contains(value, "%") {
			t.Errorf("%s%s: %s = %q", tc.host, tc.path, tc.header, value)
		}
	}

	if got := clientHintsHead("/h3/meta.nav", port); !strings.Contains(got, `content="sec-ch-ua `+third+`;`) {
		t.Errorf("meta.nav head = %s, want a Delegate-CH meta tag naming %s", got, third)
	}

	policy := ""
	for _, h := range clientHintsHeaders(chHost, port, "/h3/deleg.nav") {
		policy = h.Value
	}

	if !strings.Contains(policy, `ch-ua-platform-version=(self "`+third+`")`) {
		t.Errorf("deleg.nav Permissions-Policy = %s, want it delegating to %s", policy, third)
	}
}
