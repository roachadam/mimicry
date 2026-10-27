package main

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/internal/site"
	"github.com/roachadam/mimicry/profile"
)

// pages are the captures kindsgen reads for a profile, one page per sample.
// Inherited captures are rewritten to the profile's version.
type pages struct {
	p                                   *profile.Profile
	kinds, kindsH1, plain               []*capture.Page
	cookies, cors, corsEdge, redirects2 []*capture.Page
	headers                             []*capture.Page
	websocket, websocketH2              []*capture.Page
	// navRedirects are the nav-redirect-<x> pages, possibly none.
	navRedirects []scenarioPages
}

// scenarioPages are a scenario's capture, a page per sample.
type scenarioPages struct {
	scenario string
	samples  []*capture.Page
}

// loadPages reads p's captures from dir: its own, or for inherited
// scenarios the source profile's, rewritten to p's version.
func loadPages(dir string, p *profile.Profile) (*pages, error) {
	out := &pages{
		p: p,
	}
	for _, f := range []struct {
		scenario string
		into     *[]*capture.Page
	}{
		{scenario: "kinds", into: &out.kinds},
		{scenario: "kinds-http1", into: &out.kindsH1},
		{scenario: "kinds-plain", into: &out.plain},
		{scenario: "cookies", into: &out.cookies},
		{scenario: "cors", into: &out.cors},
		{scenario: "cors-edge", into: &out.corsEdge},
		{scenario: "redirects2", into: &out.redirects2},
		{scenario: "headers-order", into: &out.headers},
		{scenario: "websocket", into: &out.websocket},
		{scenario: "websocket-h2", into: &out.websocketH2},
	} {
		source, rw, err := capture.Inherited(p, f.scenario)
		if err != nil {
			return nil, err
		}
		got, err := loadFile(capture.File{
			Path:     filepath.Join(dir, source+"."+f.scenario+".pagetrace.json.gz"),
			Scenario: f.scenario,
			Source:   source,
			Rewriter: rw,
		})
		if err != nil {
			return nil, err
		}
		*f.into = got
	}
	files, err := capture.Files(dir, p, ".pagetrace.json.gz")
	if err != nil {
		return nil, err
	}
	files = slices.DeleteFunc(files, func(f capture.File) bool {
		return !strings.HasPrefix(f.Scenario, "nav-redirect-")
	})
	// In scenario order, whichever profile each comes from.
	slices.SortStableFunc(files, func(a, b capture.File) int {
		return strings.Compare(a.Scenario, b.Scenario)
	})
	for _, f := range files {
		got, err := loadFile(f)
		if err != nil {
			return nil, err
		}
		out.navRedirects = append(out.navRedirects, scenarioPages{
			scenario: f.Scenario,
			samples:  got,
		})
	}

	return out, nil
}

// loadFile reads a page capture, its header values rewritten to the
// version of the profile that inherits it.
func loadFile(f capture.File) ([]*capture.Page, error) {
	got, err := capture.LoadPages(f.Path)
	if err != nil {
		return nil, err
	}
	f.Rewriter.Pages(got)

	return got, nil
}

// request is one captured request of the kinds, cookies, cors, cors-edge,
// redirects2 or headers-order page.
type request struct {
	// label is the step ("script-cors.cs"); base is before the first dot.
	label, base string
	method      string
	hop         bool // the request after a redirect
	// path excludes the query.
	host, path string
	// Redirect-hop facts relative to the request whose redirect it
	// follows (set by linkRedirects).
	crossOriginRedirect, crossSiteRedirect, originBefore, methodChanged, insecureBefore bool
	// plain is a kinds-plain request: HTTP/1.1 to a non-secure origin.
	plain bool
	// other requests (not the kinds page) exist over one protocol only.
	other bool
	// site is the URL's own relation to its page (profile.Site*).
	site string
	// h2 merges cookie crumbs into one cookie field.
	h2     []profile.Header
	h1     []profile.Header
	weight uint8
	stream uint32
	conn   int
	sample int
	// pageOrder is the page's set order where the browser keeps it (XHR);
	// nil means by name (fetch()'s Headers).
	pageOrder []string
	// Origin does not make a WebSocket handshake CORS-checked.
	websocket bool
}

// labelSite is the site relation a kinds page label names: so, ss or cs.
// A redirect label ("so-cs") names first leg then hop. The kinds page and
// the redirecting cookies page are on the so host.
func labelSite(label string, hop bool) string {
	_, target, _ := strings.Cut(label, ".")
	if first, second, ok := strings.Cut(target, "-"); ok {
		target = first
		if hop {
			target = second
		}
	}
	switch {
	case strings.HasPrefix(target, "ss"):
		return profile.SiteSameSite
	case strings.HasPrefix(target, "cs"):
		return profile.SiteCrossSite
	}

	return profile.SiteSameOrigin
}

// h1Fields splits HTTP/1.1 header lines into fields, names as sent.
func h1Fields(lines []string) []profile.Header {
	out := make([]profile.Header, 0, len(lines))
	for _, l := range lines {
		name, value, _ := strings.Cut(l, ":")
		out = append(out, profile.Header{
			Name:  name,
			Value: strings.TrimSpace(value),
		})
	}

	return out
}

// h2Requests returns a page's HTTP/2 requests under prefix ("/k/"), with
// sample si.
func h2Requests(page *capture.Page, si int, prefix string) []*request {
	var out []*request
	for ci, tr := range page.H2 {
		weights := map[uint32]uint8{}
		for _, f := range tr.Frames {
			if f.Type == "HEADERS" && f.Priority != nil {
				weights[f.Stream] = f.Priority.Weight
			}
		}
		for _, r := range tr.Requests {
			label, ok := strings.CutPrefix(r.Path, prefix)
			if !ok {
				continue
			}
			q := &request{
				host:   tr.Host,
				path:   r.Path,
				conn:   ci,
				weight: weights[r.Stream],
				stream: r.Stream,
				sample: si,
			}
			for _, ins := range r.Instructions {
				switch {
				case ins.Rep == "size_update":
				case ins.Name == ":method":
					q.method = ins.Value
				case strings.HasPrefix(ins.Name, ":"):
				case ins.Name == "cookie" && len(q.h2) > 0 && q.h2[len(q.h2)-1].Name == "cookie":
				default:
					q.h2 = append(q.h2, profile.Header{
						Name:  ins.Name,
						Value: ins.Value,
					})
				}
			}
			q.label, q.hop = strings.CutSuffix(label, ".hop")
			q.base, _, _ = strings.Cut(q.label, ".")
			out = append(out, q)
		}
	}

	return out
}

// collect gathers the kinds and cookies pages' requests from every sample
// of the captures. Plain-HTTP requests include the page's own navigation,
// as label "page". Every kinds request must be captured over both HTTP/2
// and HTTP/1.1, once per sample.
func collect(pg *pages) ([]*request, error) {
	var out []*request
	var errs []error
	byKey := map[string]*request{}
	key := func(sample int, label, method string) string {
		return fmt.Sprint(sample, " ", method, " ", label)
	}
	// hopPrev is a kinds, kinds-plain or cookies hop's previous request:
	// its label's own path.
	hopPrev := func(q *request) string {
		return strings.TrimSuffix(q.path, ".hop")
	}
	for si, page := range pg.kinds {
		qs := h2Requests(page, si, "/k/")
		errs = append(errs, linkRedirects("kinds", qs, hopPrev))
		for _, q := range qs {
			q.site = labelSite(q.label, q.hop)
			label := q.label
			if q.hop {
				label += ".hop"
			}
			k := key(si, label, q.method)
			if byKey[k] != nil {
				errs = append(errs, fmt.Errorf("kinds sample %d: %s %s captured twice over HTTP/2", si+1, q.method, label))
				continue
			}
			byKey[k] = q
			out = append(out, q)
		}
	}
	for si, page := range pg.kindsH1 {
		for _, r := range page.H1 {
			method, rest, _ := strings.Cut(r.Line, " ")
			target, _, _ := strings.Cut(rest, " ")
			label, ok := strings.CutPrefix(target, "/k/")
			if !ok {
				continue
			}
			q := byKey[key(si, label, method)]
			switch {
			case q == nil:
				errs = append(errs, fmt.Errorf("kinds sample %d: %s %s captured over HTTP/1.1 only", si+1, method, label))
				continue
			case q.h1 != nil:
				errs = append(errs, fmt.Errorf("kinds sample %d: %s %s captured twice over HTTP/1.1", si+1, method, label))
				continue
			}
			q.h1 = h1Fields(r.Headers)
		}
	}
	for _, q := range out {
		if q.h1 == nil {
			label := q.label
			if q.hop {
				label += ".hop"
			}
			errs = append(errs, fmt.Errorf("kinds sample %d: %s %s captured over HTTP/2 only", q.sample+1, q.method, label))
		}
	}
	for si, page := range pg.plain {
		var qs []*request
		for _, r := range page.H1 {
			method, rest, _ := strings.Cut(r.Line, " ")
			target, _, _ := strings.Cut(rest, " ")
			target, _, _ = strings.Cut(target, "?")
			label, ok := strings.CutPrefix(target, "/k/")
			switch {
			case target == "/":
				label = "page"
			case !ok:
				continue
			}
			q := &request{
				method: method,
				host:   r.Host,
				path:   target,
				sample: si,
				plain:  true,
				h1:     h1Fields(r.Headers),
			}
			q.label, q.hop = strings.CutSuffix(label, ".hop")
			q.base, _, _ = strings.Cut(q.label, ".")
			q.site = labelSite(q.label, q.hop)
			qs = append(qs, q)
		}
		errs = append(errs, linkRedirects("kinds-plain", qs, hopPrev))
		out = append(out, qs...)
	}
	for si, page := range pg.cookies {
		qs := h2Requests(page, si, "/k/")
		errs = append(errs, linkRedirects("cookies", qs, hopPrev))
		for _, q := range qs {
			q.other = true
			// Cookies-page labels name hosts, not relations. Every redirect
			// starts on the so page, so only redirects change sec-fetch-site.
			if q.hop {
				q.site = labelSite(q.label, true)
				out = append(out, q)

				continue
			}
			v, ok := headerValue(q.h2, "sec-fetch-site")
			switch v {
			case "same-origin":
				q.site = profile.SiteSameOrigin
			case "same-site", "cross-site":
				q.site = v
			default:
				errs = append(errs, fmt.Errorf("cookies sample %d: %s has sec-fetch-site %q (sent %v)", si+1, q.label, v, ok))
				continue
			}
			out = append(out, q)
		}
	}
	steps := map[string]bool{}
	for _, st := range server.CORSSteps() {
		steps[st.Label] = true
	}
	for si, page := range pg.cors {
		qs := h2Requests(page, si, "/p/")
		for _, q := range qs {
			// Every fetch goes to the cross-site host, its label naming
			// it; a label no step has is a redirect's target.
			q.hop = q.method != "OPTIONS" && !steps[q.label]
		}
		errs = append(errs, linkRedirects("cors", qs, func(q *request) string {
			return "/p/" + corsRedirects[q.label]
		}))
		for _, q := range qs {
			// The OPTIONS are the preflight kind's.
			if q.method == "OPTIONS" {
				continue
			}
			q.other = true
			q.base = "cors-" + q.base
			q.site = labelSite(q.label, q.hop)
			out = append(out, q)
		}
	}
	edge, err := corsEdgeRequests(pg.corsEdge)
	if err != nil {
		errs = append(errs, err)
	}
	out = append(out, edge...)
	r2, err := redirects2Requests(pg.redirects2)
	if err != nil {
		errs = append(errs, err)
	}
	out = append(out, r2...)
	navs, err := navRedirectRequests(pg.p, pg.navRedirects)
	if err != nil {
		errs = append(errs, err)
	}
	out = append(out, navs...)
	hdrs, err := headersRequests(pg.headers)
	if err != nil {
		errs = append(errs, err)
	}
	out = append(out, hdrs...)
	sockets, err := websocketRequests(pg.websocket, pg.websocketH2)
	if err != nil {
		errs = append(errs, err)
	}
	out = append(out, sockets...)

	return out, errors.Join(errs...)
}

// corsRedirects names, for each redirect target of the cors page, the step
// whose redirect leads there (server.CORS answers redirect.cs 307).
var corsRedirects = map[string]string{
	"redirect-target.cs": "redirect.cs",
}

// linkRedirects sets each hop's redirect conditions from the request whose
// redirect it follows (path prev(hop)): host change, Origin, method change.
func linkRedirects(page string, reqs []*request, prev func(*request) string) error {
	byPath := map[string]*request{}
	for _, q := range reqs {
		if q.method != "OPTIONS" && byPath[q.path] == nil {
			byPath[q.path] = q
		}
	}
	var errs []error
	for _, q := range reqs {
		if !q.hop {
			continue
		}
		p := byPath[prev(q)]
		if p == nil {
			errs = append(errs, fmt.Errorf("%s sample %d: %s %s follows a redirect from %s, which was not captured", page, q.sample+1, q.method, q.path, prev(q)))
			continue
		}
		// Another host, or the same over the other scheme (a plain-HTTP
		// request's hop to HTTPS).
		q.crossOriginRedirect = p.host != q.host || p.plain != q.plain
		q.crossSiteRedirect = schemefulSite(p) != schemefulSite(q)
		q.originBefore = slices.ContainsFunc(slices.Concat(p.h2, p.h1), func(h profile.Header) bool {
			return strings.EqualFold(h.Name, "origin")
		})
		q.methodChanged = p.method != q.method
		q.insecureBefore = p.plain
	}

	return errors.Join(errs...)
}

// schemefulSite is the schemeful site of q's URL ("https://example.com"),
// as internal/site computes it for mimicry.
func schemefulSite(q *request) string {
	scheme := "https://"
	if q.plain {
		scheme = "http://"
	}

	return scheme + site.Registrable(q.host)
}

// corsEdgeRequests returns the cors-edge page's CORS fetches: /p/<label>
// and its hops /p/<label>.hop<n>. OPTIONS belong to the preflight kind.
func corsEdgeRequests(samples []*capture.Page) ([]*request, error) {
	var out []*request
	errs := make([]error, 0, len(samples))
	for si, page := range samples {
		qs := h2Requests(page, si, "/p/")
		for _, q := range qs {
			label, hop, _ := strings.Cut(q.label, ".hop")
			q.label, q.hop = label, hop != ""
		}
		errs = append(errs, linkRedirects("cors-edge", qs, previousHop))
		for _, q := range qs {
			if q.method == "OPTIONS" {
				continue
			}
			q.other = true
			q.base = "cors-edge"
			// A hop stays on its label's host: its own relation is the
			// label's, as is the chain's.
			q.site = labelSite(q.label, false)
			out = append(out, q)
		}
	}

	return out, errors.Join(errs...)
}

// redirects2Requests returns the redirects2 page's requests
// (server.Redirects2Steps), each with base "redir-<step kind>".
// A navigation's GET first leg is skipped: a script navigation is not the
// profile's. A repeated path is skipped with the rest of its step:
// Chrome's error page reloads a navigation its redirect limit stopped.
func redirects2Requests(samples []*capture.Page) ([]*request, error) {
	steps := map[string]server.Redirects2Step{}
	for _, st := range server.Redirects2Steps() {
		steps[st.Label] = st
	}
	var out []*request
	var errs []error
	for si, page := range samples {
		var qs []*request
		seen, reloaded := map[string]bool{}, map[string]bool{}
		for _, q := range h2Requests(page, si, "/p/") {
			label, hop, _ := strings.Cut(q.label, ".hop")
			st, ok := steps[label]
			if !ok {
				errs = append(errs, fmt.Errorf("redirects2 sample %d: %s is no step", si+1, q.path))
				continue
			}
			if q.method != "OPTIONS" {
				reloaded[label] = reloaded[label] || seen[q.path]
				seen[q.path] = true
			}
			if reloaded[label] {
				continue
			}
			q.label, q.hop = label, hop != ""
			q.base = "redir-" + st.Kind
			q.site = labelSite(q.label, q.hop)
			q.other = true
			qs = append(qs, q)
		}
		errs = append(errs, linkRedirects("redirects2", qs, previousHop))
		for _, q := range qs {
			st := steps[q.label]
			if q.method == "OPTIONS" {
				// A hop's preflight is a request of its own (resolve).
				q.hop, q.crossOriginRedirect, q.crossSiteRedirect, q.originBefore, q.methodChanged, q.insecureBefore = false, false, false, false, false, false
			}
			if st.Nav() && !q.hop && q.method == "GET" {
				continue
			}
			out = append(out, q)
		}
	}

	return out, errors.Join(errs...)
}

// previousHop is the path of the request whose redirect a .hop<n> request
// follows: .hop<n-1>, or for .hop1 its label's.
func previousHop(q *request) string {
	path, n, _ := strings.Cut(q.path, ".hop")
	if n == "1" {
		return path
	}
	i, err := strconv.Atoi(n)
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%s.hop%d", path, i-1)
}

// navRedirectBase is the base of the nav-redirect pages' requests.
const navRedirectBase = "nav-redirect"

// sampleGap is a request of one sample of a capture: its scenario, the
// sample (from 1) and its path.
type sampleGap struct {
	scenario string
	sample   int
	path     string
}

// sampleGaps are, per profile, requests left out of the derivation: forms
// sent in one sample that no condition explains (browser state) while the
// other samples agree. Inheriting profiles leave them out too.
var sampleGaps = map[string]map[sampleGap]string{
	"safari-27-macos": {
		{scenario: "nav-redirect-page", sample: 1, path: "/p/nav-302.cs-so.hop1"}: "the run's first sample, on a cold Safari: the hop back to the page's site went on a new connection, " +
			"with Referer after Sec-Fetch-Dest (as on a redirect within a site) and sec-fetch-site cross-site; the other two samples, on the page's connection, " +
			"put Referer first, as on every other redirect to another site, and send same-origin (redirects.navigation_site restart)",
	},
	"safari-26-macos": {
		{scenario: "nav-redirect-page", sample: 1, path: "/p/nav-302.cs-so.hop1"}: "the run's first sample, on a cold Safari: the hop back to the page's site went on a new connection, " +
			"with Referer after Sec-Fetch-Dest (as on a redirect within a site); the other two samples, on the page's connection, " +
			"put Referer first, as on every other redirect to another site",
	},
}

// navRedirectRequests returns the nav-redirect pages' navigations, one
// slice per sample. HTTP/1.1 means non-secure: every captured HTTPS
// request is HTTP/2. A chain whose first request has no Referer is typed:
// relation "none" throughout. Otherwise it takes the label's relation.
// Secure GET first legs, other paths and sampleGaps are left out; each
// sampleGap must exist in its capture.
func navRedirectRequests(p *profile.Profile, pages []scenarioPages) ([]*request, error) {
	var out []*request
	var errs []error
	seen := map[sampleGap]bool{}
	for _, sp := range pages {
		for si, page := range sp.samples {
			qs := h2Requests(page, si, "/p/")
			for _, q := range qs {
				q.label, _, _ = strings.Cut(q.label, "?")
				q.path, _, _ = strings.Cut(q.path, "?")
			}
			for _, r := range page.H1 {
				method, rest, _ := strings.Cut(r.Line, " ")
				target, _, _ := strings.Cut(rest, " ")
				target, _, _ = strings.Cut(target, "?")
				label, ok := strings.CutPrefix(target, "/p/")
				if !ok {
					continue
				}
				host := r.Host
				if h, _, err := net.SplitHostPort(host); err == nil {
					host = h
				}
				qs = append(qs, &request{
					label:  label,
					method: method,
					host:   host,
					path:   target,
					sample: si,
					plain:  true,
					h1:     h1Fields(r.Headers),
				})
			}
			typed := map[string]bool{}
			for _, q := range qs {
				label, hop, _ := strings.Cut(q.label, ".hop")
				q.label, q.hop = label, hop != ""
				q.base = navRedirectBase
				q.other = true
				if !q.hop {
					typed[label] = !slices.ContainsFunc(slices.Concat(q.h2, q.h1), func(h profile.Header) bool {
						return strings.EqualFold(h.Name, "referer")
					})
				}
			}
			errs = append(errs, linkRedirects(navRedirectBase, qs, previousHop))
			for _, q := range qs {
				q.site = labelSite(q.label, q.hop)
				if typed[q.label] {
					q.site = profile.SiteNone
				}
				if !q.hop && !q.plain && q.method == "GET" {
					continue
				}
				gap := sampleGap{
					scenario: sp.scenario,
					sample:   si + 1,
					path:     q.path,
				}
				if _, ok := sampleGaps[capture.Source(p, sp.scenario)][gap]; ok {
					seen[gap] = true
					continue
				}
				out = append(out, q)
			}
		}
	}
	for source, gaps := range sampleGaps {
		for gap := range gaps {
			if capture.Source(p, gap.scenario) == source && !seen[gap] {
				errs = append(errs, fmt.Errorf("%s sample %d: %s is in sampleGaps (%s) but not in the capture: drop it", gap.scenario, gap.sample, gap.path, source))
			}
		}
	}

	return out, errors.Join(errs...)
}

// headersRequests returns the headers-order page's fetches and XHRs with
// page-set headers (server.HeadersSteps), base hdr-fetch or hdr-xhr.
// OPTIONS belong to the preflight kind.
func headersRequests(samples []*capture.Page) ([]*request, error) {
	steps := map[string]server.HeadersStep{}
	for _, st := range server.HeadersSteps() {
		steps[st.Label] = st
	}
	var out []*request
	var errs []error
	add := func(q *request, si int) {
		st, ok := steps[q.label]
		if !ok {
			errs = append(errs, fmt.Errorf("headers-order sample %d: %s is no step", si+1, q.label))
			return
		}
		target := q.label[strings.LastIndex(q.label, ".")+1:]
		q.other = true
		q.base = "hdr-" + st.API
		q.site = map[string]string{"so": profile.SiteSameOrigin, "cs": profile.SiteCrossSite, "h1": profile.SiteSameSite}[target]
		if st.API == "xhr" {
			for _, h := range st.Headers {
				if name := strings.ToLower(h[0]); !slices.Contains(q.pageOrder, name) {
					q.pageOrder = append(q.pageOrder, name)
				}
			}
		}
		out = append(out, q)
	}
	for si, page := range samples {
		for _, q := range h2Requests(page, si, "/h/") {
			if q.method != "OPTIONS" {
				add(q, si)
			}
		}
		for _, r := range page.H1 {
			method, rest, _ := strings.Cut(r.Line, " ")
			target, _, _ := strings.Cut(rest, " ")
			label, ok := strings.CutPrefix(target, "/h/")
			if !ok || method == "OPTIONS" {
				continue
			}
			add(&request{
				label:  label,
				method: method,
				sample: si,
				h1:     h1Fields(r.Headers),
			}, si)
		}
	}

	return out, errors.Join(errs...)
}

// headerValue is the value of the first field named name in hs.
func headerValue(hs []profile.Header, name string) (string, bool) {
	for _, h := range hs {
		if h.Name == name {
			return h.Value, true
		}
	}

	return "", false
}

// contextHeaders depend on the request, never on the kind: always slots.
var contextHeaders = []string{
	"referer", "origin", "cookie", "sec-fetch-site", "sec-fetch-storage-access",
	"content-type", "content-length", "host",
	// WebSocket key, protocols and extensions vary per handshake.
	"sec-websocket-key", "sec-websocket-protocol", "sec-websocket-extensions",
	"access-control-request-method", "access-control-request-headers",
}

// pageSetNames are the headers the kinds, cors and headers-order pages set
// themselves (fetch() and XHR headers), lowercase, but Content-Type, which
// being set never makes a request CORS-checked.
var pageSetNames = func() map[string]bool {
	out := map[string]bool{}
	for _, st := range server.KindsSteps() {
		for k := range st.Headers {
			out[strings.ToLower(k)] = true
		}
	}
	for _, st := range server.CORSSteps() {
		for _, h := range st.Headers {
			out[strings.ToLower(h[0])] = true
		}
	}
	for _, st := range server.HeadersSteps() {
		for _, h := range st.Headers {
			out[strings.ToLower(h[0])] = true
		}
	}
	delete(out, "content-type")

	return out
}()

// isPageSet reports whether name is a header the page set itself.
func isPageSet(name string) bool {
	return pageSetNames[strings.ToLower(name)]
}

// kindSource says which captured requests describe a kind: own gives
// defaults and weight, all (its resource type) the header order, high and
// low the fetchpriority variants. Only preflight kinds take OPTIONS.
type kindSource struct {
	kind      string
	own, all  []string
	high, low string
	preflight bool
}

var kindSources = []kindSource{
	{kind: profile.KindFrame, own: []string{"iframe"}, all: []string{"iframe"}},
	{kind: profile.KindScript, own: []string{"script-blocking"}, all: scripts},
	{kind: profile.KindAsyncScript, own: []string{"script-async"}, all: scripts},
	{kind: profile.KindDeferScript, own: []string{"script-defer"}, all: scripts},
	{kind: profile.KindInjectedScript, own: []string{"script"}, all: scripts},
	{kind: profile.KindPreloadScript, own: []string{"preload-script"}, all: scripts},
	{kind: profile.KindStyle, own: []string{"style-head", "style-fonts"}, all: []string{"style", "style-head", "style-fonts"}},
	{kind: profile.KindImage, own: []string{"image-markup"}, all: images},
	{kind: profile.KindInjectedImage, own: []string{"image"}, all: images, high: "image-high"},
	{kind: profile.KindFont, own: []string{"font-css"}, all: fonts},
	{kind: profile.KindPreloadFont, own: []string{"preload-font"}, all: fonts},
	{kind: profile.KindFetch, own: []string{"fetch"}, all: fetches, high: "fetch-high", low: "fetch-low"},
	{kind: profile.KindXHR, own: []string{"xhr"}, all: []string{"xhr", "hdr-xhr", "redir-" + server.RedirectXHR}},
	{kind: profile.KindBeacon, own: []string{"beacon"}, all: []string{"beacon"}},
	// Preflights: the kinds page's, for headers the page set; the
	// redirects2 page's, for PATCH alone.
	{kind: profile.KindPreflight, own: []string{"fetch-json", "fetch-custom"}, all: []string{"fetch-json", "fetch-custom", "redir-" + server.RedirectFetch}, preflight: true},
	{kind: profile.KindWebSocket, own: []string{wsBase}, all: []string{wsBase}},
}

var (
	// scripts include the redirects2 page's <script crossorigin>.
	scripts = []string{"script", "script-cors", "script-cors-cred", "script-blocking", "script-defer", "script-async", "preload-script", "redir-" + server.RedirectScript}
	// images include the cookies page's (cross-site, redirected to the
	// page's site) and the redirects2 page's redirected ones.
	images = []string{"image", "image-cors", "image-high", "image-markup", "image-setcookie", "image-redirect", "c-setcookie", "c-order", "c-redirect", "c-part", "c-third",
		"redir-" + server.RedirectImageMarkup, "redir-" + server.RedirectImageNew, "redir-" + server.RedirectImage}
	fonts = []string{"font", "font-css", "preload-font"}
	// fetches include the cors, cors-edge, redirects2 and headers-order
	// pages' fetches.
	fetches = []string{"fetch", "fetch-include", "fetch-omit", "fetch-nocors", "fetch-json", "fetch-custom", "fetch-high", "fetch-low", "fetch-redirect",
		"cors-put", "cors-setcookie", "cors-include", "cors-maxage", "cors-default", "cors-redirect", "cors-redirect-target", "cors-cleanup", "cors-multi",
		"cors-edge", "redir-" + server.RedirectFetch, "hdr-fetch"}
	// navigations include typed ones, form POSTs and redirect chains.
	navigations = []string{"form-post", "c-nav-post", "redir-" + server.RedirectForm, "redir-" + server.RedirectNavigate, navRedirectBase}
)

func derive(p *profile.Profile, reqs []*request) error {
	// pick selects TLS or plain requests, preflights or the rest.
	pick := func(bases []string, plain, preflight bool) []*request {
		var out []*request
		for _, r := range reqs {
			if (r.method == "OPTIONS") == preflight && r.plain == plain && slices.Contains(bases, r.base) {
				out = append(out, r)
			}
		}

		return out
	}
	p.Kinds = map[string]*profile.Kind{}
	var errs []error
	all := func(kind string) []*request {
		i := slices.IndexFunc(kindSources, func(s kindSource) bool {
			return s.kind == kind
		})

		return slices.Concat(pick(kindSources[i].all, false, false), pick(kindSources[i].all, true, false))
	}
	table, err := deriveTable(all(profile.KindFetch), all(profile.KindXHR))
	if err != nil {
		errs = append(errs, err)
	}
	for _, src := range kindSources {
		own := pick(src.own, false, src.preflight)
		if len(own) == 0 {
			errs = append(errs, fmt.Errorf("%s: no captured requests", src.kind))
			continue
		}
		k := &profile.Kind{}
		values := func(h1 bool) func(string) string {
			list := (*request).h2Of
			if h1 {
				list = (*request).h1Of
			}

			return func(name string) string {
				return commonValue(own, list, name)
			}
		}
		var t *profile.HeaderTable
		if slices.Contains(tableKinds, src.kind) {
			t = table
		}
		k.Headers, k.HTTP1, k.Variants, err = orders(
			slices.Concat(own, pick(src.own, true, src.preflight)),
			slices.Concat(pick(src.all, false, src.preflight), pick(src.all, true, src.preflight)),
			nil, t, values)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.kind, err))
		}
		if k.Weight, err = weightOf(own); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.kind, err))
		}
		for which, base := range map[**profile.Urgency]string{&k.High: src.high, &k.Low: src.low} {
			if base == "" {
				continue
			}
			u, err := urgencyOf(pick([]string{base}, false, false))
			if err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", src.kind, base, err))
			}
			*which = u
		}
		p.Kinds[src.kind] = k
	}
	ctx, err := contextOf(slices.DeleteFunc(slices.Clone(reqs), func(r *request) bool {
		return r.plain || r.other
	}))
	if err != nil {
		errs = append(errs, err)
	} else {
		ctx.HeaderTable = table
	}
	p.Context = ctx
	navs := slices.Concat(pick(navigations, false, false), pick([]string{"form-post", "page", navRedirectBase}, true, false))
	if err := navigationVariants(p, navs); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// condition is what picks a header order: a profile.Variant's condition.
type condition struct {
	method     string // "" for GET
	site       string // profile.Site*
	cors       bool
	pageSet    bool
	redirected bool
	// Redirect conditions; crossSite applies to navigations only.
	crossOrigin    bool
	crossSite      bool
	originBefore   bool
	methodChanged  bool
	insecureBefore bool
	insecure       bool
}

func (r *request) condition() condition {
	headers := r.h2
	if r.plain || len(r.h2) == 0 {
		// Over HTTP/1.1 only: plain HTTP, or the headers-order page's
		// HTTP/1.1 host.
		headers = r.h1
	}
	c := condition{
		site: r.site,
		pageSet: slices.ContainsFunc(headers, func(h profile.Header) bool {
			return isPageSet(h.Name)
		}),
		redirected:     r.hop,
		crossOrigin:    r.crossOriginRedirect,
		crossSite:      r.crossSiteRedirect && slices.Contains(navigations, r.base),
		originBefore:   r.originBefore,
		methodChanged:  r.methodChanged,
		insecureBefore: r.insecureBefore,
		insecure:       r.plain,
	}
	if r.method == "OPTIONS" {
		// A preflight names the page's headers in
		// Access-Control-Request-Headers (resolve's rule).
		c.pageSet = slices.ContainsFunc(headers, func(h profile.Header) bool {
			return strings.EqualFold(h.Name, "access-control-request-headers")
		})
	}
	c.cors = !r.websocket && (c.pageSet || slices.ContainsFunc(headers, func(h profile.Header) bool {
		return strings.EqualFold(h.Name, "origin")
	}))
	if r.method != "GET" {
		c.method = r.method
	}

	return c
}

func (r *request) h2Of() []profile.Header {
	return r.h2
}

func (r *request) h1Of() []profile.Header {
	return r.h1
}

// orderPair is a header order over each protocol.
type orderPair struct {
	h2, h1 []string
}

// orders derives a kind's default order and its variants. all's requests
// group by condition and merge per group; a group whose type-mates
// disagree keeps the kind's own order. The default is the plain GET's, or
// the commonest condition for kinds never sent so (beacons). A non-nil
// seed replaces the default. With a header table, page-set-header groups
// the table already orders need no variant (tablePlaces).
func orders(own, all []*request, seed *orderPair, table *profile.HeaderTable, values func(h1 bool) func(string) string) (h2, h1 []profile.Header, variants []profile.Variant, err error) {
	var conds []condition
	groups := map[condition][]*request{}
	mine := map[condition][]*request{}
	for _, r := range all {
		c := r.condition()
		if groups[c] == nil {
			conds = append(conds, c)
		}
		groups[c] = append(groups[c], r)
		if slices.Contains(own, r) {
			mine[c] = append(mine[c], r)
		}
	}
	def := condition{}
	if groups[def] == nil {
		for _, c := range conds {
			if !c.insecure && len(mine[c]) > len(mine[def]) {
				def = c
			}
		}
	}
	orderFor := func(c condition) (orderPair, error) {
		var o orderPair
		for _, p := range []struct {
			list func(*request) []profile.Header
			h1   bool
			out  *[]string
		}{
			{list: (*request).h2Of, out: &o.h2},
			{list: (*request).h1Of, h1: true, out: &o.h1},
		} {
			order, err := orderOf(groups[c], p.list, p.h1)
			if err != nil && len(mine[c]) > 0 {
				order, err = orderOf(mine[c], p.list, p.h1)
			}
			if err != nil {
				return o, fmt.Errorf("%+v over %s: %w", c, map[bool]string{false: "HTTP/2", true: "HTTP/1.1"}[p.h1], err)
			}
			*p.out = order
		}

		return o, nil
	}
	// fold merges b into a when both orders agree; an empty side agrees.
	fold := func(a, b []string) ([]string, bool) {
		if len(a) == 0 || len(b) == 0 {
			return append(a, b...), true
		}
		m, err := mergeOrder([][]string{a, b})

		return m, err == nil
	}
	base, err := orderFor(def)
	if err != nil {
		return nil, nil, nil, err
	}
	if seed != nil {
		def, base = condition{method: "seed"}, *seed
	}
	var errs []error
	var conflicting []condition
	others := map[condition]orderPair{}
	for _, c := range conds {
		if c == def {
			continue
		}
		if table != nil && c.pageSet {
			placed, err := tablePlaces(table, groups, groups[c])
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if placed {
				continue
			}
			// The table can't derive this order: keep the captured order
			// with a slot. Never folded, or the slot would join the default.
			o, err := orderFor(c)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			conflicting = append(conflicting, c)
			others[c] = o

			continue
		}
		o, err := orderFor(c)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		switch {
		case c.insecure:
			// Non-secure lists are kept exactly as sent (no secure-only
			// headers, own Accept-Encoding). Always a variant.
		case seed != nil:
			// The seed stays as measured; any differing group is a variant.
			if slices.Equal(base.h2, o.h2) && slices.Equal(base.h1, o.h1) {
				continue
			}
		case !c.redirected:
			// A redirect's order is never folded: its lists may rightly
			// lack headers the kind sends otherwise (Safari's hops).
			h2, ok2 := fold(slices.Clone(base.h2), o.h2)
			h1, ok1 := fold(slices.Clone(base.h1), o.h1)
			if ok2 && ok1 {
				base.h2, base.h1 = h2, h1
				continue
			}
		}
		conflicting = append(conflicting, c)
		others[c] = o
	}
	ownPlain := slices.DeleteFunc(slices.Clone(own), func(r *request) bool {
		return !r.plain
	})
	for _, c := range conflicting {
		v := profile.Variant{
			Method:                  c.method,
			Site:                    c.site,
			CORSChecked:             c.cors,
			PageSet:                 c.pageSet,
			Redirected:              c.redirected,
			Insecure:                c.insecure,
			CrossOriginRedirect:     c.crossOrigin,
			CrossSiteRedirect:       c.crossSite,
			OriginBeforeRedirect:    c.originBefore,
			MethodChangedByRedirect: c.methodChanged,
			InsecureBeforeRedirect:  c.insecureBefore,
			Headers:                 fill(others[c].h2, values(false)),
			HTTP1:                   fill(others[c].h1, values(true)),
		}
		if c.insecure {
			v.HTTP1 = fill(others[c].h1, insecureValue(mine[c], ownPlain, groups[c]))
		}
		variants = append(variants, v)
	}

	return fill(base.h2, values(false)), fill(base.h1, values(true)), variants, errors.Join(errs...)
}

// insecureValue gives a non-secure variant's values from the plain
// requests: the kind's own, preferring the variant's condition; type-mates'
// only for headers the kind never sends over plain HTTP.
func insecureValue(mine, ownPlain, group []*request) func(string) string {
	return func(name string) string {
		if len(mine) > 0 {
			return commonValue(mine, (*request).h1Of, name)
		}
		if slices.ContainsFunc(ownPlain, func(r *request) bool {
			_, ok := headerValue(r.h1, name)
			return ok
		}) {
			return commonValue(ownPlain, (*request).h1Of, name)
		}

		return commonValue(group, (*request).h1Of, name)
	}
}

// orderOf merges the header orders of rs. HTTP/1.1 names keep their
// casing; the page's own headers, next to each other, become one
// profile.OtherHeaders (apart, the merge refuses them).
func orderOf(rs []*request, list func(*request) []profile.Header, h1 bool) ([]string, error) {
	var seqs [][]string
	for _, r := range rs {
		var seq []string
		for _, h := range list(r) {
			switch {
			case isPageSet(h.Name):
				if len(seq) == 0 || seq[len(seq)-1] != profile.OtherHeaders {
					seq = append(seq, profile.OtherHeaders)
				}
			case h1:
				seq = append(seq, h.Name)
			default:
				seq = append(seq, strings.ToLower(h.Name))
			}
		}
		if len(seq) > 0 {
			seqs = append(seqs, seq)
		}
	}
	if len(seqs) == 0 {
		return nil, nil
	}

	return mergeOrder(seqs)
}

// fill turns an order into a header list: context headers and the
// OtherHeaders marker are slots, the rest take value(name).
func fill(order []string, value func(string) string) []profile.Header {
	if len(order) == 0 {
		return nil
	}
	out := make([]profile.Header, 0, len(order))
	for _, name := range order {
		h := profile.Header{
			Name: name,
		}
		if name != profile.OtherHeaders && !slices.Contains(contextHeaders, strings.ToLower(name)) {
			h.Value = value(name)
		}
		out = append(out, h)
	}

	return out
}

// commonValue is the value every request in own sends for name, or "" when
// one omits it or they differ. Requests not captured over list's protocol
// are left out.
func commonValue(own []*request, list func(*request) []profile.Header, name string) string {
	value, first := "", true
	for _, r := range own {
		if len(list(r)) == 0 {
			continue // no capture over this protocol
		}
		got, found := headerValue(list(r), name)
		if !found || (!first && got != value) {
			return ""
		}
		value, first = got, false
	}

	return value
}

// mergeOrder orders names so that every sequence's order holds, breaking
// ties by first appearance; an order no sequence agrees on is an error.
func mergeOrder(seqs [][]string) ([]string, error) {
	var names []string
	after := map[string]map[string]bool{} // name -> names that must come after it
	before := map[string]int{}            // name -> how many must come before it
	for _, seq := range seqs {
		for i, n := range seq {
			if !slices.Contains(names, n) {
				names = append(names, n)
				after[n] = map[string]bool{}
			}
			if i > 0 && !after[seq[i-1]][n] {
				after[seq[i-1]][n] = true
				before[n]++
			}
		}
	}
	var out []string
	done := map[string]bool{}
	for len(out) < len(names) {
		next := ""
		for _, n := range names {
			if !done[n] && before[n] == 0 {
				next = n
				break
			}
		}
		if next == "" {
			var stuck []string
			for _, n := range names {
				if !done[n] {
					stuck = append(stuck, n)
				}
			}

			return nil, fmt.Errorf("the captures order %v inconsistently", stuck)
		}
		done[next] = true
		out = append(out, next)
		for n := range after[next] {
			before[n]--
		}
	}

	return out, nil
}

func weightOf(own []*request) (uint8, error) {
	var w uint8
	for i, r := range own {
		if i > 0 && r.weight != w {
			return 0, fmt.Errorf("weights differ: %d and %d", w, r.weight)
		}
		w = r.weight
	}

	return w, nil
}

func urgencyOf(reqs []*request) (*profile.Urgency, error) {
	if len(reqs) == 0 {
		return nil, errors.New("no captured requests")
	}
	w, err := weightOf(reqs)
	if err != nil {
		return nil, err
	}
	u := &profile.Urgency{
		Weight:   w,
		Priority: commonValue(reqs, (*request).h2Of, "priority"),
	}

	return u, nil
}

// contextOf derives the profile's context rules from the kinds page's
// requests that show them, in every sample; samples that show different
// rules are an error.
func contextOf(reqs []*request) (*profile.Context, error) {
	var samples []int
	for _, r := range reqs {
		if !slices.Contains(samples, r.sample) {
			samples = append(samples, r.sample)
		}
	}
	slices.Sort(samples)
	if len(samples) == 0 {
		return nil, errors.New("context: no captured requests")
	}
	var out *profile.Context
	for _, s := range samples {
		c, err := sampleContext(reqs, s)
		if err != nil {
			return nil, fmt.Errorf("context, sample %d: %w", s+1, err)
		}
		if out != nil && *c != *out {
			return nil, fmt.Errorf("context: samples disagree (%+v in sample %d, %+v in sample %d)", *out, samples[0]+1, *c, s+1)
		}
		out = c
	}

	return out, nil
}

// sampleContext derives the context rules from one sample's requests.
func sampleContext(reqs []*request, sample int) (*profile.Context, error) {
	find := func(label string) *request {
		for _, r := range reqs {
			if r.label == label && r.method == "GET" && !r.hop && r.sample == sample {
				return r
			}
		}

		return nil
	}
	c := &profile.Context{}
	var access []string
	for _, r := range reqs {
		if v, ok := headerValue(r.h2, "sec-fetch-storage-access"); ok && r.sample == sample && !slices.Contains(access, v) {
			access = append(access, v)
		}
	}
	switch len(access) {
	case 0:
	case 1:
		c.StorageAccess = access[0]
	default:
		return nil, fmt.Errorf("sec-fetch-storage-access takes several values: %q", access)
	}
	corsSO := find("image-cors.so")
	fetchSO, omitSO := find("fetch.so"), find("fetch-omit.so")
	frameCS, scriptCS := find("iframe.cs"), find("script.cs")
	cssFont := find("font-css.so")
	if corsSO == nil || fetchSO == nil || omitSO == nil || frameCS == nil || scriptCS == nil || cssFont == nil {
		return nil, errors.New("the captures lack the requests that show it")
	}
	_, c.SameOriginElementOrigin = headerValue(corsSO.h2, "origin")
	ref, _ := headerValue(cssFont.h2, "referer")
	c.StylesheetReferrer = strings.HasSuffix(ref, "/k/style-fonts.so")
	c.AnonymousConnections = omitSO.conn != fetchSO.conn
	c.FrameConnections = frameCS.conn != scriptCS.conn

	return c, nil
}

// navigationVariants sets the navigation's variants from the captured form
// POSTs: their orders, with the navigation's values, and the POSTs' own for
// headers only they send. navs may hold plain-HTTP navigations too (the
// page and its form POST), which become non-secure variants.
func navigationVariants(p *profile.Profile, navs []*request) error {
	secure := slices.DeleteFunc(slices.Clone(navs), func(r *request) bool {
		return r.plain
	})
	posts := slices.DeleteFunc(slices.Clone(secure), func(r *request) bool {
		return r.method != "POST"
	})
	if len(posts) == 0 {
		return errors.New("navigation: no captured form POST")
	}
	// Headers the navigation lacks take the form POSTs' agreed value, else
	// other navigations' (Upgrade-Insecure-Requests on an http:// hop).
	values := func(h1 bool) func(string) string {
		base, list := p.Headers, (*request).h2Of
		if h1 {
			base, list = nil, (*request).h1Of
			if p.HTTP1 != nil {
				base = p.HTTP1.Headers
			}
		}

		return func(name string) string {
			if v, ok := headerValue(base, name); ok {
				return v
			}
			for _, set := range [][]*request{posts, secure} {
				sending := slices.DeleteFunc(slices.Clone(set), func(r *request) bool {
					_, ok := headerValue(list(r), name)
					return !ok
				})
				if len(sending) > 0 {
					return commonValue(sending, list, name)
				}
			}

			return ""
		}
	}
	seed := &orderPair{
		h2: names(p.Headers),
	}
	if p.HTTP1 != nil {
		seed.h1 = names(p.HTTP1.Headers)
	}
	_, _, variants, err := orders(nil, navs, seed, nil, values)
	if err != nil {
		return fmt.Errorf("navigation: %w", err)
	}
	p.Variants = variants

	return nil
}

func names(hs []profile.Header) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Name
	}

	return out
}

// preflightCache derives profile.Context.PreflightCache from the cors page:
// after an X-Other preflight to maxage.cs, is the cached X-Mimicry request
// preflighted again (replace) or not (merge)? All samples must agree, each
// on one connection.
func preflightCache(pages []*capture.Page) (string, error) {
	var found string
	for i, page := range pages {
		var seq []string
		conns := 0
		for _, tr := range page.H2 {
			reqs := slices.Clone(tr.Requests)
			slices.SortFunc(reqs, func(a, b h2trace.Request) int {
				return int(a.Stream) - int(b.Stream)
			})
			n := len(seq)
			for _, r := range reqs {
				if r.Path != "/p/maxage.cs" {
					continue
				}
				method, acrh := "", ""
				for _, ins := range r.Instructions {
					switch ins.Name {
					case ":method":
						method = ins.Value
					case "access-control-request-headers":
						acrh = ins.Value
					}
				}
				seq = append(seq, method+" "+acrh)
			}
			if len(seq) > n {
				conns++
			}
		}
		if conns > 1 {
			return "", fmt.Errorf("preflight cache: cors sample %d sends maxage.cs on %d connections, in no known order", i+1, conns)
		}
		at := slices.Index(seq, "OPTIONS x-other")
		if at < 0 || at+2 >= len(seq) || seq[at+1] != "GET " {
			return "", fmt.Errorf("preflight cache: cors sample %d lacks the requests that show it: %q", i+1, seq)
		}
		// seq[at+1] is the X-Other request; then the X-Mimicry one again,
		// preflighted or not.
		var got string
		switch seq[at+2] {
		case "OPTIONS x-mimicry":
			got = profile.PreflightCacheReplace
		case "GET ":
			got = profile.PreflightCacheMerge
		default:
			return "", fmt.Errorf("preflight cache: cors sample %d sends %q after the X-Other request: %q", i+1, seq[at+2], seq)
		}
		if found != "" && got != found {
			return "", fmt.Errorf("preflight cache: cors samples disagree (%s, %s)", found, got)
		}
		found = got
	}
	if found == "" {
		return "", errors.New("preflight cache: no cors samples")
	}

	return found, nil
}
