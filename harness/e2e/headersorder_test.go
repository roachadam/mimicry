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
	"github.com/roachadam/mimicry/profiles"
)

// headersOrderGaps are, per profile, the headers-order steps mimicry does
// not send as the browser did, all for one reason: an XHR whose page set
// its headers in another order than by name, where the browser's order
// follows the page's (Firefox: XHR keeps it; Chrome and Safari: names that
// collide in their header table take the first set's place). A mimicry
// caller's headers are an http.Header, which has no order: mimicry takes
// them by name, as fetch() hands them over. Any other difference, or one of
// these no longer seen, fails.
var headersOrderGaps = map[string][]string{
	"chrome-155-macos": {
		"letters-rev.xhr.so", "around-rev.xhr.so",
		"letters-rev.xhr.cs", "around-rev.xhr.cs",
		"letters-rev.xhr.h1", "around-rev.xhr.h1",
	},
	"firefox-158-macos": {
		"pair-ba.xhr.so", "letters-rev.xhr.so", "around-rev.xhr.so", "case.xhr.so",
		"pair-ba.xhr.cs", "letters-rev.xhr.cs", "around-rev.xhr.cs", "case.xhr.cs",
		"pair-ba.xhr.h1", "letters-rev.xhr.h1", "around-rev.xhr.h1", "case.xhr.h1",
	},
	"safari-26-macos": {
		"letters-rev.xhr.so", "around-rev.xhr.so", "case.xhr.so",
		"around-rev.xhr.cs", "case.xhr.cs",
		"around-rev.xhr.h1", "case.xhr.h1",
	},
	"safari-27-macos": {
		"letters-rev.xhr.so", "around-rev.xhr.so", "case.xhr.so",
		"around-rev.xhr.cs", "case.xhr.cs",
		"around-rev.xhr.h1", "case.xhr.h1",
	},
}

// Every request of the headers-order page, made through mimicry as the
// page's fetch() or XHR, is the browser's: where each header the page set
// goes among the browser's own (its header table, or the lists' slot),
// one line for a name set twice, the page's spelling over HTTP/1.1 for XHR
// and lowercase for fetch(), and the same preflights. The forbidden name
// (sec-a), which browsers drop, is left out, as a page cannot set it.
func TestHeadersOrderMatchesBrowser(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			want, err := capture.LoadPages(captureDir + name + ".headers-order.pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}
			s := startPage(t, p, server.HeadersOrder)
			hosts, _, _ := server.HeadersOrder.MultiHost()
			page := s.url(hosts[0], "/")
			s.send(http.MethodGet, page, mimicry.Request{}, nil, "")
			from, err := url.Parse(page)
			if err != nil {
				t.Fatal(err)
			}
			kinds := map[string]mimicry.Kind{
				"fetch": mimicry.Fetch,
				"xhr":   mimicry.XHR,
			}
			for _, st := range server.HeadersSteps() {
				h := http.Header{}
				for _, nv := range st.Headers {
					if !strings.HasPrefix(strings.ToLower(nv[0]), "sec-") {
						// As the page spelled it.
						h[nv[0]] = append(h[nv[0]], nv[1])
					}
				}
				s.send(http.MethodGet, s.url(st.Host, "/h/"+st.Label), mimicry.Request{
					Kind: kinds[st.API],
					From: from,
				}, h, "")
			}
			got := s.finish(s.url(hosts[0], "/done"))
			gaps := headersOrderGaps[name]
			for _, st := range server.HeadersSteps() {
				h1 := strings.HasSuffix(st.Label, ".h1")
				path := "/h/" + st.Label
				for _, preflight := range []bool{false, true} {
					g := requestFields(got, path, h1, preflight)
					w := requestFields(want[0], path, h1, preflight)
					if preflight && (g == nil) != (w == nil) {
						t.Errorf("%s preflight: mimicry sent one %v, the browser %v", st.Label, g != nil, w != nil)
						continue
					}
					if g == nil {
						if !preflight {
							t.Errorf("%s: mimicry sent no request", st.Label)
						}
						continue
					}
					forms := browserForms(t, want, path, h1, preflight)
					same := slices.ContainsFunc(forms, func(w *kindsSent) bool {
						return slices.Equal(g.lines, w.lines)
					})
					known := !preflight && slices.Contains(gaps, st.Label)
					switch {
					case same && known:
						t.Errorf("%s: known gap no longer seen", st.Label)
					case !same && known:
						t.Logf("%s: known gap (the page's XHR order):\n got %q\nwant %q", st.Label, g.lines, forms[0].lines)
					case !same:
						name := st.Label
						if preflight {
							name += " preflight"
						}
						t.Errorf("%s:\n got %q\nwant %q", name, g.lines, forms[0].lines)
					}
				}
			}
		})
	}
}
