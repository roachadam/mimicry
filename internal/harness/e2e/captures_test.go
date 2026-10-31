// Package e2e proves the profiles against the real browsers: each test
// replays a browser's captures (internal/harness/testdata/captures) through
// mimicry and fails on any difference not pinned as a known gap.
//
// Tests check the profiles MIMICRY_PROFILES selects (testprofiles, decision
// 0035), against their own captures or those capturesFrom names. Gap tables
// are keyed by the profile that took the capture and listed in
// profiles/EVIDENCE.md (Not covered by any claim); a pinned gap that stops appearing fails, except in four
// tables that never check that they were used: redirectsLineGaps (it only
// logs), redirectsMerged, connectionsPreconnects and raceCookieLabels.
// Timer-driven behaviour is tested in internal/h2 with a fake clock, not
// here.
package e2e

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/internal/harness/internal/testprofiles"
	"github.com/roachadam/mimicry/profile"
)

const captureDir = "../testdata/captures/"

// testProfiles names the profiles each test checks: the newest of each
// browser on each platform, every supported one with MIMICRY_PROFILES=all, or those it lists
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
// through capturesFrom: its own, or the earlier profile's it inherits with
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

	var (
		data []byte
		err  error
	)

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

func TestMain(m *testing.M) {
	if dir := os.Getenv(interopChildEnv); dir != "" {
		os.Exit(runChildBackend(dir))
	}

	os.Exit(m.Run())
}

func scenario(t *testing.T, name string) *server.Scenario {
	t.Helper()

	sc, err := server.ScenarioByName(name)
	if err != nil {
		t.Fatal(err)
	}

	return sc
}

// missingCaptures are the captures known not to exist, and why; a test
// needing one is skipped. Any other missing capture fails. Keyed by the file
// of the profile that holds the scenario's captures, so a profile that
// inherits the scenario (capturesFrom) inherits the entry too.
var missingCaptures = withTwinsMissing(map[string]string{
	"safari-26-macos.proxy-auth.proxytrace.json":    "Safari 26 proxy credentials were never captured",
	"safari-26-macos.proxy-plain-auth.h1trace.json": "Safari 26 proxy credentials were never captured",
	// Safari answers a proxy's 407 only with credentials macOS holds: a
	// com.apple.proxy.http.global device profile and the system's login
	// dialog, which need a person at the screen (docs/capturing.md). Safari 26's
	// socket credential rules are copied from Safari 27's capture.
	"safari-26-macos.proxy-websocket-auth.pagetrace.json.gz": "Safari 26 WebSockets through a proxy with credentials were never captured (macOS proxy credentials need a person at the screen); its rules are copied from Safari 27's",
	// The capture tool clicks in Safari through the system's mouse
	// events, which need Accessibility access; captures taken
	// remotely over ssh have none.
	"safari-26-macos.nav-redirect-click.pagetrace.json.gz":         "Safari 26 clicks were never captured (no Accessibility access over ssh)",
	"safari-26-macos.nav-redirect-click-cookies.pagetrace.json.gz": "Safari 26 clicks were never captured (no Accessibility access over ssh)",
	// h3-streams-blocked (2026-10-31) was captured on the capture Mac
	// only, from Chrome 156, Firefox 158 and Safari 27.
	"chrome-155-macos.h3-streams-blocked.pagetrace.json.gz":               "h3-streams-blocked came after Chrome 155, no longer installable as branded Chrome",
	testprofiles.InheritingName + ".h3-streams-blocked.pagetrace.json.gz": "chrome-155-macos, whose captures it inherits, has none",
	"chrome-156-windows.h3-streams-blocked.pagetrace.json.gz":             "h3-streams-blocked was captured on macOS only (2026-10-31); not yet on the Windows PC",
	"safari-26-macos.h3-streams-blocked.pagetrace.json.gz":                "h3-streams-blocked was captured with Safari 27 only (2026-10-31); not yet on the macOS 26 Mac",
}, map[string]string{
	// The cookie twins (server.CookieTwins) came after Chrome 155, which
	// is no longer installable as branded Chrome; capturesFrom names only
	// an older version, so it cannot inherit Chrome 156's (profiles/EVIDENCE.md, Not covered).
	"chrome-155-macos": "the cookie twins came after Chrome 155, no longer installable as branded Chrome",
	// TestInheritedProfile's synthetic profile inherits only the scenarios
	// chrome-155-macos has captures of.
	testprofiles.InheritingName: "chrome-155-macos, whose captures it inherits, has none",
})

// withTwinsMissing adds to missing an entry for each cookie twin
// (server.CookieTwins) of each profile in profiles, with why.
func withTwinsMissing(missing, profiles map[string]string) map[string]string {
	for name, why := range profiles {
		for _, sc := range server.CookieTwins {
			missing[name+"."+sc.Name+".pagetrace.json.gz"] = why
		}
	}

	return missing
}

// testPort matches a .test host's port.
var testPort = regexp.MustCompile(`\.test:\d+`)

// normalisePort replaces the capture ports in s.
func normalisePort(s string) string {
	return testPort.ReplaceAllString(s, ".test:PORT")
}

// portless replaces ports after .test hosts.
func portless(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = normalisePort(l)
	}

	return out
}
