// Package headerorder merges a request's headers with a profile's defaults
// and puts them in the profile's order. Both HTTP/1.1 and HTTP/2 use it.
//
// Imports: profile and the standard library.
package headerorder

import (
	"net/http"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/profile"
)

// Key is the request header that overrides the order for one request: its
// values are header names, in the order to send them. The trailing colon makes
// it an invalid header name, so it can never be sent by mistake.
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
//   - order is the profile's (or Key's, if set); headers it does not list come
//     after, sorted by name;
//   - skip names headers never to send (connection-level ones the protocol
//     handles itself).
func Build(prof []profile.Header, h http.Header, skip func(name string) bool) []Field {
	// A key present with no values suppresses the profile's default: it stays
	// in the map with nothing to send.
	values := make(map[string][]string, len(h))
	for k, v := range h {
		if k == Key {
			continue
		}
		name := strings.ToLower(k)
		values[name] = append(values[name], v...)
	}
	defaults := make(map[string]string, len(prof))
	order := make([]string, 0, len(prof))
	for _, ph := range prof {
		defaults[ph.Name] = ph.Value
		order = append(order, ph.Name)
	}
	if override := h[Key]; len(override) > 0 {
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
	for _, name := range order {
		emit(name)
	}
	rest := make([]string, 0, len(values))
	for name := range values {
		if !sent[name] {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	for _, name := range rest {
		emit(name)
	}
	return fields
}
