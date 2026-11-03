package server

import (
	"slices"
	"strings"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// Bodiless is the bodiless scenario: fetch POST, PUT and PATCH and a form
// POST navigation, none with a body, first over HTTP/2 on a host that never
// advertises HTTP/3 (h3-c), then over HTTP/3 on one that does (h3-a): whether
// each browser sends content-length: 0, where in the header list, and how it
// ends the request's stream. See BodilessSteps.
var Bodiless = &Scenario{
	Name: "bodiless",
	Doc:  "fetch POST, PUT and PATCH and a form POST navigation with no body, over HTTP/2 (a host without Alt-Svc) and over HTTP/3 (a second host, after a warm-up and a navigation): content-length: 0 and its place in the header list, and the request's frames (END_STREAM on HEADERS or an empty DATA frame)",
	multi: h3Page{
		fixedHosts: fixedHosts{
			names: []string{
				h3PlainHost,
				h3Host,
			},
			runFor: 20 * time.Second,
		},
		steps:      BodilessSteps,
		plainHosts: []string{h3PlainHost},
	},
	h3: &h3Server{},
}

// BodilessCaptures returns p's existing bodiless captures in dir (own or
// inherited, with and without -h3-local-roots): what kindsgen derives the
// navigation's and fetch()'s emptyBody from.
func BodilessCaptures(dir string, p *profile.Spec) ([]capture.File, error) {
	return existingCaptures(dir, p, Bodiless)
}

// BodilessSteps lists the bodiless page's requests in order: on h3-c's /
// (HTTP/2 only), the bodiless fetches and a bodiless form POST to
// /h3/form-post.h2, whose page navigates to h3-a's /h3/start.h3; there a
// warm-up fetch and, 2 s later, a navigation to /h3/bodiless.h3 (over
// HTTP/3 by then, as in H3PageSteps), which makes the same requests, its
// form POST landing on /h3/form-post.h3, which asks for /done.
func BodilessSteps() []H3Step {
	const (
		startH3    = "/h3/start.h3"
		bodilessH3 = "/h3/bodiless.h3"
	)

	// bodiless are page's bodiless requests to host, 300 ms apart, ending
	// with the form POST to form-post.<proto>.
	bodiless := func(host, page, proto string) []H3Step {
		out := make([]H3Step, 0, 4)
		for _, m := range []string{"POST", "PUT", "PATCH"} {
			out = append(out, H3Step{
				Label:       "fetch-" + strings.ToLower(m) + "." + proto,
				Kind:        "fetch",
				Host:        host,
				Method:      m,
				Page:        page,
				PauseMillis: 300,
			})
		}

		return append(out, H3Step{
			Label:       "form-post." + proto,
			Kind:        "navigate",
			Host:        host,
			Method:      "POST",
			Page:        page,
			PauseMillis: 300,
		})
	}

	return slices.Concat(bodiless(h3PlainHost, "/", "h2"), []H3Step{
		{Label: "start.h3", Kind: "navigate", Host: h3Host, Page: "/h3/form-post.h2", PauseMillis: 300},
		{Label: "warm.h3", Kind: "fetch", Host: h3Host, Page: startH3},
		{Label: "bodiless.h3", Kind: "navigate", Host: h3Host, Page: startH3, PauseMillis: 2000},
	}, bodiless(h3Host, bodilessH3, "h3"))
}
