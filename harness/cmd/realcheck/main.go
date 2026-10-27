// Command realcheck compares mimicry with the real browsers on this machine
// as public fingerprinting services see them, and tries a few sites behind
// bot-management vendors. It is opt-in and never part of go test: it needs
// the network, and the browser part takes over the screen.
//
//	go run ./cmd/realcheck                                  # mimicry and the sites
//	go run ./cmd/realcheck -browsers chrome,firefox,safari  # the real browsers too
//	go run ./cmd/realcheck -echo=false -sites=false -profiles none   # compare only
//
// Echo endpoints (endpoints) answer with what they saw of the request:
// JA3, JA4, peetprint, the Akamai HTTP/2 fingerprint, HTTP/2 frames,
// header order and values, and over HTTP/3 the QUIC hello, transport
// parameters and HTTP/3 settings. Every built-in profile and every browser
// named in -browsers visits each one as a typed URL (mimicry: a plain
// request, a navigation), and the HTTP/3 ones again until the echo reports
// HTTP/3, as Alt-Svc moves each client there.
//
// Browsers: Chrome and Firefox run on throwaway profiles with their
// defaults and are driven over DevTools and WebDriver BiDi; Safari opens a
// window in the user's Safari, driven by AppleScript (it can read the
// page's text, not its status or cookies). Run them in one short batch.
//
// Sites (sites) are plain homepage GETs, one per client per site and a few
// seconds apart, from each browser named in -browsers, each profile with
// a fresh client and cookie jar, then Go's net/http default client for
// contrast. What each got back is classified by its challenge markers.
//
// Raw answers go to OUT/echo/<client>/<endpoint>.json and
// OUT/sites/<client>.json, so runs can be split (browsers once, mimicry
// again after a fix) and compared together: the comparison is always made
// from everything in OUT, to OUT/compare.md (summary, mismatches) and
// OUT/compare-full.md (every field). OUT holds this machine's public IP;
// it is not meant to be committed.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/browser"
	"github.com/roachadam/mimicry/profiles"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "realcheck:", err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("out", filepath.Join(os.TempDir(), "mimicry-realcheck"), "directory for raw answers and the comparison")
	echo := flag.Bool("echo", true, "visit the echo endpoints")
	doSites := flag.Bool("sites", true, "visit the protected sites")
	browserList := flag.String("browsers", "", "comma-separated installed browsers to drive (chrome, firefox, safari); takes over the screen")
	profileList := flag.String("profiles", "", "comma-separated built-in profile names (default every supported one; \"none\" for none)")
	noGo := flag.Bool("no-go", false, "skip Go's net/http default client on the sites")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("writing to %s", *out)
	names := profiles.Supported()
	switch *profileList {
	case "":
	case "none":
		names = nil
	default:
		names = strings.Split(*profileList, ",")
	}
	var bs []*browser.Browser
	if *browserList != "" {
		found := map[string]*browser.Browser{}
		for _, b := range browser.Find() {
			found[b.Name] = b
		}
		for _, n := range strings.Split(*browserList, ",") {
			b := found[n]
			if b == nil {
				return fmt.Errorf("browser %q is not installed", n)
			}
			bs = append(bs, b)
		}
	}
	// Browsers first, so no earlier client's requests from this address
	// colour what a site shows them; Go's client, the one most likely to
	// be flagged, last.
	for _, b := range bs {
		if err := runBrowser(ctx, b, *out, *echo, *doSites); err != nil {
			return fmt.Errorf("%s: %w", b.Name, err)
		}
	}
	for _, n := range names {
		p, err := profiles.ByName(n)
		if err != nil {
			return err
		}
		if *echo {
			if err := echoProfile(ctx, n, p, *out); err != nil {
				return err
			}
		}
	}
	if *doSites {
		if err := sitesClients(ctx, names, !*noGo, *out); err != nil {
			return err
		}
	}
	return compare(*out)
}
