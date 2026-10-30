// Command kindsgen derives a profile's request kinds and context rules from
// its browser's captures, and writes them into the profile:
//
//	go run ./cmd/kindsgen -captures testdata/captures -profile ../../profiles/data/chrome-155-macos.json
//
// It reads the profile's kinds, cookies, cors, cors-edge-cookies, redirects2,
// nav-redirect, headers-order, websocket, h3-discovery and h3-page page
// captures (or the inherited ones, rewritten to the profile's version). It
// refuses to write when the captures disagree or a request was captured
// over one protocol only.
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
	captures := flag.String("captures", "testdata/captures", "directory of captures (testdata/captures, or a refresh overlay); the profile's name picks its files")
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
