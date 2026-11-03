package main

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/quictrace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// h2EndStream is HTTP/2's END_STREAM flag (RFC 9113, section 6.1).
const h2EndStream = 0x1

// emptySent is one request without a body of the bodiless captures: whether
// it sent content-length: 0, last (after the other headers, before a final
// te) or not, and, over HTTP/2, whether an empty DATA frame ended it.
type emptySent struct {
	at     string
	kind   string
	method string
	h3     bool
	sends  bool
	last   bool
	data   bool
}

// deriveEmptyBodies derives the navigation's and fetch()'s emptyBody from
// the bodiless captures (server.BodilessCaptures); without one, both stay
// nil. Run after derive: a content-length the request sends in its list's
// place must be where one of the kind's lists has it. Samples, the kind's
// methods and HTTP/3 must agree on the place and the framing; a method
// without content-length must end its stream on its headers.
func deriveEmptyBodies(p *profile.Spec, dir string) error {
	p.EmptyBody = nil
	for _, k := range p.Kinds {
		if k != nil {
			k.EmptyBody = nil
		}
	}

	files, err := server.BodilessCaptures(dir, p)
	if err != nil {
		return err
	}

	var (
		sent []emptySent
		errs []error
	)

	for _, f := range files {
		pages, err := loadFile(f)
		if err != nil {
			return err
		}

		got, fileErrs := emptySents(p, filepath.Base(f.Path), pages)

		sent, errs = append(sent, got...), append(errs, fileErrs...)
	}

	for _, kind := range []string{navigation, profile.KindFetch} {
		e, kindErrs := emptyBodyOf(kind, sent)

		errs = append(errs, kindErrs...)

		if kind == navigation {
			p.EmptyBody = e
		} else if k := p.Kinds[kind]; k != nil {
			k.EmptyBody = e
		}
	}

	return errors.Join(errs...)
}

// emptySents reads the requests of the bodiless page's steps (those with a
// method) in pages, a capture file's samples.
func emptySents(p *profile.Spec, file string, pages []*capture.Page) ([]emptySent, []error) {
	steps := map[string]server.H3Step{}
	for _, st := range server.BodilessSteps() {
		if st.Method != "" {
			steps[st.Path()] = st
		}
	}

	var (
		out  []emptySent
		errs []error
	)

	for si, page := range pages {
		for ti, tr := range page.H2 {
			for _, r := range tr.Requests {
				st, ok := steps[r.Path]
				if !ok {
					continue
				}

				at := fmt.Sprintf("%s: sample %d, TCP connection %d, %s", file, si+1, ti+1, r.Path)
				method, fields := "", []profile.Header{}

				for _, ins := range r.Instructions {
					switch {
					case ins.Name == ":method":
						method = ins.Value
					case ins.Name == "" || strings.HasPrefix(ins.Name, ":"):
					default:
						fields = append(fields, profile.Header{
							Name:  ins.Name,
							Value: ins.Value,
						})
					}
				}

				s, err := emptyRequest(p, at, st, method, fields, false)
				if err != nil {
					errs = append(errs, err)
					continue
				}

				var headersEnd, other bool

				data := 0

				for _, fr := range tr.Frames {
					switch {
					case fr.Stream != r.Stream:
					case fr.Type == "HEADERS":
						headersEnd = fr.Flags&h2EndStream != 0
					case fr.Type == "DATA" && fr.Length == 0 && fr.Flags&h2EndStream != 0:
						data++
					case fr.Type == "DATA":
						other = true
					}
				}

				switch {
				case other || data > 1 || headersEnd == (data == 1):
					errs = append(errs, fmt.Errorf("%s: its stream ended neither on HEADERS nor with one empty DATA frame", at))
				case data == 1 && !s.sends:
					errs = append(errs, fmt.Errorf("%s: an empty DATA frame without content-length: 0, which emptyBody cannot say", at))
				default:
					s.data = data == 1
					out = append(out, s)
				}
			}
		}

		for ci, c := range page.QUIC {
			if c.H3 == nil {
				continue
			}

			for _, r := range c.H3.Requests {
				st, ok := steps[r.Path]
				if !ok {
					continue
				}

				at := fmt.Sprintf("%s: sample %d, QUIC connection %d, %s", file, si+1, ci+1, r.Path)

				var fields []profile.Header

				for _, fl := range r.Section.Fields {
					if !strings.HasPrefix(fl.Name, ":") {
						fields = append(fields, profile.Header{
							Name:  fl.Name,
							Value: fl.Value,
						})
					}
				}

				s, err := emptyRequest(p, at, st, r.Method, fields, true)

				switch {
				case err != nil:
					errs = append(errs, err)
				case r.End != "FIN" || slices.ContainsFunc(r.Frames, func(fr quictrace.H3Frame) bool {
					return fr.Name != "HEADERS"
				}):
					errs = append(errs, fmt.Errorf("%s: not one HEADERS frame ending the stream, which emptyBody cannot say over HTTP/3", at))
				default:
					out = append(out, s)
				}
			}
		}
	}

	return out, errs
}

// emptyRequest reads one request: its kind from its step, whether it sent
// content-length: 0 and where: last when nothing but te follows it, else,
// over HTTP/2, in its list's place, the request's names in the order of
// one of the kind's lists that has content-length. Over HTTP/3, which
// drops and adds names, only last is read.
func emptyRequest(p *profile.Spec, at string, st server.H3Step, method string, fields []profile.Header, h3 bool) (emptySent, error) {
	s := emptySent{
		at:     at,
		method: method,
		h3:     h3,
	}

	switch st.Kind {
	case "navigate":
		s.kind = navigation
	case "fetch":
		s.kind = profile.KindFetch
	default:
		return s, fmt.Errorf("%s: step kind %q", at, st.Kind)
	}

	if method != st.Method {
		return s, fmt.Errorf("%s: method %s, its step's %s", at, method, st.Method)
	}

	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}

	i := slices.Index(names, "content-length")
	if s.sends = i >= 0; !s.sends {
		return s, nil
	}

	if slices.Index(names[i+1:], "content-length") >= 0 || fields[i].Value != "0" {
		return s, fmt.Errorf("%s: content-length %q without a body", at, fields[i].Value)
	}

	// Last says the same as a list placing content-length last, and more
	// where the list the request takes places it earlier (Firefox's
	// same-origin fetch() POST list, from a POST with a body).
	s.last = i == len(names)-1 || (i == len(names)-2 && names[i+1] == "te")
	if h3 || s.last {
		return s, nil
	}

	if !slices.ContainsFunc(kindLists(p, s.kind), func(list []profile.Header) bool {
		order := make([]string, 0, len(list))
		for _, hd := range list {
			order = append(order, strings.ToLower(hd.Name))
		}

		return slices.Contains(order, "content-length") && subsequence(names, order)
	}) {
		return s, fmt.Errorf("%s: content-length: 0 neither in a %s list's place nor last: %s", at, s.kind, strings.Join(names, " "))
	}

	return s, nil
}

// kindLists are kind's HTTP/2 lists: its own and its variants'.
func kindLists(p *profile.Spec, kind string) [][]profile.Header {
	own, variants := p.Headers, p.Variants

	if kind != navigation {
		k := p.Kinds[kind]
		if k == nil {
			return nil
		}

		own, variants = k.Headers, k.Variants
	}

	out := [][]profile.Header{
		own,
	}
	for _, v := range variants {
		out = append(out, v.Headers)
	}

	return out
}

// emptyBodyOf is kind's emptyBody from its requests in sent: nil when none
// sent content-length: 0. Each method must send it in every request or in
// none, over both protocols; those that do, in one place (over HTTP/3 last
// where HTTP/2's is) and, over HTTP/2, with one framing.
func emptyBodyOf(kind string, sent []emptySent) (*profile.EmptyBody, []error) {
	sends := map[string]bool{}
	seen := map[string]string{}

	var (
		errs  []error
		place *emptySent
	)

	for i, s := range sent {
		if s.kind != kind {
			continue
		}

		if at, ok := seen[s.method]; ok && sends[s.method] != s.sends {
			errs = append(errs, fmt.Errorf("%s: content-length: 0 sent %v, at %s %v", s.at, s.sends, at, sends[s.method]))
			continue
		}

		seen[s.method], sends[s.method] = s.at, s.sends
		if !s.sends || s.h3 {
			continue
		}

		switch {
		case place == nil:
			place = &sent[i]
		case place.last != s.last:
			errs = append(errs, fmt.Errorf("%s: content-length: 0 last %v, at %s %v", s.at, s.last, place.at, place.last))
		case place.data != s.data:
			errs = append(errs, fmt.Errorf("%s: an empty DATA frame %v, at %s %v", s.at, s.data, place.at, place.data))
		}
	}

	for _, s := range sent {
		if s.kind == kind && s.h3 && s.sends && place != nil && place.last && !s.last {
			errs = append(errs, fmt.Errorf("%s: content-length: 0 not last, at %s last", s.at, place.at))
		}
	}

	var methods []string
	for _, m := range slices.Sorted(maps.Keys(sends)) {
		if sends[m] {
			methods = append(methods, m)
		}
	}

	switch {
	case len(errs) > 0 || len(methods) == 0:
		return nil, errs
	case place == nil:
		return nil, []error{
			fmt.Errorf("%s: content-length: 0 seen over HTTP/3 only", kind),
		}
	}

	return &profile.EmptyBody{
		ContentLength: methods,
		Last:          place.last,
		Data:          place.data,
	}, nil
}
