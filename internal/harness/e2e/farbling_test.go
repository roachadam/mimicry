package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/testprofiles"
	"github.com/roachadam/mimicry/profile"
)

// A farbling profile's Accept-Language (profile.Context.AcceptLanguageQ,
// Brave's Shields) against its samples as sent, and mimicry's against the
// same rules. On the page and kinds pages every request but the CORS
// preflights carries one q value of the profile's, the top-level page's,
// whatever host it goes to (cdn.kinds-b.test's too); the preflights carry
// the lists' own. A navigation redirected to another site keeps its first
// URL's q on every hop. The samples between them draw more than one
// value, and mimicry draws one per page origin, kept, from the whole set.
// The other tests compare everything else unfarbled (loadProfile).
func TestAcceptLanguageFarbling(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			loaded, _, err := testprofiles.Load(name)
			if err != nil {
				t.Fatal(err)
			}

			p := loaded.Spec()
			if p.Context == nil || len(p.Context.AcceptLanguageQ) == 0 {
				if capture.Farbles(p.Browser) {
					t.Fatalf("%s farbles its accept-language, but the profile has no context.acceptLanguageQ", p.Browser)
				}

				t.Skip("the profile farbles nothing")
			}

			own := valueOf(p.Headers, "accept-language")
			drawn := map[string]bool{}

			for i, page := range rawPages(t, p, "kinds") {
				drawn[checkOneQ(t, fmt.Sprintf("kinds sample %d", i+1), wireRequests(page), p, own)] = true
			}

			path, file, _ := capturePath(t, p, capture.DefaultScenario, ".h2trace.json")
			skipIfMissing(t, path, file)

			traces, err := h2trace.Load(path)
			if err != nil {
				t.Fatal(err)
			}

			for i, tr := range traces {
				drawn[checkOneQ(t, fmt.Sprintf("page sample %d", i+1), wireRequests(&capture.Page{
					H2: []*h2trace.Trace{tr},
				}), p, own)] = true
			}

			sc := scenario(t, "nav-redirect-cs")
			step, _ := sc.NavRedirectStep()
			chain := append([]string{step.StartPath()}, step.HopPaths()...)

			for i, page := range rawPages(t, p, sc.Name) {
				drawn[checkChainQ(t, fmt.Sprintf("%s sample %d", sc.Name, i+1), page, chain, p)] = true
			}

			if len(drawn) < 2 {
				t.Errorf("every sample drew q %v: nothing shows the farbling", slices.Sorted(func(yield func(string) bool) {
					for q := range drawn {
						if !yield(q) {
							return
						}
					}
				}))
			}

			checkOneQ(t, "mimicry's kinds page", wireRequests(runKindsPage(t, p, scenario(t, "kinds"))), p, own)

			s := startPage(t, p, sc)
			if _, err := redirectsSend(t, s, http.MethodGet, s.url(step.Host, step.StartPath()), "", nil, mimicry.Request{}, false); err != nil {
				t.Fatal(err)
			}

			checkChainQ(t, "mimicry's "+sc.Name, s.finish(s.url(step.LastHost(), "/done")), chain, p)
			checkPlannedQ(t, p)
		})
	}
}

// rawPages are p's page captures of scenario as sent: not unfarbled.
func rawPages(t *testing.T, p *profile.Spec, scenario string) []*capture.Page {
	t.Helper()

	path, file, _ := capturePath(t, p, scenario, ".pagetrace.json.gz")
	skipIfMissing(t, path, file)

	pages, err := capture.LoadPages(path)
	if err != nil {
		t.Fatal(err)
	}

	return pages
}

// languageOf is w's Accept-Language value, "" when it sends none, and
// whether w is a CORS preflight: an OPTIONS with
// Access-Control-Request-Method (a page's own OPTIONS fetch is not one).
func languageOf(w *wireRequest) (value string, preflight bool) {
	method, acrm := w.method, false

	for i, l := range w.lines {
		if i == 0 && w.proto == "HTTP/1.1" {
			method, _, _ = strings.Cut(l, " ")

			continue
		}

		switch name, v, _ := strings.Cut(l, ": "); strings.ToLower(name) {
		case "accept-language":
			value = v
		case "access-control-request-method":
			acrm = true
		}
	}

	return value, method == http.MethodOptions && acrm
}

// checkOneQ holds one page's requests to the rule: every one but the
// preflights with one q value of p's, the preflights with the lists' own
// value. It returns the q drawn.
func checkOneQ(t *testing.T, at string, reqs []*wireRequest, p *profile.Spec, own string) string {
	t.Helper()

	drawn := ""

	for _, w := range reqs {
		value, preflight := languageOf(w)
		if value == "" {
			continue
		}

		if preflight {
			if value != own {
				t.Errorf("%s: the preflight of %s sent accept-language %q, not the lists' %q", at, w.path, value, own)
			}

			continue
		}

		q, unfarbled, ok := capture.FarbledQ(value)

		switch {
		case !ok || unfarbled != own || !slices.Contains(p.Context.AcceptLanguageQ, q):
			t.Errorf("%s: %s sent accept-language %q: not the lists' %q with a q of %v", at, w.path, value, own, p.Context.AcceptLanguageQ)
		case drawn == "":
			drawn = q
		case q != drawn:
			t.Errorf("%s: %s sent q %s, the page's other requests %s", at, w.path, q, drawn)
		}
	}

	if drawn == "" {
		t.Errorf("%s: no request sent accept-language", at)
	}

	return drawn
}

// checkChainQ holds a navigation and its redirect hops (chain, their
// paths) to one q value of p's, and returns it.
func checkChainQ(t *testing.T, at string, page *capture.Page, chain []string, p *profile.Spec) string {
	t.Helper()

	drawn := ""

	for _, w := range wireRequests(page) {
		if !slices.Contains(chain, w.path) {
			continue
		}

		value, _ := languageOf(w)
		q, _, ok := capture.FarbledQ(value)

		switch {
		case !ok || !slices.Contains(p.Context.AcceptLanguageQ, q):
			t.Errorf("%s: %s sent accept-language %q, no q of %v", at, w.path, value, p.Context.AcceptLanguageQ)
		case drawn == "":
			drawn = q
		case q != drawn:
			t.Errorf("%s: the redirect hop %s sent q %s, the navigation %s", at, w.path, q, drawn)
		}
	}

	if drawn == "" {
		t.Errorf("%s: none of %v was sent", at, chain)
	}

	return drawn
}

// checkPlannedQ plans navigations to many page origins through one
// mimicry Transport: each keeps its q, every q is p's, and between them
// they draw every one.
func checkPlannedQ(t *testing.T, p *profile.Spec) {
	t.Helper()

	tr, err := mimicry.NewTransport(mimicry.Config{
		Profile: checked(p),
	})
	if err != nil {
		t.Fatal(err)
	}

	defer tr.CloseIdleConnections()

	planned := func(target string) string {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}

		plan, err := tr.Plan(req)
		if err != nil {
			t.Fatal(err)
		}

		for _, h := range plan.HTTP2.Headers {
			if h.Name == "accept-language" {
				q, _, _ := capture.FarbledQ(h.Value)

				return q
			}
		}

		t.Fatalf("the plan for %s sends no accept-language", target)

		return ""
	}

	drawn := map[string]bool{}

	for i := range 200 {
		target := fmt.Sprintf("https://www.site%d.test/", i)
		q := planned(target)

		if !slices.Contains(p.Context.AcceptLanguageQ, q) {
			t.Errorf("mimicry drew q %q for %s, not one of %v", q, target, p.Context.AcceptLanguageQ)
		}

		if again := planned(target + "next"); again != q {
			t.Errorf("mimicry drew q %s for %s, then %s", q, target, again)
		}

		drawn[q] = true
	}

	if len(drawn) != len(p.Context.AcceptLanguageQ) {
		t.Errorf("mimicry drew %d of the %d q values over 200 page origins", len(drawn), len(p.Context.AcceptLanguageQ))
	}
}

// valueOf is the value of the header called name in list.
func valueOf(list []profile.Header, name string) string {
	for _, h := range list {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}

	return ""
}
