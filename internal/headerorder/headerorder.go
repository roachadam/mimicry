// Package headerorder merges a request's headers with a profile's defaults
// and puts them in the profile's order. Both HTTP/1.1 and HTTP/2 use it.
//
// Imports: profile and the standard library.
package headerorder

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

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
	// Headers is the HTTP/2 list (lowercase names); HTTP1 the HTTP/1.1 one,
	// named as sent, or nil to write Headers after Host and Connection.
	// Both empty sends only the request's own headers.
	Headers []profile.Header
	HTTP1   []profile.Header
	// NoHTTP1: the request's lists were measured over HTTP/2 only, so an
	// HTTP/1.1 connection must refuse it (ErrNotMeasured) rather than
	// guess the order.
	NoHTTP1 bool
	// Weight is the HEADERS priority weight; zero keeps the profile's.
	Weight uint8
	// Table is the browser's header table for the page's own headers
	// (fetch() and XHR); nil places them as the lists say.
	Table *profile.HeaderTable
	// LowerOwn sends the headers the lists do not name lowercase over
	// HTTP/1.1 (fetch()), not as the caller spelled them.
	LowerOwn bool
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

// Key is the request header that sets the header list for one request: its
// values are header names, in the order to send them. It replaces the
// profile's order, so a profile default it does not name is not sent; a
// header the request sets is still sent, after the named ones. Its name is
// matched in any case. The trailing colon makes it an invalid header name,
// so it can never be sent by mistake.
const Key = "Header-Order:"

// Field is one header to send. Name is lowercase.
type Field struct {
	Name  string
	Value string
}

// Build returns the headers to send for a request with headers h, using the
// profile's headers for order and defaults:
//
//   - a profile header with a value is sent unless h sets that header; h's
//     value wins;
//   - a header in h with no values (h["Accept-Language"] = nil) suppresses the
//     profile's default;
//   - order is the profile's, or Key's if set (then profile defaults Key
//     does not name are dropped); headers it does not list go where it
//     lists profile.OtherHeaders, else, with a table and no Key, in the
//     table's order together with the table's names the list sends, where
//     the list has the first of those (TableOrder), else after the rest,
//     sorted by name;
//   - names are compared case-insensitively;
//   - skip names headers never to send (connection-level ones the protocol
//     handles itself).
func Build(prof []profile.Header, h http.Header, skip func(name string) bool, table *profile.HeaderTable) []Field {
	// A key present with no values suppresses the profile's default: it stays
	// in the map with nothing to send.
	values := make(map[string][]string, len(h))
	var override []string
	for k, v := range h {
		if strings.EqualFold(k, Key) {
			override = append(override, v...)
			continue
		}
		name := strings.ToLower(k)
		values[name] = append(values[name], v...)
	}
	defaults := make(map[string]string, len(prof))
	order := make([]string, 0, len(prof))
	for _, ph := range prof {
		name := strings.ToLower(ph.Name)
		defaults[name] = ph.Value
		order = append(order, name)
	}
	if len(override) > 0 {
		// The caller's own order: no browser placed anything.
		table = nil
		order = order[:0]
		for _, name := range override {
			order = append(order, strings.ToLower(name))
		}
	}

	fields := make([]Field, 0, len(values)+len(defaults))
	sent := make(map[string]bool, len(order))
	emit := func(name string) {
		if sent[name] || skip(name) {
			return
		}
		sent[name] = true
		if v, set := values[name]; set {
			for _, value := range v {
				fields = append(fields, Field{
					Name:  name,
					Value: value,
				})
			}
			return
		}
		if d := defaults[name]; d != "" {
			fields = append(fields, Field{
				Name:  name,
				Value: d,
			})
		}
	}
	rest := make([]string, 0, len(values))
	for name := range values {
		if !slices.Contains(order, name) {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	tabled := tableRun(table, order, rest, func(name string) bool {
		if skip(name) {
			return false
		}
		if v, set := values[name]; set {
			return len(v) > 0
		}
		return defaults[name] != ""
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

// tableRun returns the run table sends for a request whose list is order
// and whose own headers are rest, sends reporting which names go out: nil
// without a table, a page header, a table name the list sends, or with an
// OtherHeaders slot (the list places the page's headers itself), or when
// the table cannot hold the list (TableOrder).
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
