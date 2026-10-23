// Command kindsgen derives a profile's request kinds and context rules from
// its browser's kinds captures (the kinds and kinds-http1 scenarios), and
// writes them into the profile:
//
//	go run ./cmd/kindsgen -captures testdata/captures -profile ../profiles/data/chrome-155-macos.json
//
// For each kind, the header order merges every captured request of its
// resource type (so slots seen only on some, like origin with a crossorigin
// attribute, keep their place); default values and the HTTP/2 weight come
// from the kind's own requests, a header sent by all of them with one value
// becoming a default and any other a slot. The navigation's lists gain the
// slots of the captured form POST. It refuses to write when the captures
// disagree (two orders that cannot be merged, weights that differ).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kindsgen:", err)
		os.Exit(1)
	}
}

func run() error {
	captures := flag.String("captures", "testdata/captures", "directory with <name>.kinds.pagetrace.json.gz and <name>.kinds-http1.pagetrace.json.gz")
	path := flag.String("profile", "", "profile JSON to update (required); its name picks the captures")
	flag.Parse()
	if *path == "" {
		flag.Usage()
		os.Exit(2)
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	p, err := profile.Parse(data)
	if err != nil {
		return err
	}
	h2, err := capture.LoadPages(filepath.Join(*captures, p.Name+".kinds.pagetrace.json.gz"))
	if err != nil {
		return err
	}
	h1, err := capture.LoadPages(filepath.Join(*captures, p.Name+".kinds-http1.pagetrace.json.gz"))
	if err != nil {
		return err
	}
	reqs := collect(h2, h1)
	if err := derive(p, reqs); err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	out, err := profile.Encode(p)
	if err != nil {
		return err
	}
	return os.WriteFile(*path, out, 0o600) //nolint:gosec // rewriting the profile named on the command line is the point
}

// request is one captured request of the kinds page.
type request struct {
	// label is its step ("script-cors.cs"), base the part before the
	// first dot ("script-cors").
	label, base string
	method      string
	hop         bool // the request after a redirect
	// h2 are its HTTP/2 header fields in order (cookie crumbs as one
	// cookie), h1 its HTTP/1.1 lines; weight its HEADERS priority weight;
	// conn its connection (trace index).
	h2     []profile.Header
	h1     []profile.Header
	weight uint8
	conn   int
	sample int
}

// collect gathers the kinds page's requests from every sample of both
// captures.
func collect(h2Pages, h1Pages []*capture.Page) []*request {
	var out []*request
	byKey := map[string]*request{}
	key := func(sample int, label, method string) string {
		return fmt.Sprint(sample, " ", method, " ", label)
	}
	for si, page := range h2Pages {
		for ci, tr := range page.H2 {
			weights := map[uint32]uint8{}
			for _, f := range tr.Frames {
				if f.Type == "HEADERS" && f.Priority != nil {
					weights[f.Stream] = f.Priority.Weight
				}
			}
			for _, r := range tr.Requests {
				label, ok := strings.CutPrefix(r.Path, "/k/")
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
				byKey[key(si, label, q.method)] = q
			}
		}
	}
	for si, page := range h1Pages {
		for _, r := range page.H1 {
			method, rest, _ := strings.Cut(r.Line, " ")
			target, _, _ := strings.Cut(rest, " ")
			label, ok := strings.CutPrefix(target, "/k/")
			if !ok {
				continue
			}
			q := byKey[key(si, label, method)]
			if q == nil {
				continue
			}
			for _, l := range r.Headers {
				name, value, _ := strings.Cut(l, ":")
				q.h1 = append(q.h1, profile.Header{
					Name:  name,
					Value: strings.TrimSpace(value),
				})
			}
		}
	}
	return out
}

// contextHeaders depend on the request, never on the kind: always slots.
var contextHeaders = []string{
	"referer", "origin", "cookie", "sec-fetch-site", "sec-fetch-storage-access",
	"content-type", "content-length", "host",
}

// customHeader is the header the kinds page sets itself on a fetch(): where
// it lands is where every page-set header goes (profile.OtherHeaders).
const customHeader = "x-mimicry"

// kindSources says which captured requests describe each kind: own give
// its defaults and weight, all (its resource type) the header order; high
// and low the fetchpriority variants where measured.
var kindSources = []struct {
	kind      string
	own, all  []string
	high, low string
}{
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
	{kind: profile.KindXHR, own: []string{"xhr"}, all: []string{"xhr"}},
	{kind: profile.KindBeacon, own: []string{"beacon"}, all: []string{"beacon"}},
}

var (
	scripts = []string{"script", "script-cors", "script-cors-cred", "script-blocking", "script-defer", "script-async", "preload-script"}
	images  = []string{"image", "image-cors", "image-high", "image-markup", "image-setcookie", "image-redirect"}
	fonts   = []string{"font", "font-css", "preload-font"}
	fetches = []string{"fetch", "fetch-include", "fetch-omit", "fetch-nocors", "fetch-json", "fetch-custom", "fetch-high", "fetch-low", "fetch-redirect"}
)

func derive(p *profile.Profile, reqs []*request) error {
	pick := func(bases []string) []*request {
		var out []*request
		for _, r := range reqs {
			if !r.hop && r.method != "OPTIONS" && slices.Contains(bases, r.base) {
				out = append(out, r)
			}
		}
		return out
	}
	p.Kinds = map[string]*profile.Kind{}
	var errs []error
	for _, src := range kindSources {
		own, all := pick(src.own), pick(src.all)
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
		var err error
		k.Headers, k.HTTP1, k.Variants, err = orders(own, all, nil, values)
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
			u, err := urgencyOf(pick([]string{base}))
			if err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", src.kind, base, err))
			}
			*which = u
		}
		p.Kinds[src.kind] = k
	}
	ctx, err := contextOf(reqs)
	if err != nil {
		errs = append(errs, err)
	}
	p.Context = ctx
	if err := navigationVariants(p, pick([]string{"form-post"})); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// condition is what picks a header order: a profile.Variant's condition.
type condition struct {
	method string // "" for GET
	site   string // profile.Site*
	cors   bool
}

func (r *request) condition() condition {
	c := condition{
		cors: slices.ContainsFunc(r.h2, func(h profile.Header) bool {
			return h.Name == "origin" || h.Name == customHeader
		}),
	}
	if r.method != "GET" {
		c.method = r.method
	}
	// Labels end in their target: so (same origin), ss (same site), cs
	// (cross site); a redirect's first leg in its first ("so-cs").
	_, target, _ := strings.Cut(r.label, ".")
	switch {
	case strings.HasPrefix(target, "ss"):
		c.site = profile.SiteSameSite
	case strings.HasPrefix(target, "cs"):
		c.site = profile.SiteCrossSite
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
// instead, every group checked against it. values gives a header's default
// value.
func orders(own, all []*request, seed *orderPair, values func(h1 bool) func(string) string) (h2, h1 []profile.Header, variants []profile.Variant, err error) {
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
			if len(mine[c]) > len(mine[def]) {
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
		o, err := orderFor(c)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if seed != nil {
			// The seed stays as measured: any group that differs from it
			// is a variant, with the headers only it sends.
			if slices.Equal(base.h2, o.h2) && slices.Equal(base.h1, o.h1) {
				continue
			}
		} else {
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
	for _, c := range conflicting {
		variants = append(variants, profile.Variant{
			Method:      c.method,
			Site:        c.site,
			CORSChecked: c.cors,
			Headers:     fill(others[c].h2, values(false)),
			HTTP1:       fill(others[c].h1, values(true)),
		})
	}
	return fill(base.h2, values(false)), fill(base.h1, values(true)), variants, errors.Join(errs...)
}

// orderOf merges the header orders of rs. HTTP/1.1 names keep their
// casing; the page's own header becomes profile.OtherHeaders.
func orderOf(rs []*request, list func(*request) []profile.Header, h1 bool) ([]string, error) {
	var seqs [][]string
	for _, r := range rs {
		var seq []string
		for _, h := range list(r) {
			switch {
			case strings.EqualFold(h.Name, customHeader):
				seq = append(seq, profile.OtherHeaders)
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
// one omits it or they differ.
func commonValue(own []*request, list func(*request) []profile.Header, name string) string {
	value, first := "", true
	for _, r := range own {
		got, found := "", false
		for _, h := range list(r) {
			if h.Name == name {
				got, found = h.Value, true
				break
			}
		}
		if len(list(r)) == 0 {
			continue // no capture over this protocol
		}
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

// contextOf derives the profile's context rules from the requests that
// show them.
func contextOf(reqs []*request) (*profile.Context, error) {
	// The first sample's GET of label (after its redirect when hop).
	find := func(label string, hop bool) *request {
		for _, r := range reqs {
			if r.label == label && r.method == "GET" && r.hop == hop && r.sample == 0 {
				return r
			}
		}
		return nil
	}
	has := func(r *request, name string) (string, bool) {
		for _, h := range r.h2 {
			if h.Name == name {
				return h.Value, true
			}
		}
		return "", false
	}
	c := &profile.Context{}
	for _, r := range reqs {
		if v, ok := has(r, "sec-fetch-storage-access"); ok {
			c.StorageAccess = v
			break
		}
	}
	corsSO := find("image-cors.so", false)
	fetchSO, omitSO := find("fetch.so", false), find("fetch-omit.so", false)
	frameCS, scriptCS := find("iframe.cs", false), find("script.cs", false)
	leg, hop := find("fetch-redirect.so-cs", false), find("fetch-redirect.so-cs", true)
	cssFont := find("font-css.so", false)
	if corsSO == nil || fetchSO == nil || omitSO == nil || frameCS == nil || scriptCS == nil || leg == nil || hop == nil || cssFont == nil {
		return nil, errors.New("context: the captures lack the requests that show it")
	}
	_, c.SameOriginElementOrigin = has(corsSO, "origin")
	ref, _ := has(cssFont, "referer")
	c.StylesheetReferrer = strings.HasSuffix(ref, "/k/style-fonts.so")
	c.AnonymousConnections = omitSO.conn != fetchSO.conn
	c.FrameConnections = frameCS.conn != scriptCS.conn
	for _, h := range leg.h2 {
		if slices.Contains(contextHeaders, h.Name) {
			continue
		}
		if _, ok := has(hop, h.Name); !ok {
			c.FetchRedirectDrops = append(c.FetchRedirectDrops, h.Name)
		}
	}
	return c, nil
}

// navigationVariants sets the navigation's variants from the captured form
// POSTs: their orders, with the navigation's values, and the POSTs' own for
// headers only they send.
func navigationVariants(p *profile.Profile, posts []*request) error {
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
			for _, h := range base {
				if h.Name == name {
					return h.Value
				}
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
	_, _, variants, err := orders(nil, posts, seed, values)
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
