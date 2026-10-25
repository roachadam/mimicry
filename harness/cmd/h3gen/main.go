// Command h3gen derives a profile's quic and http3 sections from its
// browser's HTTP/3 captures, and writes them into the profile:
//
//	go run ./cmd/h3gen -captures testdata/captures -profile ../profiles/data/chrome-155-macos.json
//
// It reads every <name>.h3-discovery and <name>.h3-page capture, with and
// without -h3-local-roots (server.H3ProfileCaptures; not the h3 edge
// scenarios' failures and other versions). Every QUIC
// connection's first flight counts, failed handshakes included (they show
// the same Initial packets, ClientHello and transport parameters); what
// follows the handshake comes from connections that completed it, and
// discovery from the h3-discovery captures in which some did. Each value
// is checked against every connection of every sample, and h3gen refuses
// to write when one disagrees, unless the difference is pinned in
// sampleGaps with why (and a pinned one that no longer appears is an
// error too). http3.headers is kindsgen's and kept as it is. Run hellogen
// first: tls.quic must describe the connections' ClientHellos.
//
// What it cannot measure stays out of the profile and is documented on
// profile.QUIC and profile.HTTP3: credit growth, key updates, Safari's
// QPACK with a dynamic table, its resumption and how it ends a connection.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/roachadam/mimicry/profile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "h3gen:", err)
		os.Exit(1)
	}
}

func run() error {
	captures := flag.String("captures", "testdata/captures", "directory with the profile's h3-discovery and h3-page captures")
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

// regenerate returns the profile JSON data with its quic and http3
// sections derived from the h3 captures in dir; without any, it leaves
// both out.
func regenerate(data []byte, dir string) ([]byte, error) {
	p, err := profile.Decode(data)
	if err != nil {
		return nil, err
	}
	set, err := load(dir, p.Name)
	if err != nil {
		return nil, err
	}
	if len(set.conns) == 0 {
		p.QUIC, p.HTTP3 = nil, nil
	} else {
		// http3.headers, high and low are kindsgen's.
		var kept profile.HTTP3
		if p.HTTP3 != nil {
			kept = *p.HTTP3
		}
		if p.QUIC, err = deriveQUIC(set, p); err != nil {
			return nil, fmt.Errorf("quic: %w", err)
		}
		if p.HTTP3, err = deriveHTTP3(set); err != nil {
			return nil, fmt.Errorf("http3: %w", err)
		}
		p.HTTP3.Headers, p.HTTP3.High, p.HTTP3.Low = kept.Headers, kept.High, kept.Low
		if err := set.unusedGaps(); err != nil {
			return nil, err
		}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return profile.Encode(p)
}
