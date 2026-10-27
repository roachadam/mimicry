// Command diff compares two captures of one connection (ClientHello,
// HTTP/2 preface, first request's headers) and lists every difference.
// Other profile sections are not compared. It exits 1 when they differ.
//
//	go run ./cmd/diff WANT.json GOT.json
package main

import (
	"fmt"
	"os"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/profile"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: diff WANT.json GOT.json")
		os.Exit(2)
	}

	want, err := load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "diff:", err)
		os.Exit(2)
	}

	got, err := load(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "diff:", err)
		os.Exit(2)
	}

	wf, gf := capture.Compute(want), capture.Compute(got)
	fmt.Printf("want %s  JA4 %s  Akamai %s\n", want.Name, wf.JA4, wf.Akamai)
	fmt.Printf("got  %s  JA4 %s  Akamai %s\n", got.Name, gf.JA4, gf.Akamai)

	diffs := diff.Compare(want, got)
	if len(diffs) == 0 {
		fmt.Println("no differences")

		return
	}

	fmt.Printf("%d differences:\n", len(diffs))

	for _, d := range diffs {
		fmt.Println(d)
	}

	os.Exit(1)
}

func load(path string) (*profile.Profile, error) {
	data, err := os.ReadFile(path) //nolint:gosec // reading the files named on the command line is the point
	if err != nil {
		return nil, err
	}

	p, err := profile.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return p, nil
}
