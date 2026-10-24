// Command kindsgen derives a profile's request kinds and context rules from
// its browser's captures, and writes them into the profile:
//
//	go run ./cmd/kindsgen -captures testdata/captures -profile ../profiles/data/chrome-155-macos.json
//
// It reads <name>.kinds.pagetrace.json.gz, <name>.kinds-http1.pagetrace.json.gz,
// <name>.kinds-plain.pagetrace.json.gz (non-secure variants),
// <name>.cookies.pagetrace.json.gz (what the kinds page cannot show:
// cross-site form POSTs, images redirected to the page's site) and
// <name>.cors.pagetrace.json.gz (context.preflight_cache, and the page's
// fetches: PUT and page-set-header variants).
//
// For each kind, the header order merges every captured request of its
// resource type (so slots seen only on some, like origin with a crossorigin
// attribute, keep their place); default values and the HTTP/2 weight come
// from the kind's own requests, a header sent by all of them with one value
// becoming a default and any other a slot. The navigation's lists gain the
// slots of the captured form POSTs. Requests over plain HTTP (kinds-plain)
// become non-secure variants, exactly as sent, with the kind's own values.
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
	pages, err := loadPages(dir, p.Name)
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
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return profile.Encode(p)
}
