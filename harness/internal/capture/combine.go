package capture

import (
	"fmt"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/profile"
)

// Combine sets every sample's extension order from how the samples compare
// (shuffled if any two orders differ, fixed otherwise), even when it then
// returns an error, and folds the GREASE ECH cipher suites and payload
// lengths every sample sent into the first's sets: a browser picks those per
// connection. It checks the samples agree on everything else, the record
// version included among the samples that kept their records, and gives the
// first sample's TLS that record version. It returns the
// first sample, holding every sample's ClientHello in SampleClientHellos and
// trace in SampleTraces; how far those agree is h2trace.Compare's job, since
// timing legitimately varies between connections.
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
	mergeECH(&first.Profile.TLS, samples)
	for i, s := range samples[1:] {
		if diffs := diff.Compare(&first.Profile, &s.Profile); len(diffs) > 0 {
			lines := make([]string, len(diffs))
			for j, d := range diffs {
				lines[j] = d.String()
			}
			return nil, fmt.Errorf("sample %d differs from sample 1 beyond extension order and ECH's per-connection choices, which a profile cannot describe:\n%s",
				i+2, strings.Join(lines, "\n"))
		}
	}
	// A sample kept as a bare handshake message (older captures) has no
	// record version: only samples with records are compared.
	var v uint16
	for i, s := range samples {
		sv := s.RecordVersion()
		if sv == 0 {
			continue
		}
		if v == 0 {
			v = sv
			continue
		}
		if sv != v {
			return nil, fmt.Errorf("sample %d's ClientHello records carry version %#04x, earlier samples' %#04x", i+1, sv, v)
		}
	}
	if v != 0 {
		first.Profile.TLS.RecordVersion = profile.Code(v)
	}
	first.SampleClientHellos = nil
	for _, s := range samples {
		if h := s.hello(); len(h) > 0 {
			first.SampleClientHellos = append(first.SampleClientHellos, h)
		}
		if s.Trace != nil {
			first.SampleTraces = append(first.SampleTraces, s.Trace)
		}
		if len(s.H1) > 0 {
			first.SampleH1 = append(first.SampleH1, s.H1)
		}
		if s.Page != nil {
			first.SamplePages = append(first.SamplePages, s.Page)
		}
		if len(s.Proxy) > 0 {
			first.SampleProxy = append(first.SampleProxy, s.Proxy)
		}
	}
	return first, nil
}

// mergeECH replaces t's GREASE ECH shape with one whose cipher suites and
// payload lengths are every sample's, sorted. The other samples keep their
// own, so Compare checks each against the merged sets.
func mergeECH(t *profile.TLS, samples []*Capture) {
	i := slices.IndexFunc(t.Extensions, func(e profile.Extension) bool {
		return e.ECH != nil
	})
	if i < 0 {
		return
	}
	merged := *t.Extensions[i].ECH
	merged.CipherSuites = slices.Clone(merged.CipherSuites)
	merged.PayloadLengths = slices.Clone(merged.PayloadLengths)
	for _, s := range samples[1:] {
		for _, e := range s.Profile.TLS.Extensions {
			if e.ECH == nil {
				continue
			}
			merged.CipherSuites = append(merged.CipherSuites, e.ECH.CipherSuites...)
			merged.PayloadLengths = append(merged.PayloadLengths, e.ECH.PayloadLengths...)
		}
	}
	slices.SortFunc(merged.CipherSuites, profile.HPKESuite.Compare)
	merged.CipherSuites = slices.Compact(merged.CipherSuites)
	slices.Sort(merged.PayloadLengths)
	merged.PayloadLengths = slices.Compact(merged.PayloadLengths)
	// The extension list may share its backing array with other values
	// (a page log's connections): replace it rather than write into it.
	t.Extensions = slices.Clone(t.Extensions)
	t.Extensions[i].ECH = &merged
}
