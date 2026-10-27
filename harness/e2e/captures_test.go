// Package e2e proves the profiles against the real browsers: each test
// replays a browser's captures (harness/testdata/captures) through mimicry
// and fails on any difference not pinned as a known gap.
//
// Tests check the profiles MIMICRY_PROFILES selects (testprofiles, decision
// 0035), against their own captures or those captures_from names. Gap tables
// are keyed by the profile that took the capture and listed in
// docs/backlog.md; a pinned gap that stops appearing fails. Timer-driven
// behaviour is tested in internal/h2 with a fake clock, not here.
package e2e

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/testprofiles"
	"github.com/roachadam/mimicry/profile"
)

const captureDir = "../testdata/captures/"

// testProfiles names the profiles each test checks: the newest of each
// browser, every supported one with MIMICRY_PROFILES=all, or those it lists
// (testprofiles, decision 0035).
func testProfiles(t testing.TB) []string {
	t.Helper()
	names, err := testprofiles.Names()
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// loadProfile returns a fresh copy of the profile under test called name.
func loadProfile(name string) (*profile.Profile, error) {
	p, _, err := testprofiles.Load(name)
	return p, err
}

// capturePath resolves p's capture of scenario (rest is what follows the
// scenario in the file name: ".pagetrace.json.gz", ".local-roots.h2trace.json")
// through captures_from: its own, or the earlier profile's it inherits with
// the Rewriter to its version. It returns the file name too, the key of
// missingCaptures.
func capturePath(t testing.TB, p *profile.Profile, scenario, rest string) (string, string, *capture.Rewriter) {
	t.Helper()
	source, rw, err := capture.Inherited(p, scenario)
	if err != nil {
		t.Fatal(err)
	}
	file := source + rest
	if scenario != capture.DefaultScenario {
		file = source + "." + scenario + rest
	}
	if rw != nil {
		t.Logf("%s: %s inherited, versions rewritten %s", scenario, file, rw)
	}
	return captureDir + file, file, rw
}

// loadPages reads p's page capture of scenario as loadSamples does, rewritten
// to p's version where inherited.
func loadPages(t testing.TB, p *profile.Profile, scenario, rest string) ([]*capture.Page, error) {
	t.Helper()
	path, _, rw := capturePath(t, p, scenario, rest)
	pages, err := capture.LoadPages(path)
	if err != nil {
		return nil, err
	}
	rw.Pages(pages)
	return pages, nil
}

// loadH2Traces reads p's HTTP/2 traces of scenario, rewritten to p's version
// where inherited (not for HPACK reproduction, which needs them as sent).
func loadH2Traces(t testing.TB, p *profile.Profile, scenario string) ([]*h2trace.Trace, error) {
	t.Helper()
	path, _, rw := capturePath(t, p, scenario, ".h2trace.json")
	traces, err := h2trace.Load(path)
	if err != nil {
		return nil, err
	}
	rw.Traces(traces)
	return traces, nil
}

// loadSamples reads the samples of p's capture of scenario (rest as
// capturePath's), rewritten to p's version where inherited, skipping the
// test when the capture is one of missingCaptures. T is *capture.Page for
// page captures, []capture.ProxyRequest or []capture.H1Request for the
// proxy's and HTTP/1.1 traces.
func loadSamples[T any](t *testing.T, p *profile.Profile, scenario, rest string) []T {
	t.Helper()
	path, file, rw := capturePath(t, p, scenario, rest)
	var data []byte
	var err error
	if strings.HasSuffix(file, ".gz") {
		var pages []*capture.Page
		pages, err = capture.LoadPages(path)
		if err == nil {
			data, err = json.Marshal(pages)
		}
	} else {
		data, err = os.ReadFile(path)
	}
	if why, ok := missingCaptures[file]; ok {
		if err == nil {
			t.Fatalf("capture %s exists but is listed missing (%s)", file, why)
		}
		t.Skipf("no capture %s: %s", file, why)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no capture %s", file)
	}
	if err != nil {
		t.Fatal(err)
	}
	var samples []T
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatal(err)
	}
	if rw == nil {
		return samples
	}
	switch s := any(samples).(type) {
	case []*capture.Page:
		rw.Pages(s)
	case [][]capture.ProxyRequest:
		for _, sample := range s {
			rw.Proxy(sample)
		}
	case [][]capture.H1Request:
		for _, sample := range s {
			rw.H1(sample)
		}
	default:
		t.Fatalf("%s is inherited, but loadSamples cannot rewrite a %T", file, samples)
	}
	return samples
}
