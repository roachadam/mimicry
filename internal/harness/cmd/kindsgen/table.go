package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// tableKinds are the kinds whose requests carry headers the page set.
var tableKinds = []string{profile.KindFetch, profile.KindXHR}

// tableShapes are the header tables kindsgen tries, in order: Blink's with
// the page's headers first (Chrome), CFNetwork's with them after the
// browser's own (Safari), and the other two pairings.
var tableShapes = []struct {
	hash      string
	pageFirst bool
}{
	{hash: profile.HashBlink, pageFirst: true},
	{hash: profile.HashCF},
	{hash: profile.HashBlink},
	{hash: profile.HashCF, pageFirst: true},
}

// Places a list header besides Own can take in a table
// (profile.HeaderTable), in the order kindsgen tries them.
const (
	roleAsPage = iota
	roleOwn
	roleLate
	roles
)

// tableCase is one captured list a table must explain: sent, built from
// base (its condition's order without page headers; nil: keep sent's
// order) plus the page's headers in pageOrder.
type tableCase struct {
	label      string
	redirected bool
	base       []string
	sent       []string
	pageOrder  []string
}

// deriveTable derives the browser's header table from its fetch() and XHR
// requests: Own are browser headers found between page headers; the shape
// is the first in tableShapes that explains every non-redirect request.
// Redirected is set when it explains redirects too. Nil when page headers
// always form one run, which profile.OtherHeaders places (Firefox).
func deriveTable(kinds ...[]*request) (*profile.HeaderTable, error) {
	ownSet, extraSet := map[string]bool{}, map[string]bool{}

	var cases, redirects []tableCase

	for _, reqs := range kinds {
		groups := map[condition][]*request{}
		for _, r := range reqs {
			groups[r.condition()] = append(groups[r.condition()], r)
		}

		for _, r := range reqs {
			for _, h1 := range []bool{false, true} {
				list := (*request).h2Of
				if h1 {
					list = (*request).h1Of
				}

				sent := lowerNames(list(r))
				if len(sent) == 0 {
					continue
				}

				c := tableCase{
					label:      fmt.Sprintf("%s %s (sample %d, %s)", r.method, r.label, r.sample+1, map[bool]string{false: "HTTP/2", true: "HTTP/1.1"}[h1]),
					redirected: r.hop,
					sent:       sent,
					pageOrder:  r.pageOrder,
				}
				if page := pageIndexes(sent); len(page) > 0 {
					if !r.hop {
						for _, n := range sent[page[0]:page[len(page)-1]] {
							if !isPageSet(n) {
								ownSet[n] = true
							}
						}
					}

					order, ok := baseOrder(groups, r, list, h1)
					if !ok {
						continue
					}

					c.base = order
				}

				if r.hop {
					redirects = append(redirects, c)
				} else {
					cases = append(cases, c)
				}
			}
		}
	}

	if len(ownSet) == 0 {
		return nil, nil //nolint:nilnil // no table: the lists' OtherHeaders slot places the page's headers
	}

	// Names inside a run of the table's, own or the page's.
	for _, c := range cases {
		first, last := -1, -1
		for i, n := range c.sent {
			if ownSet[n] || isPageSet(n) {
				if first < 0 {
					first = i
				}

				last = i
			}
		}

		for i := first + 1; i < last; i++ {
			if n := c.sent[i]; !ownSet[n] && !isPageSet(n) {
				extraSet[n] = true
			}
		}
	}

	own, extras := sortedKeys(ownSet), sortedKeys(extraSet)
	explained := func(t *profile.HeaderTable, cs []tableCase) int {
		return slices.IndexFunc(cs, func(c tableCase) bool {
			return !tableExplains(t, c.base, c.sent, c.pageOrder)
		})
	}

	var failed []string

	for _, shape := range tableShapes {
		for combo := range pow(roles, len(extras)) {
			t := &profile.HeaderTable{
				Hash:      shape.hash,
				Own:       slices.Clone(own),
				PageFirst: shape.pageFirst,
			}
			for i, n := range extras {
				switch combo / pow(roles, i) % roles {
				case roleAsPage:
					t.AsPage = append(t.AsPage, n)
				case roleOwn:
					t.Own = append(t.Own, n)
				default:
					t.Late = append(t.Late, n)
				}
			}

			slices.Sort(t.Own)

			bad := explained(t, cases)
			if bad < 0 {
				t.Redirected = explained(t, redirects) < 0
				return t, nil
			}

			if combo == 0 {
				failed = append(failed, fmt.Sprintf("%s (page first %v): %s %q", shape.hash, shape.pageFirst, cases[bad].label, cases[bad].sent))
			}
		}
	}

	return nil, fmt.Errorf("header table: own headers %q (extras %q) in no table explain the captures; first failure per table: %s", own, extras, strings.Join(failed, "; "))
}

// baseOrder returns, lowercase, the order of r's condition without page
// headers; false when uncaptured, inconsistent, or missing a header r sends.
func baseOrder(groups map[condition][]*request, r *request, list func(*request) []profile.Header, h1 bool) ([]string, bool) {
	base := r.condition()

	base.pageSet, base.cors = false, slices.ContainsFunc(list(r), func(h profile.Header) bool {
		return strings.EqualFold(h.Name, "origin")
	})

	if groups[base] == nil {
		return nil, false
	}

	order, err := orderOf(groups[base], list, h1)
	if err != nil {
		return nil, false
	}

	order = lowerAll(order)

	return order, describes(order, lowerNames(list(r)))
}

// tablePlaces reports whether table places the page's headers on every
// request of group, so the group needs no variant. It errors when the
// table fails a group it should order.
func tablePlaces(table *profile.HeaderTable, groups map[condition][]*request, group []*request) (bool, error) {
	for _, r := range group {
		if r.hop && !table.Redirected {
			return false, nil
		}

		for _, h1 := range []bool{false, true} {
			list := (*request).h2Of
			if h1 {
				list = (*request).h1Of
			}

			if len(list(r)) == 0 {
				continue
			}

			base, ok := baseOrder(groups, r, list, h1)
			if !ok {
				return false, nil
			}

			if sent := lowerNames(list(r)); !tableExplains(table, base, sent, r.pageOrder) {
				return false, fmt.Errorf("header table: %s %s (sample %d) sends %q; the table makes it %q", r.method, r.label, r.sample+1, sent, tableSent(table, base, sent, r.pageOrder))
			}
		}
	}

	return true, nil
}

// describes reports whether base orders every header sent but the page's.
func describes(base, sent []string) bool {
	return !slices.ContainsFunc(sent, func(n string) bool {
		return !isPageSet(n) && !slices.Contains(base, n)
	})
}

// tableExplains reports whether table builds sent from base (nil: sent
// itself) as headerorder.Build does.
func tableExplains(table *profile.HeaderTable, base, sent, pageOrder []string) bool {
	if base == nil {
		base = sent
	}

	return slices.Equal(tableSent(table, base, sent, pageOrder), sent)
}

// tableSent is what headerorder.Build sends for base and sent: the page's
// headers in the table's run at its first table name, else sorted at the
// end. Nil when the table cannot hold base's order.
func tableSent(table *profile.HeaderTable, base, sent, pageOrder []string) []string {
	members, order := []string{}, []string{}
	all := table.Members()

	for _, n := range base {
		if slices.Contains(sent, n) {
			order = append(order, n)
			if slices.Contains(all, n) {
				members = append(members, n)
			}
		}
	}

	var page []string
	for _, i := range pageIndexes(sent) {
		page = append(page, sent[i])
	}

	slices.Sort(page)

	if pageOrder != nil {
		page = slices.DeleteFunc(slices.Clone(pageOrder), func(n string) bool {
			return !slices.Contains(sent, n)
		})
	}

	if len(members) == 0 {
		slices.Sort(page)

		return append(order, page...)
	}

	run, ok := headerorder.TableOrder(table, members, page)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(sent))
	placed := false

	for _, n := range order {
		switch {
		case !slices.Contains(members, n):
			out = append(out, n)
		case !placed:
			out = append(out, run...)
			placed = true
		}
	}

	return out
}

// pageIndexes are the indexes of the page's headers in names.
func pageIndexes(names []string) []int {
	var out []int
	for i, n := range names {
		if isPageSet(n) {
			out = append(out, i)
		}
	}

	return out
}

func lowerNames(hs []profile.Header) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = strings.ToLower(h.Name)
	}

	return out
}

func lowerAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.ToLower(n)
	}

	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// pow is b to the e.
func pow(b, e int) int {
	out := 1
	for range e {
		out *= b
	}

	return out
}
