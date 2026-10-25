package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// navigation keys the navigation among h3Request kinds.
const navigation = "navigation"

// altUsed is the header Firefox adds over HTTP/3 (RFC 7838, section 5).
const altUsed = "alt-used"

// h3PriorityGaps are the h3 pages' requests whose priority header differs
// from their kind's for a reason the profile does not model, by profile
// and path, each with why. A pinned one that matches again is an error.
var h3PriorityGaps = map[string]string{
	"firefox-158-macos /h3/markup-image2.so": "Firefox gave the second page's only image u=4, i (its kind's u=5, i): a layout-driven image priority, a known gap over HTTP/2 too (redirects2)",
}

// h3Request is one request of the h3 captures: its kind (navigation for
// the navigation) and method, its step's fetchpriority, its pseudo-header
// order, its headers in order (names, and fields with their first
// values) and its priority header ("" for none).
type h3Request struct {
	at            string
	path          string
	kind          string
	method        string
	fetchPriority string
	pseudo        []string
	names         []string
	fields        []profile.Header
	priority      string
}

// deriveH3 derives what HTTP/3 changes in p's request kinds from every
// request of the h3 captures (<name>.h3-*.pagetrace.json.gz) sent over
// HTTP/3: http3.headers, and http3.high and http3.low; and the variants
// of a kind's HTTP/2 lists that only the h3 pages show (an XHR POST). An
// error without such requests.
func deriveH3(p *profile.Profile, dir string) error {
	reqs, err := h3Requests(dir, p.Name)
	if err != nil {
		return err
	}
	if len(reqs) == 0 {
		return errors.New("no request over HTTP/3 in the h3 captures")
	}
	if err := h3Variants(p, dir, reqs, h3Drop(p, reqs)); err != nil {
		return err
	}
	h, err := h3Headers(p, reqs)
	if err != nil {
		return fmt.Errorf("http3.headers: %w", err)
	}
	// Lists that are the HTTP/2 ones: no headers section.
	if slices.Equal(h.PseudoHeaderOrder, p.HTTP2.PseudoHeaderOrder) && len(h.Drop) == 0 && h.AltUsed == nil && h.AltUsedNavigation == "" {
		h = nil
	}
	p.HTTP3.Headers = h
	if err := h2Urgencies(p, dir); err != nil {
		return err
	}
	p.HTTP3.High, p.HTTP3.Low, err = h3Priorities(p, reqs)
	return err
}

// h3Drop are the headers every measured kind's list sends with a value
// that no request over HTTP/3 carries (not one only some lists have, such
// as the navigation's sec-fetch-user, which needs user activation the h3
// pages' script navigations lack).
func h3Drop(p *profile.Profile, reqs []h3Request) []string {
	sent := map[string]bool{}
	kinds := map[string]bool{}
	for _, q := range reqs {
		kinds[q.kind] = true
		for _, n := range q.names {
			sent[n] = true
		}
	}
	var out []string
	for _, h := range h2List(p, reqs[0].kind) {
		everywhere := h.Value != "" && !sent[h.Name]
		for kind := range kinds {
			everywhere = everywhere && slices.ContainsFunc(h2List(p, kind), func(o profile.Header) bool {
				return o.Name == h.Name && o.Value != ""
			})
		}
		if everywhere {
			out = append(out, h.Name)
		}
	}
	slices.Sort(out)
	return out
}

// h3Headers derives http3.headers: how a request's header list differs
// from its kind's HTTP/2 list (Kind.Headers; Profile.Headers for the
// navigation). Every request must have one pseudo-header order, send its
// headers in its kind's order (one of its variants'), leave out the same
// headers every kind's list sends with a value (Firefox: te), and add none
// but alt-used, whose place in each kind's list every request of the kind
// must agree on: after every header sent before it, before every header
// sent after it. Nil when the lists are the HTTP/2 ones.
func h3Headers(p *profile.Profile, reqs []h3Request) (*profile.H3Headers, error) {
	var errs []error
	out := &profile.H3Headers{
		PseudoHeaderOrder: reqs[0].pseudo,
	}
	for _, q := range reqs {
		if !slices.Equal(q.pseudo, out.PseudoHeaderOrder) {
			errs = append(errs, fmt.Errorf("%s: pseudo-headers %v, another request's %v", q.at, q.pseudo, out.PseudoHeaderOrder))
		}
	}
	out.Drop = h3Drop(p, reqs)
	anchors := map[string]*anchorSpan{}
	// before and after are the headers some request sent before alt-used,
	// and after it.
	before, after := map[string]bool{}, map[string]bool{}
	for _, q := range reqs {
		i := slices.Index(q.names, altUsed)
		for j, n := range q.names {
			if i >= 0 && j < i {
				before[n] = true
			} else if i >= 0 && j > i {
				after[n] = true
			}
		}
	}
	for _, q := range reqs {
		if err := checkH3Order(p, q, out.Drop); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", q.at, err))
			continue
		}
		if !slices.Contains(q.names, altUsed) {
			continue
		}
		a := anchors[q.kind]
		if a == nil {
			a = &anchorSpan{
				last:  -1,
				first: len(h2List(p, q.kind)),
			}
			anchors[q.kind] = a
		}
		a.add(names(h2List(p, q.kind)), q.names)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	for _, q := range reqs {
		if (anchors[q.kind] != nil) != slices.Contains(q.names, altUsed) {
			errs = append(errs, fmt.Errorf("%s: alt-used sent by some requests of %s only", q.at, q.kind))
		}
	}
	for kind, a := range anchors {
		list := names(h2List(p, kind))
		if a.last >= a.first || a.last+1 >= len(list) {
			errs = append(errs, fmt.Errorf("%s: alt-used goes after %q and before %q in different requests", kind, list[max(a.last, 0)], list[min(a.first, len(list)-1)]))
			continue
		}
		// Between the last header seen before alt-used and the first seen
		// after it, a header no request of the kind sent goes on the side
		// requests of other kinds show it on; page-set headers (*) before,
		// as fetch()'s are; one never sent over HTTP/3 (cookie: the h3
		// pages set none) after.
		at := a.last + 1
		for at < a.first && (list[at] == profile.OtherHeaders || before[list[at]] && !after[list[at]]) {
			at++
		}
		anchor := list[at]
		if kind == navigation {
			out.AltUsedNavigation = anchor
			continue
		}
		if out.AltUsed == nil {
			out.AltUsed = map[string]string{}
		}
		out.AltUsed[kind] = anchor
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// anchorSpan is where alt-used goes in a kind's HTTP/2 list: after the
// header at last, before the one at first, over every request of the kind.
type anchorSpan struct {
	last, first int
}

func (a *anchorSpan) add(list, got []string) {
	i := slices.Index(got, altUsed)
	for j, n := range got {
		pos := slices.Index(list, n)
		if pos < 0 {
			continue
		}
		if j < i {
			a.last = max(a.last, pos)
		} else if j > i {
			a.first = min(a.first, pos)
		}
	}
}

// h2List is a kind's HTTP/2 header list, the navigation's for navigation.
func h2List(p *profile.Profile, kind string) []profile.Header {
	if kind == navigation {
		return p.Headers
	}
	if k := p.Kinds[kind]; k != nil {
		return k.Headers
	}
	return nil
}

// checkH3Order checks that q's headers, alt-used and the dropped ones
// aside, are in its kind's order: the kind's list's or one of its secure
// variants'.
func checkH3Order(p *profile.Profile, q h3Request, drop []string) error {
	got := slices.DeleteFunc(slices.Clone(q.names), func(n string) bool {
		return n == altUsed
	})
	lists := [][]profile.Header{
		h2List(p, q.kind),
	}
	variants := p.Variants
	if q.kind != navigation {
		if k := p.Kinds[q.kind]; k != nil {
			variants = k.Variants
		}
	}
	for _, v := range variants {
		if len(v.Headers) > 0 {
			lists = append(lists, v.Headers)
		}
	}
	if lists[0] == nil {
		return fmt.Errorf("the profile has no kind %s", q.kind)
	}
	for _, list := range lists {
		order := slices.DeleteFunc(names(list), func(n string) bool {
			return slices.Contains(drop, n) || n == profile.OtherHeaders
		})
		if subsequence(got, order) {
			return nil
		}
	}
	return fmt.Errorf("headers %v are in no order of %s's HTTP/2 lists", got, q.kind)
}

// subsequence reports whether every name of got is in order, in the
// order given.
func subsequence(got, order []string) bool {
	i := 0
	for _, n := range got {
		j := slices.Index(order[i:], n)
		if j < 0 {
			return false
		}
		i += j + 1
	}
	return true
}

// h3Steps are the h3 pages' steps by path.
func h3Steps() map[string]server.H3Step {
	steps := map[string]server.H3Step{}
	for _, st := range slices.Concat(server.H3DiscoverySteps(), server.H3PageSteps()) {
		steps[st.Path()] = st
	}
	return steps
}

// h3Requests reads every request over HTTP/3 of the h3 captures, with
// its kind from its page's step.
func h3Requests(dir, name string) ([]h3Request, error) {
	paths, err := filepath.Glob(filepath.Join(dir, name+".h3-*.pagetrace.json.gz"))
	if err != nil {
		return nil, err
	}
	steps := h3Steps()
	var out []h3Request
	var errs []error
	for _, path := range paths {
		pages, err := capture.LoadPages(path)
		if err != nil {
			return nil, err
		}
		for si, page := range pages {
			for ci, c := range page.QUIC {
				if c.H3 == nil {
					continue
				}
				for _, r := range c.H3.Requests {
					at := fmt.Sprintf("%s: sample %d, QUIC connection %d, %s", filepath.Base(path), si+1, ci+1, r.Path)
					kind, err := h3Kind(steps, r.Path)
					if err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", at, err))
						continue
					}
					q := h3Request{
						at:            at,
						path:          r.Path,
						kind:          kind,
						method:        r.Method,
						fetchPriority: steps[r.Path].Priority,
					}
					for _, f := range r.Section.Fields {
						switch {
						case strings.HasPrefix(f.Name, ":"):
							q.pseudo = append(q.pseudo, f.Name)
						case !slices.Contains(q.names, f.Name):
							q.names = append(q.names, f.Name)
							q.fields = append(q.fields, profile.Header{
								Name:  f.Name,
								Value: f.Value,
							})
						}
						if f.Name == "priority" {
							q.priority = f.Value
						}
					}
					out = append(out, q)
				}
			}
		}
	}
	return out, errors.Join(errs...)
}

// h3Kind is the kind of the h3 pages' request for path: its step's, the
// page's own navigation, or /done's fetch.
func h3Kind(steps map[string]server.H3Step, path string) (string, error) {
	switch path {
	case "/":
		return navigation, nil
	case "/done":
		return profile.KindFetch, nil
	}
	st, ok := steps[path]
	if !ok {
		return "", errors.New("no h3 step makes it")
	}
	switch st.Kind {
	case "navigate":
		return navigation, nil
	case "style":
		return profile.KindStyle, nil
	case "script":
		switch {
		case st.Tag == "":
			return profile.KindInjectedScript, nil
		case strings.Contains(st.Tag, " async "):
			return profile.KindAsyncScript, nil
		}
		return profile.KindScript, nil
	case "image":
		if st.Tag == "" {
			return profile.KindInjectedImage, nil
		}
		return profile.KindImage, nil
	case "iframe":
		return profile.KindFrame, nil
	case "font", "fetch", "xhr", "beacon":
		return st.Kind, nil
	}
	return "", fmt.Errorf("step kind %q", st.Kind)
}

// h3Priorities checks every HTTP/3 request's priority header against its
// kind's: the HTTP/2 list's default for a request without fetchpriority
// (a variant's where one sets it), Kind.High or Kind.Low for one with it;
// where the kind's High or Low was not measured over HTTP/2, the HTTP/3
// requests' one value becomes http3.high or http3.low.
func h3Priorities(p *profile.Profile, reqs []h3Request) (high, low map[string]string, err error) {
	var errs []error
	measured := func(level string, got map[string]string, q h3Request, kind *profile.Kind) {
		var u *profile.Urgency
		if kind != nil {
			u = kind.High
			if level == "low" {
				u = kind.Low
			}
		}
		switch v, seen := got[q.kind]; {
		case u != nil && u.Priority != q.priority:
			errs = append(errs, fmt.Errorf("%s: fetchpriority %s with priority %q, the kind's %q", q.at, level, q.priority, u.Priority))
		case u != nil:
		case seen && v != q.priority:
			errs = append(errs, fmt.Errorf("%s: fetchpriority %s with priority %q, another request's %q", q.at, level, q.priority, v))
		default:
			got[q.kind] = q.priority
		}
	}
	high, low = map[string]string{}, map[string]string{}
	for _, q := range reqs {
		kind := p.Kinds[q.kind]
		switch q.fetchPriority {
		case "high":
			measured("high", high, q, kind)
		case "low":
			measured("low", low, q, kind)
		default:
			_, pinned := h3PriorityGaps[p.Name+" "+q.path]
			switch ok := defaultPriority(p, q); {
			case ok && pinned:
				errs = append(errs, fmt.Errorf("%s: h3PriorityGaps pins it, but its priority %q is its kind's: remove the pin", q.at, q.priority))
			case !ok && !pinned:
				errs = append(errs, fmt.Errorf("%s: priority %q is no HTTP/2 list's of %s", q.at, q.priority, q.kind))
			}
		}
	}
	if len(high) == 0 {
		high = nil
	}
	if len(low) == 0 {
		low = nil
	}
	return high, low, errors.Join(errs...)
}

// defaultPriority reports whether q's priority header (or its absence) is
// what one of its kind's HTTP/2 lists sends by default.
func defaultPriority(p *profile.Profile, q h3Request) bool {
	lists := [][]profile.Header{
		h2List(p, q.kind),
	}
	variants := p.Variants
	if q.kind != navigation {
		variants = p.Kinds[q.kind].Variants
	}
	for _, v := range variants {
		if len(v.Headers) > 0 {
			lists = append(lists, v.Headers)
		}
	}
	for _, list := range lists {
		value, ok := headerValue(list, "priority")
		if ok && value == q.priority || !ok && q.priority == "" {
			return true
		}
	}
	return false
}

// h2Urgencies takes the fetchpriority high and low requests the h3 pages
// sent over HTTP/2 (Safari's h3-page went all over HTTP/2) as their kind's
// High and Low where the kinds captures did not measure them, and checks
// them against the kind's where they did.
func h2Urgencies(p *profile.Profile, dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, p.Name+".h3-*.pagetrace.json.gz"))
	if err != nil {
		return err
	}
	steps := h3Steps()
	type key struct {
		kind, level string
	}
	groups := map[key][]*request{}
	var order []key
	for _, path := range paths {
		pages, err := capture.LoadPages(path)
		if err != nil {
			return err
		}
		for si, page := range pages {
			for _, q := range h2Requests(page, si, "/h3/") {
				st, ok := steps[q.path]
				if !ok || st.Priority == "" {
					continue
				}
				kind, err := h3Kind(steps, q.path)
				if err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				k := key{kind, st.Priority}
				if _, seen := groups[k]; !seen {
					order = append(order, k)
				}
				groups[k] = append(groups[k], q)
			}
		}
	}
	var errs []error
	for _, k := range order {
		got, err := urgencyOf(groups[k])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s fetchpriority %s over HTTP/2 in the h3 pages: %w", k.kind, k.level, err))
			continue
		}
		kind := p.Kinds[k.kind]
		if kind == nil {
			errs = append(errs, fmt.Errorf("the profile has no kind %s", k.kind))
			continue
		}
		have := &kind.High
		if k.level == "low" {
			have = &kind.Low
		}
		switch {
		case *have == nil:
			*have = got
		case **have != *got:
			errs = append(errs, fmt.Errorf("%s fetchpriority %s over HTTP/2 in the h3 pages is %+v, the kinds captures' %+v", k.kind, k.level, *got, **have))
		}
	}
	return errors.Join(errs...)
}
