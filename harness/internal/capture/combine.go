package capture

import (
	"fmt"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/profile"
)

// Combine sets every sample's extension order from how the samples compare
// (shuffled if any two orders differ, fixed otherwise) and checks the samples
// agree on everything else. It returns the first sample, holding every
// sample's trace in SampleTraces; how far those agree is h2trace.Compare's
// job, since timing legitimately varies between connections.
func Combine(samples []*Capture) (*Capture, error) {
	first := samples[0]
	order := profile.ExtensionOrderFixed
	for _, s := range samples[1:] {
		if !slices.Equal(extensionTypes(&first.Profile.TLS), extensionTypes(&s.Profile.TLS)) {
			order = profile.ExtensionOrderShuffled
		}
	}
	for _, s := range samples {
		s.Profile.TLS.ExtensionOrder = order
	}
	for i, s := range samples[1:] {
		if diffs := diff.Compare(&first.Profile, &s.Profile); len(diffs) > 0 {
			lines := make([]string, len(diffs))
			for j, d := range diffs {
				lines[j] = d.String()
			}
			return nil, fmt.Errorf("sample %d differs from sample 1 beyond extension order, which a profile cannot describe:\n%s",
				i+2, strings.Join(lines, "\n"))
		}
	}
	for _, s := range samples {
		if s.Trace != nil {
			first.SampleTraces = append(first.SampleTraces, s.Trace)
		}
		if len(s.H1) > 0 {
			first.SampleH1 = append(first.SampleH1, s.H1)
		}
		if s.Page != nil {
			first.SamplePages = append(first.SamplePages, s.Page)
		}
	}
	return first, nil
}
