// Command kindsgen derives a profile's request kinds and context rules from
// its browser's captures, and writes them into the profile:
//
//	go run ./cmd/kindsgen -captures testdata/captures -profile ../profiles/data/chrome-155-macos.json
//
// It reads <name>.kinds.pagetrace.json.gz, <name>.kinds-http1.pagetrace.json.gz,
// <name>.kinds-plain.pagetrace.json.gz (non-secure variants),
// <name>.cookies.pagetrace.json.gz (what the kinds page cannot show:
// cross-site form POSTs, images redirected to the page's site),
// <name>.cors.pagetrace.json.gz (context.preflight_cache, and the page's
// fetches: PUT and page-set-header variants), <name>.cors-edge.pagetrace.json.gz
// (fetch redirects: PUT, DELETE and POST answered 301, 302 and 303, and a
// long chain), <name>.redirects2.pagetrace.json.gz (redirects of every
// kind, form POSTs, a navigation chain, PATCH preflights), any
// <name>.nav-redirect-*.pagetrace.json.gz (typed navigations whose start
// URL redirects, and a page's navigations redirected to another origin),
// <name>.headers-order.pagetrace.json.gz (context.header_table), and
// <name>.websocket.pagetrace.json.gz and <name>.websocket-h2.pagetrace.json.gz
// (the websocket kind: handshakes over HTTP/1.1 and as HTTP/2 extended
// CONNECTs, whose :protocol must follow the profile's pseudo-header
// order, and ws:// from a non-secure page).
//
// Each is the profile's own capture or, for a scenario its captures_from
// inherits, that profile's (capture.Inherited), its user-agent and
// sec-ch-ua values rewritten to the profile's version before anything is
// derived from them; a sampleGaps or h3PriorityGaps pin holds for the
// profile whose capture shows it and every profile inheriting that capture.
//
// For each kind, the header order merges every captured request of its
// resource type (so slots seen only on some, like origin with a crossorigin
// attribute, keep their place); default values and the HTTP/2 weight come
// from the kind's own requests, a header sent by all of them with one value
// becoming a default and any other a slot. The navigation's lists gain the
// slots of the captured form POSTs. Requests over plain HTTP (kinds-plain)
// become non-secure variants, exactly as sent, with the kind's own values.
// A request made by following a redirect is never folded into another
// order: its condition also says whether the redirect went to another
// origin (host) than the request it answered, and whether that request
// carried Origin, both read from that request's capture.
// It refuses to write when the captures disagree (two orders that cannot be
// merged, weights that differ, samples that show different rules) or cannot
// be reconciled (a request captured over one protocol only).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/roachadam/mimicry/profile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kindsgen:", err)
		os.Exit(1)
	}
}

func run() error {
	captures := flag.String("captures", "testdata/captures", "directory with the profile's kinds, kinds-http1, kinds-plain, cookies and cors page captures (<name>.<scenario>.pagetrace.json.gz)")
	path := flag.String("profile", "", "profile JSON to update (required); its name picks the captures")
	flag.Parse()
	if *path == "" {
		flag.Usage()
		os.Exit(2)
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	out, err := regenerate(data, *captures)
	if err != nil {
		return err
	}
	return os.WriteFile(*path, out, 0o600) //nolint:gosec // rewriting the profile named on the command line is the point
}

// regenerate returns the profile JSON data with its kinds, context and
// navigation variants derived from the captures in dir.
func regenerate(data []byte, dir string) ([]byte, error) {
	p, err := profile.Parse(data)
	if err != nil {
		return nil, err
	}
	pages, err := loadPages(dir, p)
	if err != nil {
		return nil, err
	}
	reqs, err := collect(pages)
	if err != nil {
		return nil, err
	}
	if err := derive(p, reqs); err != nil {
		return nil, err
	}
	if p.Context.PreflightCache, err = preflightCache(pages.cors); err != nil {
		return nil, err
	}
	if err := checkProtocolPlace(p, pages.websocketH2); err != nil {
		return nil, err
	}
	if p.HTTP3 != nil {
		if err := deriveH3(p, dir); err != nil {
			return nil, err
		}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return profile.Encode(p)
}
