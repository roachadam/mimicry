// Command tracestat reports, for each trace file, which features its samples
// agree on and which vary (h2trace.Compare). Varying features are behaviour a
// profile must either model or list as a gap.
//
//	go run ./cmd/tracestat testdata/captures/*.h2trace.json
package main

import (
	"fmt"
	"os"

	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tracestat TRACE.h2trace.json...")
		os.Exit(2)
	}

	failed := false
	for _, path := range os.Args[1:] {
		samples, err := h2trace.Load(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tracestat:", err)

			failed = true

			continue
		}

		variations := h2trace.Compare(samples)
		f := h2trace.Extract(samples[0])
		exact, measured := f.Exact, f.Measured
		fmt.Printf("%s: %d samples, %d features, %d vary\n", path, len(samples), len(exact)+len(measured), len(variations))

		for _, v := range variations {
			fmt.Printf("  %s\n", v.Feature)

			for i, value := range v.Values {
				fmt.Printf("    sample %d: %s\n", i+1, truncate(value, 160))
			}
		}
	}

	if failed {
		os.Exit(1)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "…"
}
