package capture

import (
	"fmt"
	"slices"

	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

// OpeningRecords is how p's browser cuts a new HTTP/2 connection's opening
// into TLS records (profile http2.openingRecords), from every connection
// in p's captures in dir whose records were kept and whose first request
// was waiting as it opened (h2trace.Opening): they must all agree.
// Connections the browser opened ahead of their request are left out:
// mimicry never does. Nil when no connection measured it.
func OpeningRecords(dir string, p *profile.Profile) ([][]string, error) {
	var (
		traces []*h2trace.Trace
		at     []string
	)

	files, err := Files(dir, p, ".h2trace.json")
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		loaded, err := h2trace.Load(f.Path)
		if err != nil {
			return nil, err
		}

		for i, tr := range loaded {
			traces = append(traces, tr)
			at = append(at, fmt.Sprintf("%s: sample %d", f.Path, i+1))
		}
	}

	pages, err := Files(dir, p, ".pagetrace.json.gz")
	if err != nil {
		return nil, err
	}

	for _, f := range pages {
		loaded, err := LoadPages(f.Path)
		if err != nil {
			return nil, err
		}

		for i, page := range loaded {
			for j, tr := range page.H2 {
				traces = append(traces, tr)
				at = append(at, fmt.Sprintf("%s: sample %d, HTTP/2 connection %d", f.Path, i+1, j+1))
			}
		}
	}

	var (
		want   [][]string
		wantAt string
	)

	for i, tr := range traces {
		got, ready, err := h2trace.Opening(tr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at[i], err)
		}

		if !ready {
			continue
		}

		if want == nil {
			want, wantAt = got, at[i]

			continue
		}

		if !slices.EqualFunc(got, want, slices.Equal) {
			return nil, fmt.Errorf("%s: opening records %v, %s's %v", at[i], got, wantAt, want)
		}
	}

	return want, nil
}
