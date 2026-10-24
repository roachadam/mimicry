package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

// Every built-in profile is what kindsgen derives from the committed
// captures: no hand edits, no generator drift.
func TestRegenerateReproducesProfiles(t *testing.T) {
	paths, err := filepath.Glob("../../../profiles/data/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no profiles found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path) //nolint:gosec // a profile in the repository
			if err != nil {
				t.Fatal(err)
			}
			got, err := regenerate(data, "../../testdata/captures")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("regenerating %s changes it; rerun kindsgen on it and review the diff", path)
			}
		})
	}
}

// plainRequest is a GET over plain HTTP with header lines.
func plainRequest(label string, lines ...string) *request {
	q := &request{
		method: "GET",
		plain:  true,
		h1:     h1Fields(lines),
	}
	q.label, q.hop = strings.CutSuffix(label, ".hop")
	q.base, _, _ = strings.Cut(q.label, ".")
	q.site = labelSite(q.label, q.hop)
	return q
}

// A non-secure variant takes its values from the kind's own plain
// requests, in its condition where captured, else in any; type-mates'
// values only for headers the kind's own never send.
func TestInsecureVariantValues(t *testing.T) {
	own := []*request{
		plainRequest("image-markup.so", "Accept: image/*", "Priority: u=5, i"),
		plainRequest("image-markup.ss", "Accept: image/*", "Priority: u=4"),
	}
	all := slices.Concat(own, []*request{
		plainRequest("image.cs", "Accept: image/*", "Priority: u=5, i", "X-Mate: 1"),
		plainRequest("image-high.cs", "Accept: image/*", "Priority: i", "X-Mate: 1"),
		plainRequest("image.ss", "Accept: image/*", "Priority: u=6"),
	})
	none := func(bool) func(string) string {
		return func(string) string {
			return ""
		}
	}
	_, _, variants, err := orders(own, all, nil, nil, none)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]profile.Header{
		// The kind's own requests in the condition.
		profile.SiteSameSite: {
			{
				Name:  "Accept",
				Value: "image/*",
			},
			{
				Name:  "Priority",
				Value: "u=4",
			},
		},
		// No own request in the condition: the kind's own plain requests
		// disagree on Priority, so it is a slot; X-Mate only type-mates
		// send.
		profile.SiteCrossSite: {
			{
				Name:  "Accept",
				Value: "image/*",
			},
			{
				Name: "Priority",
			},
			{
				Name:  "X-Mate",
				Value: "1",
			},
		},
	}
	for _, v := range variants {
		if !v.Insecure {
			continue
		}
		w, ok := want[v.Site]
		if !ok {
			continue
		}
		if !slices.Equal(v.HTTP1, w) {
			t.Errorf("site %q: got %+v, want %+v", v.Site, v.HTTP1, w)
		}
		delete(want, v.Site)
	}
	for site := range want {
		t.Errorf("no non-secure variant for site %q", site)
	}
}

// One own plain request: a condition it was never sent in still takes its
// values, not the type-mates' commonest.
func TestInsecureVariantValuesFromOwnElsewhere(t *testing.T) {
	own := []*request{
		plainRequest("image-markup.so", "Priority: u=5, i"),
	}
	all := slices.Concat(own, []*request{
		plainRequest("image.cs", "Priority: u=5, i"),
		plainRequest("image-high.cs", "Priority: i"),
	})
	none := func(bool) func(string) string {
		return func(string) string {
			return ""
		}
	}
	_, _, variants, err := orders(own, all, nil, nil, none)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(variants, func(v profile.Variant) bool {
		return v.Insecure && v.Site == profile.SiteCrossSite
	})
	if i < 0 {
		t.Fatalf("no non-secure cross-site variant in %+v", variants)
	}
	want := []profile.Header{
		{
			Name:  "Priority",
			Value: "u=5, i",
		},
	}
	if got := variants[i].HTTP1; !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMergeOrder(t *testing.T) {
	got, err := mergeOrder([][]string{
		{"a", "c"},
		{"a", "b", "c"},
		{"b", "d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c", "d"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, seqs := range [][][]string{
		{{"a", "b"}, {"b", "a"}},
		{{"a", "b"}, {"b", "c"}, {"c", "a"}},
	} {
		if got, err := mergeOrder(seqs); err == nil {
			t.Errorf("%q: got %q, want an error", seqs, got)
		}
	}
}

// corsPage is a cors page whose maxage.cs requests are seq ("METHOD
// access-control-request-headers"), on one connection per element of
// conns.
func corsPage(conns ...[]string) *capture.Page {
	page := &capture.Page{}
	for _, seq := range conns {
		tr := &h2trace.Trace{}
		for i, s := range seq {
			method, acrh, _ := strings.Cut(s, " ")
			r := h2trace.Request{
				Stream: uint32(2*i + 1), //nolint:gosec // a handful of test streams
				Path:   "/p/maxage.cs",
				Instructions: []h2trace.Instruction{
					{
						Name:  ":method",
						Value: method,
					},
				},
			}
			if acrh != "" {
				r.Instructions = append(r.Instructions, h2trace.Instruction{
					Name:  "access-control-request-headers",
					Value: acrh,
				})
			}
			tr.Requests = append(tr.Requests, r)
		}
		page.H2 = append(page.H2, tr)
	}
	return page
}

func TestPreflightCache(t *testing.T) {
	start := []string{"OPTIONS x-mimicry", "GET ", "GET "}
	for _, tc := range []struct {
		name  string
		pages []*capture.Page
		want  string // "" for an error
	}{
		{
			name: "replace",
			pages: []*capture.Page{
				corsPage(slices.Concat(start, []string{"OPTIONS x-other", "GET ", "OPTIONS x-mimicry", "GET "})),
			},
			want: profile.PreflightCacheReplace,
		},
		{
			name: "merge",
			pages: []*capture.Page{
				corsPage(slices.Concat(start, []string{"OPTIONS x-other", "GET ", "GET ", "PUT "})),
			},
			want: profile.PreflightCacheMerge,
		},
		{
			name: "something else after the X-Other request",
			pages: []*capture.Page{
				corsPage(slices.Concat(start, []string{"OPTIONS x-other", "GET ", "PUT "})),
			},
		},
		{
			name: "no X-Other request",
			pages: []*capture.Page{
				corsPage(slices.Concat(start, []string{"OPTIONS x-other", "OPTIONS x-mimicry", "GET "})),
			},
		},
		{
			name: "on two connections",
			pages: []*capture.Page{
				corsPage(slices.Concat(start, []string{"OPTIONS x-other", "GET "}), []string{"GET "}),
			},
		},
		{
			name: "samples disagree",
			pages: []*capture.Page{
				corsPage(slices.Concat(start, []string{"OPTIONS x-other", "GET ", "OPTIONS x-mimicry"})),
				corsPage(slices.Concat(start, []string{"OPTIONS x-other", "GET ", "GET "})),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := preflightCache(tc.pages)
			switch {
			case tc.want == "" && err == nil:
				t.Errorf("got %q, want an error", got)
			case tc.want != "" && err != nil:
				t.Errorf("got error %v, want %q", err, tc.want)
			case got != tc.want:
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// h2Page is a page with one HTTP/2 connection carrying GETs of paths.
func h2Page(paths ...string) *capture.Page {
	tr := &h2trace.Trace{}
	for i, p := range paths {
		tr.Requests = append(tr.Requests, h2trace.Request{
			Stream: uint32(2*i + 1), //nolint:gosec // a handful of test streams
			Path:   p,
			Instructions: []h2trace.Instruction{
				{
					Name:  ":method",
					Value: "GET",
				},
				{
					Name:  "accept",
					Value: "*/*",
				},
			},
		})
	}
	return &capture.Page{
		H2: []*h2trace.Trace{
			tr,
		},
	}
}

// h1Page is a page of HTTP/1.1 GETs of paths.
func h1Page(paths ...string) *capture.Page {
	page := &capture.Page{}
	for _, p := range paths {
		page.H1 = append(page.H1, capture.H1Request{
			Conn: 1,
			Line: "GET " + p + " HTTP/1.1",
			Headers: []string{
				"Accept: */*",
			},
		})
	}
	return page
}

// A kinds request captured over one protocol only cannot be reconciled.
func TestCollectRefusesUnmatchedRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		h2, h1 *capture.Page
		want   string // in the error; "" for none
	}{
		{
			name: "matched",
			h2:   h2Page("/k/a.so", "/k/b.cs", "/done"),
			h1:   h1Page("/", "/k/b.cs", "/k/a.so"),
		},
		{
			name: "HTTP/2 only",
			h2:   h2Page("/k/a.so", "/k/b.cs"),
			h1:   h1Page("/k/a.so"),
			want: "GET b.cs captured over HTTP/2 only",
		},
		{
			name: "HTTP/1.1 only",
			h2:   h2Page("/k/a.so"),
			h1:   h1Page("/k/a.so", "/k/b.cs"),
			want: "GET b.cs captured over HTTP/1.1 only",
		},
		{
			name: "twice over HTTP/1.1",
			h2:   h2Page("/k/a.so"),
			h1:   h1Page("/k/a.so", "/k/a.so"),
			want: "GET a.so captured twice over HTTP/1.1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reqs, err := collect(&pages{
				kinds: []*capture.Page{
					tc.h2,
				},
				kindsH1: []*capture.Page{
					tc.h1,
				},
			})
			switch {
			case tc.want == "" && err != nil:
				t.Fatal(err)
			case tc.want == "":
				for _, r := range reqs {
					if len(r.h1) != 1 || len(r.h2) != 1 {
						t.Errorf("%s: h2 %+v, h1 %+v", r.label, r.h2, r.h1)
					}
				}
			case err == nil || !strings.Contains(err.Error(), tc.want):
				t.Errorf("got error %v, want one with %q", err, tc.want)
			}
		})
	}
}

// A header the page set makes a request CORS-checked and page-set; Origin
// only CORS-checked; Content-Type neither.
func TestConditionPageSet(t *testing.T) {
	for _, tc := range []struct {
		header        string
		cors, pageSet bool
	}{
		{
			header:  "x-mimicry",
			cors:    true,
			pageSet: true,
		},
		{
			header: "origin",
			cors:   true,
		},
		{
			header: "content-type",
		},
	} {
		r := &request{
			method: "GET",
			h2: []profile.Header{
				{
					Name:  "accept",
					Value: "*/*",
				},
				{
					Name:  tc.header,
					Value: "1",
				},
			},
		}
		if c := r.condition(); c.cors != tc.cors || c.pageSet != tc.pageSet {
			t.Errorf("%s: cors %v, page set %v; want %v, %v", tc.header, c.cors, c.pageSet, tc.cors, tc.pageSet)
		}
	}
}

// fetchRequest is a cors page request: method, path and header fields.
func fetchRequest(method, path string, fields ...string) h2trace.Request {
	r := h2trace.Request{
		Path: path,
		Instructions: []h2trace.Instruction{
			{
				Name:  ":method",
				Value: method,
			},
		},
	}
	for _, f := range fields {
		name, value, _ := strings.Cut(f, ": ")
		r.Instructions = append(r.Instructions, h2trace.Instruction{
			Name:  name,
			Value: value,
		})
	}
	return r
}

// The cors page's fetches are the fetch kind's, whatever header the page
// set (a header table places each by name), but its preflights; a path no
// step has is a redirect's target.
func TestCollectCORSFetches(t *testing.T) {
	page := &capture.Page{
		H2: []*h2trace.Trace{
			{
				Requests: []h2trace.Request{
					fetchRequest("OPTIONS", "/p/put.cs", "access-control-request-method: PUT"),
					fetchRequest("PUT", "/p/put.cs", "origin: https://a.test", "content-length: 0"),
					fetchRequest("GET", "/p/multi.cs", "x-b: 1", "x-a: 1", "origin: https://a.test"),
					fetchRequest("GET", "/p/maxage.cs", "x-other: 1", "origin: https://a.test"),
					fetchRequest("GET", "/p/redirect-target.cs", "origin: https://a.test", "x-mimicry: 1"),
				},
			},
		},
	}
	reqs, err := collect(&pages{
		cors: []*capture.Page{
			page,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(reqs))
	for _, r := range reqs {
		got = append(got, fmt.Sprintf("%s %s %+v", r.method, r.base, r.condition()))
	}
	want := []string{
		fmt.Sprintf("PUT cors-put %+v", condition{
			method: "PUT",
			site:   profile.SiteCrossSite,
			cors:   true,
		}),
		fmt.Sprintf("GET cors-multi %+v", condition{
			site:    profile.SiteCrossSite,
			cors:    true,
			pageSet: true,
		}),
		fmt.Sprintf("GET cors-maxage %+v", condition{
			site:    profile.SiteCrossSite,
			cors:    true,
			pageSet: true,
		}),
		fmt.Sprintf("GET cors-redirect-target %+v", condition{
			site:       profile.SiteCrossSite,
			cors:       true,
			pageSet:    true,
			redirected: true,
		}),
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// contextRequests are one sample's requests that show the context rules,
// the omit fetch on connection omitConn, the others on 0.
func contextRequests(sample, omitConn int, access string) []*request {
	labels := []string{"image-cors.so", "fetch.so", "fetch-omit.so", "iframe.cs", "script.cs", "font-css.so"}
	out := make([]*request, 0, len(labels))
	for _, label := range labels {
		r := &request{
			label:  label,
			method: "GET",
			sample: sample,
		}
		if label == "fetch-omit.so" {
			r.conn = omitConn
		}
		if label == "script.cs" && access != "" {
			r.h2 = []profile.Header{
				{
					Name:  "sec-fetch-storage-access",
					Value: access,
				},
			}
		}
		out = append(out, r)
	}
	return out
}

func TestContextOfEverySample(t *testing.T) {
	got, err := contextOf(slices.Concat(contextRequests(0, 1, "active"), contextRequests(1, 2, "active")))
	if err != nil {
		t.Fatal(err)
	}
	if !got.AnonymousConnections || got.StorageAccess != "active" {
		t.Errorf("got %+v, want anonymous connections and storage access active", *got)
	}
	for name, reqs := range map[string][]*request{
		"connections":    slices.Concat(contextRequests(0, 1, "active"), contextRequests(1, 0, "active")),
		"storage access": slices.Concat(contextRequests(0, 1, "active"), contextRequests(1, 1, "")),
		"two values": slices.Concat(contextRequests(0, 1, "active"), []*request{
			{
				label:  "image.cs",
				method: "GET",
				h2: []profile.Header{
					{
						Name:  "sec-fetch-storage-access",
						Value: "none",
					},
				},
			},
		}),
	} {
		if got, err := contextOf(reqs); err == nil {
			t.Errorf("%s: got %+v, want an error", name, *got)
		}
	}
}
