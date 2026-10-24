package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// pages are a profile's captures kindsgen reads, each a page per sample.
type pages struct {
	// kinds is the kinds page over HTTP/2, kindsH1 over HTTP/1.1 and
	// plain over plain HTTP (kinds, kinds-http1, kinds-plain).
	kinds, kindsH1, plain []*capture.Page
	// cookies is the cookies page (HTTP/2), cors the cors page, corsEdge
	// the cors-edge page (HTTP/2).
	cookies, cors, corsEdge []*capture.Page
	// headers is the headers-order page: fetch() and XHR with headers the
	// page set, over HTTP/2 (so, cs) and HTTP/1.1 (h1).
	headers []*capture.Page
}

// loadPages reads profile name's captures from dir.
func loadPages(dir, name string) (*pages, error) {
	out := &pages{}
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
		{scenario: "headers-order", into: &out.headers},
	} {
		got, err := capture.LoadPages(filepath.Join(dir, name+"."+f.scenario+".pagetrace.json.gz"))
		if err != nil {
			return nil, err
		}
		*f.into = got
	}
	return out, nil
}

// request is one captured request of the kinds, cookies, cors, cors-edge or
// headers-order page.
type request struct {
	// label is its step ("script-cors.cs"), base the part before the
	// first dot ("script-cors").
	label, base string
	method      string
	hop         bool // the request after a redirect
	// plain is a request over plain HTTP (kinds-plain): HTTP/1.1 only,
	// and to a non-secure origin.
	plain bool
	// other is a request of another page than the kinds page: over one
	// protocol only.
	other bool
	// site is the request URL's own relation to its page
	// (profile.Site*).
	site string
	// h2 are its HTTP/2 header fields in order (cookie crumbs as one
	// cookie), h1 its HTTP/1.1 lines; weight its HEADERS priority weight;
	// conn its connection (trace index).
	h2     []profile.Header
	h1     []profile.Header
	weight uint8
	conn   int
	sample int
	// pageOrder are the page's headers, lowercase, in the order the page
	// set them where that is the order they reach the browser (XHR's
	// setRequestHeader); nil: by name (fetch()'s Headers object).
	pageOrder []string
}

// labelSite is the site relation a kinds page label names: labels end in
// their target, so (same origin), ss (same site) or cs (cross site); a
// redirect's in its legs' ("so-cs"), the first leg going to the first, the
// hop to the second. The kinds page is on the so host, and so is the
// cookies page that follows redirects.
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
				conn:   ci,
				weight: weights[r.Stream],
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
	for si, page := range pg.kinds {
		for _, q := range h2Requests(page, si, "/k/") {
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
				sample: si,
				plain:  true,
				h1:     h1Fields(r.Headers),
			}
			q.label, q.hop = strings.CutSuffix(label, ".hop")
			q.base, _, _ = strings.Cut(q.label, ".")
			q.site = labelSite(q.label, q.hop)
			out = append(out, q)
		}
	}
	for si, page := range pg.cookies {
		for _, q := range h2Requests(page, si, "/k/") {
			q.other = true
			// The cookies page's requests come from pages on several
			// sites, so a label names its host, not its relation; but
			// every redirect starts on the so page, and only a redirect
			// makes sec-fetch-site (the chain's) differ from the URL's.
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
		for _, q := range h2Requests(page, si, "/p/") {
			// The OPTIONS are the preflight kind's.
			if q.method == "OPTIONS" {
				continue
			}
			q.other = true
			// Every fetch goes to the cross-site host, its label naming
			// it; a label no step has is a redirect's target.
			q.hop = !steps[q.label]
			q.base = "cors-" + q.base
			q.site = labelSite(q.label, q.hop)
			out = append(out, q)
		}
	}
	out = append(out, corsEdgeRequests(pg.corsEdge)...)
	hdrs, err := headersRequests(pg.headers)
	if err != nil {
		errs = append(errs, err)
	}
	out = append(out, hdrs...)
	return out, errors.Join(errs...)
}

// corsEdgeRequests returns the cors-edge page's fetches (HTTP/2, all fetch()
// in CORS mode from the so page): /p/<label> and its redirects'
// /p/<label>.hop<n>, each hop on its label's host (so or cs), but the GET
// hops. Those share a condition with the kinds and cors pages' redirects
// yet order otherwise: Chrome places Origin by whether the first leg
// carried it, Safari reorders every header on a redirect, so their orders
// cannot merge into one list per condition. The OPTIONS are the preflight
// kind's, which the kinds page describes.
func corsEdgeRequests(samples []*capture.Page) []*request {
	var out []*request
	for si, page := range samples {
		for _, q := range h2Requests(page, si, "/p/") {
			label, hop, _ := strings.Cut(q.label, ".hop")
			if q.method == "OPTIONS" || (hop != "" && q.method == "GET") {
				continue
			}
			q.other = true
			q.label, q.hop = label, hop != ""
			q.base = "cors-edge"
			// A hop stays on its label's host: its own relation is the
			// label's, as is the chain's.
			q.site = labelSite(q.label, false)
			out = append(out, q)
		}
	}
	return out
}

// headersRequests returns the headers-order page's fetches and XHRs, each
// carrying headers the page set (server.HeadersSteps): over HTTP/2 to the
// page's origin (so) and another site (cs), over HTTP/1.1 to another
// origin of the page's site (h1). Their base is hdr-fetch or hdr-xhr. The
// OPTIONS are the preflight kind's, which the kinds page describes.
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

// kindSource says which captured requests describe a kind: own give its
// defaults and weight, all (its resource type) the header order; high and
// low the fetchpriority variants where measured. A preflight kind takes
// the OPTIONS requests, others never.
type kindSource struct {
	kind      string
	own, all  []string
	high, low string
	preflight bool
}

// kindSources are every kind's sources.
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
	{kind: profile.KindXHR, own: []string{"xhr"}, all: []string{"xhr", "hdr-xhr"}},
	{kind: profile.KindBeacon, own: []string{"beacon"}, all: []string{"beacon"}},
	{kind: profile.KindPreflight, own: []string{"fetch-json", "fetch-custom"}, all: []string{"fetch-json", "fetch-custom"}, preflight: true},
}

var (
	scripts = []string{"script", "script-cors", "script-cors-cred", "script-blocking", "script-defer", "script-async", "preload-script"}
	// images include the cookies page's: injected images, some from
	// another site's page, and redirects to the page's own site.
	images = []string{"image", "image-cors", "image-high", "image-markup", "image-setcookie", "image-redirect", "c-setcookie", "c-order", "c-redirect", "c-part", "c-third"}
	fonts  = []string{"font", "font-css", "preload-font"}
	// fetches include the cors page's (cors-*): PUTs, and fetches with
	// and without a header the page set, to the cross-site host; the
	// cors-edge page's (methods, redirects); and the headers-order page's
	// (hdr-fetch).
	fetches = []string{"fetch", "fetch-include", "fetch-omit", "fetch-nocors", "fetch-json", "fetch-custom", "fetch-high", "fetch-low", "fetch-redirect",
		"cors-put", "cors-setcookie", "cors-include", "cors-maxage", "cors-default", "cors-redirect", "cors-redirect-target", "cors-cleanup", "cors-multi",
		"cors-edge", "hdr-fetch"}
	// formPosts are the form POST navigations: the kinds page's to its own
	// origin, the cookies page's from another site.
	formPosts = []string{"form-post", "c-nav-post"}
)

func derive(p *profile.Profile, reqs []*request) error {
	// pick takes the kinds' requests over TLS (plain false) or plain HTTP,
	// the preflights (OPTIONS) or the rest.
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
	navs := slices.Concat(pick(formPosts, false, false), pick([]string{"form-post", "page"}, true, false))
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
	insecure   bool
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
		redirected: r.hop,
		insecure:   r.plain,
	}
	c.cors = c.pageSet || slices.ContainsFunc(headers, func(h profile.Header) bool {
		return strings.EqualFold(h.Name, "origin")
	})
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
// group by condition, each group's orders merging into one; a group whose
// type-mates disagree keeps the kind's own requests' order. The default is
// the plain GET's, or, for a kind never sent so (beacons POST), its own
// requests' commonest condition. A non-nil seed is the default order
// instead, every group checked against it. With a header table, a group of
// requests carrying the page's headers needs no variant when the table
// places them in the order of their condition without them
// (tablePlaces); it never folds into the default. values gives a header's
// default value.
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
			// The table does not order them from a captured order without
			// the page's headers: the browser's order as captured, the
			// page's at the slot. Never folded: the slot would join the
			// default, which the table orders.
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
			// A non-secure request's lists are exactly what the browser
			// sent: without the headers it sends only to secure origins,
			// with values of their own (Accept-Encoding). Always a variant.
		case seed != nil:
			// The seed stays as measured: any group that differs from it
			// is a variant, with the headers only it sends.
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
	// ownPlain are the kind's own requests over plain HTTP, in any
	// condition.
	ownPlain := slices.DeleteFunc(slices.Clone(own), func(r *request) bool {
		return !r.plain
	})
	for _, c := range conflicting {
		v := profile.Variant{
			Method:      c.method,
			Site:        c.site,
			CORSChecked: c.cors,
			PageSet:     c.pageSet,
			Redirected:  c.redirected,
			Insecure:    c.insecure,
			Headers:     fill(others[c].h2, values(false)),
			HTTP1:       fill(others[c].h1, values(true)),
		}
		if c.insecure {
			v.HTTP1 = fill(others[c].h1, insecureValue(mine[c], ownPlain, groups[c]))
		}
		variants = append(variants, v)
	}
	return fill(base.h2, values(false)), fill(base.h1, values(true)), variants, errors.Join(errs...)
}

// insecureValue gives a non-secure variant's values from the plain
// requests themselves: the kind's own in the variant's condition where
// captured; else the kind's own in any condition; its type-mates' (group)
// only for a header the kind's own plain requests never send.
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
	// The sample's GET of label (the first leg, for a redirect).
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
	posts := slices.DeleteFunc(slices.Clone(navs), func(r *request) bool {
		return r.plain
	})
	if len(posts) == 0 {
		return errors.New("navigation: no captured form POST")
	}
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
			return commonValue(posts, list, name)
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

// preflightCache derives profile.Context.PreflightCache from the cors
// page: after a preflight for another header (X-Other) to maxage.cs and
// its request, the first request (X-Mimicry, preflighted and cached
// before) is preflighted again when the latest answer replaced the URL's
// entry, and not when answers merge. Every sample must show one or the
// other, on one connection (requests on several have no order), and agree.
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
