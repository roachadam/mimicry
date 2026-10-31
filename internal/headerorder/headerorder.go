// Package headerorder merges a request's headers with a profile's defaults
// and puts them in the profile's order. HTTP/1.1, HTTP/2 and HTTP/3 use it;
// it also builds responses' header maps (response.go), HTTP/2's now.
package headerorder

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/roachadam/mimicry/profile"
)

// ErrNotMeasured reports a request the profile cannot describe. The root
// package exports it as mimicry.ErrNotMeasured; it lives here so the
// connections can return it too.
var ErrNotMeasured = errors.New("mimicry: not measured for this profile")

// Plan is how to send one request other than as the profile's navigation:
// its header lists, with request-dependent slots already filled, and its
// HEADERS weight. The connections use it in place of the profile's lists.
type Plan struct {
	// Kind is the request's kind in the profile (Profile.Kinds); empty
	// for the navigation and for Exact requests.
	Kind string
	// Headers is the HTTP/2 list (lowercase names); HTTP1 the HTTP/1.1 one,
	// named as sent, or nil to write Headers after Host and Connection.
	// Both empty sends only the request's own headers.
	Headers []profile.Header
	HTTP1   []profile.Header
	// NoHTTP1: the request's lists were measured over HTTP/2 only, so an
	// HTTP/1.1 connection must refuse it (ErrNotMeasured) rather than
	// guess the order.
	NoHTTP1 bool
	// HTTP3Only: part of the request (its fetchpriority's priority header)
	// was measured over HTTP/3 only, so a TCP connection must refuse it
	// (ErrNotMeasured).
	HTTP3Only bool
	// Weight is the HEADERS priority weight; zero keeps the profile's.
	Weight uint8
	// Table is the browser's header table for the page's own headers
	// (fetch() and XHR); nil places them as the lists say.
	Table *profile.HeaderTable
	// LowerOwn sends the headers the lists do not name lowercase over
	// HTTP/1.1 (fetch()), not as the caller spelled them.
	LowerOwn bool
	// NoContentLength sends a body over HTTP/2 and HTTP/3 without content-length:
	// the request's measured list has none (Safari's redirected bodies
	// after a preflight).
	NoContentLength bool
}

type planKey struct{}

// WithPlan returns ctx carrying p.
func WithPlan(ctx context.Context, p *Plan) context.Context {
	return context.WithValue(ctx, planKey{}, p)
}

// PlanFrom returns the plan ctx carries, or nil: the profile's navigation.
func PlanFrom(ctx context.Context) *Plan {
	p, _ := ctx.Value(planKey{}).(*Plan)
	return p
}

// Key is the request header naming, in order, the headers to send for one
// request. It replaces the profile's order: unnamed profile defaults are
// dropped, unnamed request headers go after. The trailing colon makes it an
// invalid header name, so it is never sent by mistake.
const Key = "Header-Order:"

// Field is one header to send. Name is lowercase.
type Field struct {
	Name  string
	Value string
}

// Set gives a header Build sends in place of the request's own, whatever
// the request's spelling of it: Value, or nothing when Value is empty,
// which also suppresses the profile's default. Name is lowercase.
type Set struct {
	Name  string
	Value string
}

// WithoutFraming returns h without the headers that frame a message
// (Content-Length, Connection, Transfer-Encoding), in any spelling: the
// connection builds them from the request, as net/http does. It is h
// itself when h has none, else a copy.
func WithoutFraming(h http.Header) http.Header {
	found := false
	for k := range h {
		if Framing(k) {
			found = true
			break
		}
	}

	if !found {
		return h
	}

	out := h.Clone()
	maps.DeleteFunc(out, func(k string, _ []string) bool {
		return Framing(k)
	})

	return out
}

// Framing reports a header name WithoutFraming drops, in any case.
func Framing(name string) bool {
	return strings.EqualFold(name, "content-length") || strings.EqualFold(name, "connection") || strings.EqualFold(name, "transfer-encoding")
}

// Build appends the headers to send for a request with headers h to dst
// and returns it, using the profile's headers for order and defaults:
//
//   - h's value wins over a profile default; a header in h with no values
//     suppresses the default;
//   - sets win over h;
//   - order is the profile's, or Key's (dropping unnamed defaults); unlisted
//     headers go at profile.OtherHeaders, else in table order (TableOrder),
//     else after the rest, sorted by name;
//   - names compare case-insensitively;
//   - skip names headers never to send (connection-level ones).
//
// h is only read, so a caller need not copy it to change it: sets do.
func Build(dst []Field, prof []profile.Header, h http.Header, sets []Set, skip func(name string) bool, table *profile.HeaderTable) []Field {
	// A key present with no values suppresses the profile's default: it stays
	// in the map with nothing to send.
	values := make(map[string][]string, len(h))

	// names[i] is prof[i]'s name, lowercase; the last default for a name
	// wins. Linear lookups and room on the stack for the usual lists:
	// lists are short, and this runs for every request.
	var namesBuf, sentBuf [48]string

	names := namesBuf[:0]
	for _, ph := range prof {
		names = append(names, strings.ToLower(ph.Name))
	}

	// setOf finds name among sets, which win over h.
	setOf := func(name string) (Set, bool) {
		for _, s := range sets {
			if s.Name == name {
				return s, true
			}
		}

		return Set{}, false
	}

	var override []string

	for k, v := range h {
		if strings.EqualFold(k, Key) {
			override = append(override, v...)
			continue
		}

		name := lowerName(k, names)
		if _, set := setOf(name); set {
			continue
		}

		if old, ok := values[name]; ok {
			values[name] = append(old, v...)
		} else {
			// Clipped: an append for another spelling of the name copies
			// instead of writing into h's array.
			values[name] = slices.Clip(v)
		}
	}

	defaultOf := func(name string) string {
		for i, n := range slices.Backward(names) {
			if n == name {
				return prof[i].Value
			}
		}

		return ""
	}

	order := names
	if len(override) > 0 {
		// The caller's own order: no browser placed anything.
		table = nil
		order = make([]string, 0, len(override))

		for _, name := range override {
			order = append(order, strings.ToLower(name))
		}
	}

	var restBuf [16]string

	fields := slices.Grow(dst, len(values)+len(sets)+len(prof))
	rest := restBuf[:0]

	for name := range values {
		if !slices.Contains(order, name) {
			rest = append(rest, name)
		}
	}

	for _, s := range sets {
		if !slices.Contains(order, s.Name) {
			rest = append(rest, s.Name)
		}
	}

	slices.Sort(rest)

	sent := sentBuf[:0]
	emit := func(name string) {
		if slices.Contains(sent, name) || skip(name) {
			return
		}

		sent = append(sent, name)
		if s, set := setOf(name); set {
			if s.Value != "" {
				fields = append(fields, Field{
					Name:  name,
					Value: s.Value,
				})
			}

			return
		}

		if v, set := values[name]; set {
			for _, value := range v {
				fields = append(fields, Field{
					Name:  name,
					Value: value,
				})
			}

			return
		}

		if d := defaultOf(name); d != "" {
			fields = append(fields, Field{
				Name:  name,
				Value: d,
			})
		}
	}

	tabled := tableRun(table, order, rest, func(name string) bool {
		if skip(name) {
			return false
		}

		if s, set := setOf(name); set {
			return s.Value != ""
		}

		if v, set := values[name]; set {
			return len(v) > 0
		}

		return defaultOf(name) != ""
	})
	for _, name := range order {
		if name == profile.OtherHeaders {
			for _, r := range rest {
				emit(r)
			}

			continue
		}

		if slices.Contains(tabled, name) {
			// The table's run, at its first name the list sends.
			for _, t := range tabled {
				emit(t)
			}

			continue
		}

		emit(name)
	}

	for _, name := range rest {
		emit(name)
	}

	return fields
}

// lowerName returns strings.ToLower(k), as the string in names when it is
// one of them, which spares allocating it.
func lowerName(k string, names []string) string {
	var buf [64]byte

	lower, ok := AppendLower(buf[:0], k)
	if !ok {
		return strings.ToLower(k)
	}

	for _, name := range names {
		// The conversion in the comparison does not allocate.
		if name == string(lower) {
			return name
		}
	}

	return strings.ToLower(k)
}

// AppendLower appends s lowercased to dst, and reports whether s is ASCII:
// otherwise the result is not strings.ToLower(s) and the caller falls back
// to it.
func AppendLower(dst []byte, s string) ([]byte, bool) {
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= utf8.RuneSelf:
			return dst, false
		case 'A' <= c && c <= 'Z':
			c += 'a' - 'A'
		}

		dst = append(dst, c)
	}

	return dst, true
}

// Replacement is a list header whose value Build takes from the
// request's headers instead: the list's value, and the request's (none:
// suppressed). Name is lowercase.
type Replacement struct {
	Name   string
	List   string
	Values []string
}

// Replaced reports, by Build's rules, which of prof's valued headers h
// replaces or suppresses and, when h sets Key, which it drops. Both in
// prof's order, names lowercase.
func Replaced(prof []profile.Header, h http.Header) (replaced []Replacement, dropped []string) {
	values := make(map[string][]string, len(h))

	var override []string

	for k, v := range h {
		if strings.EqualFold(k, Key) {
			for _, name := range v {
				override = append(override, strings.ToLower(name))
			}

			continue
		}

		name := strings.ToLower(k)

		values[name] = append(values[name], v...)
	}

	for _, ph := range prof {
		name := strings.ToLower(ph.Name)
		v, set := values[name]

		switch {
		case ph.Value == "" || name == profile.OtherHeaders:
		case set && !slices.Equal(v, []string{ph.Value}):
			replaced = append(replaced, Replacement{
				Name:   name,
				List:   ph.Value,
				Values: v,
			})

		case !set && len(override) > 0 && !slices.Contains(override, name):
			dropped = append(dropped, name)
		}
	}

	return replaced, dropped
}

// tableRun returns the table-ordered run for a request with list order
// and own headers rest, or nil when no table applies (no table, an
// OtherHeaders slot, or TableOrder cannot hold the list).
func tableRun(table *profile.HeaderTable, order, rest []string, sends func(string) bool) []string {
	if table == nil || slices.Contains(order, profile.OtherHeaders) {
		return nil
	}

	var page, members []string
	for _, name := range rest {
		if sends(name) {
			page = append(page, name)
		}
	}

	if len(page) == 0 {
		return nil
	}

	all := table.Members()
	for _, name := range order {
		if slices.Contains(all, name) && sends(name) && !slices.Contains(members, name) {
			members = append(members, name)
		}
	}

	if len(members) == 0 {
		return nil
	}

	run, ok := TableOrder(table, members, page)
	if !ok {
		return nil
	}

	return run
}
