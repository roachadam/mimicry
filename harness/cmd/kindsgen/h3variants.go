package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// h3Candidate is one h3 page request, in its HTTP/2 form, that no list of
// its kind covers.
type h3Candidate struct {
	at     string
	kind   string
	method string
	fields []profile.Header
}

// h3Variants adds to each kind the method variants only the h3 pages
// measured (the h3-page's XHR POST: no other capture sends one): from
// their requests over HTTP/2 (Safari's h3-page went all over HTTP/2) and
// over HTTP/3 in their HTTP/2 form (alt-used out, the headers HTTP/3 drops
// back in after what precedes them in the kind's list: Firefox's te,
// last in every list). Every sample's order must be the same; the
// variant's list adds the kind's slots it lacks (cookie, page-set
// headers) where the kind's list has them, keeps the kind's default values
// the requests sent, and makes every other header a slot. Its condition is
// the method, CORS-checked when it carries Origin; it has no HTTP/1.1
// list (not measured). A method variant the kind already has must cover
// the requests.
func h3Variants(p *profile.Profile, dir string, reqs []h3Request, drop []string) error {
	var cands []h3Candidate
	for _, q := range reqs {
		if q.method == "" || q.method == "GET" || q.kind == navigation {
			continue
		}
		fields := slices.DeleteFunc(slices.Clone(q.fields), func(h profile.Header) bool {
			return h.Name == altUsed
		})
		for _, d := range drop {
			fields = insertAfterPredecessor(fields, h2List(p, q.kind), d)
		}
		cands = append(cands, h3Candidate{
			at:     q.at,
			kind:   q.kind,
			method: q.method,
			fields: fields,
		})
	}
	over2, err := h2Candidates(p, dir)
	if err != nil {
		return err
	}
	cands = append(cands, over2...)
	type key struct {
		kind, method string
	}
	groups := map[key][]h3Candidate{}
	var order []key
	for _, c := range cands {
		kind := p.Kinds[c.kind]
		if kind == nil || covered(kind, c) {
			continue
		}
		k := key{c.kind, c.method}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}
	var errs []error
	for _, k := range order {
		v, err := methodVariant(p.Kinds[k.kind], groups[k])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p.Kinds[k.kind].Variants = append(p.Kinds[k.kind].Variants, v)
	}
	return errors.Join(errs...)
}

// covered reports whether one of kind's lists for c's method has c's
// headers in its order.
func covered(kind *profile.Kind, c h3Candidate) bool {
	got := names(c.fields)
	if subsequence(got, slices.DeleteFunc(names(kind.Headers), func(n string) bool {
		return n == profile.OtherHeaders
	})) {
		return true
	}
	for _, v := range kind.Variants {
		if v.Method == c.method && len(v.Headers) > 0 && subsequence(got, slices.DeleteFunc(names(v.Headers), func(n string) bool {
			return n == profile.OtherHeaders
		})) {
			return true
		}
	}
	return false
}

// methodVariant is the variant cands, one kind's requests of one method,
// show.
func methodVariant(kind *profile.Kind, cands []h3Candidate) (profile.Variant, error) {
	first := names(cands[0].fields)
	for _, c := range cands[1:] {
		if got := names(c.fields); !slices.Equal(got, first) {
			return profile.Variant{}, fmt.Errorf("%s: %s %s headers %v, another sample's %v", c.at, c.kind, c.method, got, first)
		}
	}
	// The kind's slots the requests did not fill go after the last header
	// that precedes them in the kind's list: their place is assumed.
	merged := slices.Clone(cands[0].fields)
	for _, h := range kind.Headers {
		if h.Value == "" && !slices.Contains(first, h.Name) {
			merged = insertAfterPredecessor(merged, kind.Headers, h.Name)
		}
	}
	v := profile.Variant{
		Method: cands[0].method,
		CORSChecked: slices.ContainsFunc(cands[0].fields, func(h profile.Header) bool {
			return h.Name == "origin"
		}),
	}
	for _, name := range names(merged) {
		value := ""
		if def, ok := headerValue(kind.Headers, name); ok && def != "" {
			value = def
			for _, c := range cands {
				if got, _ := headerValue(c.fields, name); got != def {
					value = ""
				}
			}
		}
		v.Headers = append(v.Headers, profile.Header{
			Name:  name,
			Value: value,
		})
	}
	return v, nil
}

// insertAfterPredecessor puts name, with its default in list, into
// fields after the last field that precedes it in list.
func insertAfterPredecessor(fields, list []profile.Header, name string) []profile.Header {
	at := slices.IndexFunc(list, func(h profile.Header) bool {
		return h.Name == name
	})
	if at < 0 {
		return fields
	}
	pos := 0
	for i, f := range fields {
		if j := slices.IndexFunc(list, func(h profile.Header) bool {
			return h.Name == f.Name
		}); j >= 0 && j < at {
			pos = i + 1
		}
	}
	return slices.Insert(fields, pos, list[at])
}

// h2Candidates are the h3 pages' non-GET requests over HTTP/2.
func h2Candidates(p *profile.Profile, dir string) ([]h3Candidate, error) {
	paths, err := server.H3ProfileCaptures(dir, p.Name)
	if err != nil {
		return nil, err
	}
	steps := h3Steps()
	var out []h3Candidate
	for _, path := range paths {
		pages, err := capture.LoadPages(path)
		if err != nil {
			return nil, err
		}
		for si, page := range pages {
			for _, q := range h2Requests(page, si, "/h3/") {
				if q.method == "GET" || q.method == "" {
					continue
				}
				kind, err := h3Kind(steps, q.path)
				if err != nil || kind == navigation {
					continue
				}
				out = append(out, h3Candidate{
					at:     fmt.Sprintf("%s: sample %d, %s over HTTP/2", filepath.Base(path), si+1, q.path),
					kind:   kind,
					method: q.method,
					fields: q.h2,
				})
			}
		}
	}
	return out, nil
}
