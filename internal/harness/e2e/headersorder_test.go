package e2e

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// headersOrderGaps are the headers-order steps mimicry cannot match: an XHR
// whose page set headers out of name order, which the browser follows. An
// http.Header has no order, so mimicry sorts by name as fetch() does.
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

// Every request of the headers-order page, made as the page's fetch() or
// XHR, is the browser's: where page-set headers go, one line per name, the
// spelling, and the same preflights. The forbidden sec-a is left out.
func TestHeadersOrderMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			want, err := loadPages(t, p, "headers-order", ".pagetrace.json.gz")
			if err != nil {
				t.Fatal(err)
			}

			s := startPage(t, p, server.HeadersOrder)
			s.planEvery()

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
			gaps := headersOrderGaps[capture.Source(p, "headers-order")]

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
