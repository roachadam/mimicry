package capture

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profile"
)

// OpeningRecords is how p's browser cuts a new HTTP/2 connection's opening
// into TLS records (profile http2.openingRecords), from every connection
// in p's captures in dir, and its own testing-build captures, whose
// records were kept and whose first request was waiting as it opened
// (h2trace.Opening): they must all agree. Connections the browser opened
// ahead of their request are left out: mimicry never does; so are those
// openingGaps pins. Nil when no connection measured it.
func OpeningRecords(dir string, p *profile.Spec) ([][]string, error) {
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
			at = append(at, fmt.Sprintf("%s: sample %d", filepath.Base(f.Path), i+1))
		}
	}

	pages, err := Files(dir, p, ".pagetrace.json.gz")
	if err != nil {
		return nil, err
	}

	// Testing-build captures count here alone, inherited as their
	// scenario is.
	testing, err := testingPages(dir, p)
	if err != nil {
		return nil, err
	}

	pages = append(pages, testing...)

	for _, f := range pages {
		loaded, err := LoadPages(f.Path)
		if err != nil {
			return nil, err
		}

		for i, page := range loaded {
			for j, tr := range page.H2 {
				traces = append(traces, tr)
				at = append(at, fmt.Sprintf("%s: sample %d, HTTP/2 connection %d", filepath.Base(f.Path), i+1, j+1))
			}
		}
	}

	var (
		want   [][]string
		wantAt string
	)

	pinned := map[string]bool{}

	for i, tr := range traces {
		got, ready, err := h2trace.Opening(tr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at[i], err)
		}

		if _, ok := openingGaps[at[i]]; ok {
			pinned[at[i]] = true
			continue
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

	for key := range openingGaps {
		file, _, _ := strings.Cut(key, ":")
		if !pinned[key] && slices.ContainsFunc(at, func(a string) bool {
			return strings.HasPrefix(a, file+":")
		}) {
			return nil, fmt.Errorf("openingGaps pins %q, which no longer appears: remove it", key)
		}
	}

	return want, nil
}

// openingGaps are connections OpeningRecords leaves out, by capture file
// and position, with why: a first request ready only just after the
// opening was written, in the preface's millisecond, which the trace's
// timing cannot tell from a waiting one.
var openingGaps = map[string]string{
	"safari-26-ios.h3-broken.pagetrace.json.gz: sample 1, HTTP/2 connection 1":     "the page's navigation, which devicectl hands Safari, went in a record of its own after the opening, in the preface's millisecond (the other samples' came 34 and 101 ms later); a waiting request goes with the opening (every other connection)",
	"chrome-157-ios.cookies.pagetrace.json.gz: sample 1, HTTP/2 connection 1":      chromeIOSOpening,
	"chrome-157-ios.h3-broken.pagetrace.json.gz: sample 1, HTTP/2 connection 1":    chromeIOSOpening,
	"chrome-157-ios.h3-discovery.pagetrace.json.gz: sample 1, HTTP/2 connection 1": chromeIOSOpening,
}

// chromeIOSOpening is why openingGaps pins three first connections of
// chrome-157-ios, as safari-26-ios's h3-broken sample 1.
const chromeIOSOpening = "the navigation devicectl hands Chrome went in a record of its own after the opening, in the preface's millisecond (samples 2 and 3 of each: after the server's SETTINGS, 19 to 47 ms later); a waiting request goes with the opening (every other connection)"

// testingPages lists the testing-build page captures in dir p is checked
// against: its own for the scenarios it does not inherit, its sources'
// for those it does.
func testingPages(dir string, p *profile.Spec) ([]File, error) {
	const suffix = forTesting + ".pagetrace.json.gz"

	names := []string{p.Name}
	for _, from := range p.CapturesFrom {
		if !slices.Contains(names, from) {
			names = append(names, from)
		}
	}

	var out []File

	for _, name := range names {
		matches, err := filepath.Glob(filepath.Join(dir, name+".*"+suffix))
		if err != nil {
			return nil, err
		}

		for _, path := range matches {
			sc := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), name+"."), suffix)
			if Source(p, sc) == name {
				out = append(out, File{
					Path:     path,
					Scenario: sc,
					Source:   name,
				})
			}
		}
	}

	return out, nil
}
