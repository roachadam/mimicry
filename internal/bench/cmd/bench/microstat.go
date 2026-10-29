package main

import (
	"io"
	"strings"
)

// geomeanCaveats are benchstat's footnotes on a geomean row that compares
// nothing: over benchmarks that differ between columns (a client with n/a
// or discarded rows), or over values with a zero (the unresumed counts).
var geomeanCaveats = []string{"benchmark set differs from baseline", "summaries must be >0 to compute geomean"}

// microstat copies benchstat's output from in to out without the geomean
// rows benchstat itself flags as not comparable (geomeanCaveats), and
// without those footnotes. Footnote markers are numbered per table, so
// each table (a block between blank lines) is cleaned on its own.
func microstat(in io.Reader, out io.Writer) error {
	b, err := io.ReadAll(in)
	if err != nil {
		return err
	}

	blocks := strings.Split(string(b), "\n\n")

	for i, block := range blocks {
		blocks[i] = cleanTable(block)
	}

	_, err = io.WriteString(out, strings.Join(blocks, "\n\n"))

	return err
}

// cleanTable drops one table's flagged geomean rows and their footnote.
func cleanTable(block string) string {
	lines := strings.Split(block, "\n")

	var markers []string

	for _, l := range lines {
		if !containsAny(l, geomeanCaveats) {
			continue
		}

		if marker, _, ok := strings.Cut(l, " "); ok && marker != "" {
			markers = append(markers, marker)
		}
	}

	if len(markers) == 0 {
		return block
	}

	out := lines[:0]

	for _, l := range lines {
		if containsAny(l, geomeanCaveats) {
			continue
		}

		if strings.HasPrefix(l, "geomean") && containsAny(l, markers) {
			continue
		}

		out = append(out, l)
	}

	return strings.Join(out, "\n")
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}

	return false
}
