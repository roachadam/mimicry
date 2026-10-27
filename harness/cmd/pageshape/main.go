// Command pageshape extracts the shape of a real page load from a Chrome net
// log, for the capture server to replay with synthetic values:
//
//	go run ./cmd/pageshape -site nike.com -out testdata/pages/nike.com.json NETLOG
//
// Record the log with a throwaway profile: --log-net-log=FILE
// --net-log-capture-mode=IncludeSensitive (cookie sizes need it). Only
// lengths and character classes leave the log, never values.
package main

import (
	"cmp"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/roachadam/mimicry/harness/internal/pageshape"
	"github.com/roachadam/mimicry/profiles"
)

func main() {
	site := flag.String("site", "", "registrable domain of the visited page (required)")
	browser := flag.String("browser", profiles.Chrome().Name, "profile name of the Chrome that recorded the log")
	date := flag.String("date", time.Now().Format(time.DateOnly), "recording date")
	out := flag.String("out", "", "output file (required)")

	flag.Parse()

	if *site == "" || *out == "" || flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	p, err := pageshape.FromNetLog(data, *site, *browser, *date)
	if err != nil {
		log.Fatal(err)
	}

	if len(p.Requests) == 0 {
		log.Fatalf("no requests with top frame %s in the log", *site)
	}

	if err := pageshape.Write(*out, p); err != nil {
		log.Fatal(err)
	}

	fmt.Println(summary(p))
}

func summary(p *pageshape.Page) string {
	protocols := map[string]int{}
	cookies := map[int]bool{}

	var maxCookie int

	for _, r := range p.Requests {
		protocols[r.Protocol]++

		maxCookie = max(maxCookie, r.CookieBytes)

		for _, c := range r.SetCookies {
			cookies[c.ID] = true
		}
	}

	keys := slices.SortedFunc(maps.Keys(protocols), cmp.Compare)

	var s strings.Builder
	fmt.Fprintf(&s, "%s: %d requests, %d hosts, %d cookies (largest cookie header %d bytes, %d script cookies unplaceable); protocols:",
		p.Site, len(p.Requests), len(p.Hosts), len(cookies), maxCookie, p.ScriptCookiesDropped)

	for _, k := range keys {
		fmt.Fprintf(&s, " %s=%d", k, protocols[k])
	}

	return s.String()
}
