package main

import (
	"slices"
	"testing"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// headerRequest is a GET over HTTP/2 sending names, in a condition of its
// own site.
func headerRequest(label, method, site string, names ...string) *request {
	q := &request{
		label:  label,
		method: method,
		site:   site,
	}
	for _, n := range names {
		q.h2 = append(q.h2, profile.Header{
			Name:  n,
			Value: "1",
		})
	}
	return q
}

// placed is what table sends for base with the page's headers page (by
// name): the table's run where base has its first table name.
func placed(t *testing.T, table *profile.HeaderTable, base, page []string) []string {
	t.Helper()
	sent := tableSent(table, base, slices.Concat(base, page), nil)
	if sent == nil {
		t.Fatalf("the table cannot hold %q", base)
	}
	return sent
}

// kindsgen finds a browser's table from its captures: the shape, Own from
// the browser's headers between the page's, a header found inside the
// run elsewhere in the role that explains it (Safari's content-length,
// added after the page's), and that the redirects' requests do not follow
// it.
func TestDeriveTableFindsTheBrowsers(t *testing.T) {
	safari := &profile.HeaderTable{
		Hash: profile.HashCF,
		Own:  []string{"accept", "origin", "referer", "sec-fetch-dest", "sec-fetch-mode", "sec-fetch-site", "user-agent"},
		Late: []string{"content-length"},
	}
	so := []string{"sec-fetch-dest", "user-agent", "accept", "referer", "sec-fetch-site", "sec-fetch-mode", "accept-language"}
	cs := []string{"sec-fetch-site", "accept", "origin", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest", "accept-language"}
	put := []string{"sec-fetch-site", "accept", "origin", "sec-fetch-mode", "user-agent", "referer", "sec-fetch-dest", "content-length", "accept-language"}
	letters := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}
	reqs := []*request{
		headerRequest("fetch.so", "GET", profile.SiteSameOrigin, so...),
		headerRequest("fetch.cs", "GET", profile.SiteCrossSite, cs...),
		headerRequest("put.cs", "PUT", profile.SiteCrossSite, put...),
		headerRequest("letters.fetch.so", "GET", profile.SiteSameOrigin, placed(t, safari, so, letters)...),
		headerRequest("letters.fetch.cs", "GET", profile.SiteCrossSite, placed(t, safari, cs, letters)...),
		headerRequest("maxage.cs", "PUT", profile.SiteCrossSite, placed(t, safari, put, []string{"x-mimicry"})...),
	}
	// A redirect's request, every header reordered.
	hop := headerRequest("redirect-target.cs", "GET", profile.SiteCrossSite, "accept", "x-mimicry", "user-agent", "origin", "accept-language", "sec-fetch-site", "referer", "sec-fetch-mode", "sec-fetch-dest")
	hop.hop = true
	hopBase := headerRequest("fetch-redirect.cs", "GET", profile.SiteCrossSite, cs...)
	hopBase.hop = true
	got, err := deriveTable(append(reqs, hop, hopBase))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Hash != safari.Hash || got.PageFirst || !slices.Equal(got.Own, safari.Own) || !slices.Equal(got.Late, safari.Late) || len(got.AsPage) > 0 || got.Redirected {
		t.Errorf("got %+v, want %+v", got, safari)
	}
}

// The page's headers in one run among the browser's (Firefox) need no
// table: the lists' slot places them.
func TestDeriveTableNoneForOneRun(t *testing.T) {
	got, err := deriveTable([]*request{
		headerRequest("fetch.so", "GET", profile.SiteSameOrigin, "user-agent", "accept", "referer", "sec-fetch-dest"),
		headerRequest("pair-ab.fetch.so", "GET", profile.SiteSameOrigin, "user-agent", "accept", "referer", "x-a", "x-b", "sec-fetch-dest"),
	})
	if err != nil || got != nil {
		t.Errorf("got %+v, %v; want no table", got, err)
	}
}

// A group of requests with the page's headers needs no variant when the
// table places them from the order without them; the table not explaining
// one is an error, and a condition without them never captured leaves the
// group to a variant.
func TestTablePlaces(t *testing.T) {
	chrome := &profile.HeaderTable{
		Hash:      profile.HashBlink,
		Own:       []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "user-agent"},
		PageFirst: true,
	}
	base := []string{"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "sec-ch-ua-mobile", "accept"}
	run, ok := headerorder.TableOrder(chrome, base[:4], []string{"x-a", "x-b"})
	if !ok {
		t.Fatal("the table cannot hold the base")
	}
	right := headerRequest("multi.cs", "GET", profile.SiteSameOrigin, append(run, "accept")...)
	wrong := headerRequest("multi.cs", "GET", profile.SiteSameOrigin, slices.Concat(base, []string{"x-a", "x-b"})...)
	groups := map[condition][]*request{
		{}: {
			headerRequest("fetch.so", "GET", profile.SiteSameOrigin, base...),
		},
	}
	if ok, err := tablePlaces(chrome, groups, []*request{right}); !ok || err != nil {
		t.Errorf("the table's order: placed %v, %v", ok, err)
	}
	if _, err := tablePlaces(chrome, groups, []*request{wrong}); err == nil {
		t.Error("another order: no error")
	}
	if ok, err := tablePlaces(chrome, map[condition][]*request{}, []*request{right}); ok || err != nil {
		t.Errorf("no base: placed %v, %v; want a variant", ok, err)
	}
}
