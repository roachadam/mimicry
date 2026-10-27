package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/harness/internal/testprofiles"
	"github.com/roachadam/mimicry/profiles"
)

// inheritedTests are the tests that read non-default scenario captures, so
// check an inheriting profile. inheritedTestsFull take a minute or more each
// and run only with MIMICRY_PROFILES=all.
var (
	inheritedTests = []string{
		"TestKindsMatchBrowserTraces",
		"TestCORSMatchesBrowser",
		"TestCORSEdgeMatchesBrowser",
		"TestCookiesMatchBrowserTrace",
		"TestCookiesScenarioMatchesBrowser",
		"TestCookiesEdgeMatchesBrowser",
		"TestCookiesEdge2MatchesBrowser",
		"TestHeadersOrderMatchesBrowser",
		"TestRedirects2MatchesBrowser",
		"TestNavRedirectsMatchBrowser",
		"TestHTTP1MatchesBrowserTrace",
		"TestProxyMatchesBrowser",
		"TestWebSocketsMatchBrowser",
		"TestDeflateReproducesBrowserMessages",
		"TestConnectionsMatchBrowser",
		"TestResumptionMatchesBrowser",
		"TestHPACKReproducesBrowserBlocks",
		"TestQPACKReproducesBrowserSections",
	}
	inheritedTestsFull = []string{
		"TestHTTP3MatchesBrowser",
		"TestHTTP3EdgeMatchesBrowser",
		"TestHTTP3DownloadMatchesBrowser",
	}
)

// inheritChildEnv marks the test binary run by TestInheritedProfile.
const inheritChildEnv = "MIMICRY_INHERIT_CHILD"

// TestInheritedProfile proves captures_from end to end (decision 0035): a
// synthetic profile inheriting chrome-155-macos's captures passes with only
// version tokens rewritten, and fails with a header value of its own. Each
// case reruns this test binary with MIMICRY_PROFILES naming it.
func TestInheritedProfile(t *testing.T) {
	if os.Getenv(inheritChildEnv) != "" {
		t.Skip("the inheriting run itself")
	}
	tests := inheritedTests
	if os.Getenv(testprofiles.Env) == "all" {
		tests = slices.Concat(inheritedTests, inheritedTestsFull)
	}
	for _, tc := range []struct {
		name  string
		tests []string
		edit  func([]byte) []byte
		// fail lists what the run's output must show; nil: it passes.
		fail []string
	}{
		{
			name:  "versions rewritten",
			tests: tests,
		},
		{
			name:  "other header value",
			tests: []string{"TestKindsMatchBrowserTraces", "TestHTTP1MatchesBrowserTrace", "TestCORSEdgeMatchesBrowser"},
			edit: func(data []byte) []byte {
				return bytes.ReplaceAll(data, []byte("en-US,en;q=0.9"), []byte("en-GB,en;q=0.9"))
			},
			fail: []string{
				"--- FAIL: TestKindsMatchBrowserTraces",
				"--- FAIL: TestHTTP1MatchesBrowserTrace",
				"--- FAIL: TestCORSEdgeMatchesBrowser",
				"en-GB,en;q=0.9",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := testprofiles.Inheriting(tc.edit)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), testprofiles.InheritingName+".json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), os.Args[0], //nolint:gosec // this test binary
				"-test.run=^("+strings.Join(tc.tests, "|")+")$",
				"-test.count=1",
				"-test.v",
			)
			cmd.Env = append(os.Environ(), testprofiles.Env+"="+path, inheritChildEnv+"=1")
			out, err := cmd.CombinedOutput()
			if tc.fail == nil {
				if err != nil {
					t.Fatalf("%s against chrome-155-macos's captures: %v\n%s", testprofiles.InheritingName, err, failures(out))
				}
				for _, name := range tc.tests {
					if !bytes.Contains(out, []byte("--- PASS: "+name+" ")) {
						t.Errorf("%s did not pass:\n%s", name, failures(out))
					}
				}
				if !bytes.Contains(out, []byte("versions rewritten chrome-155-macos → "+testprofiles.InheritingName)) {
					t.Error("no capture was read through captures_from")
				}
				return
			}
			if err == nil {
				t.Fatalf("passed with a header value the browser did not send")
			}
			for _, want := range tc.fail {
				if !bytes.Contains(out, []byte(want)) {
					t.Errorf("the run's output lacks %q:\n%s", want, failures(out))
				}
			}
		})
	}
}

// failures keeps a verbose test run's failure lines and the lines that
// say why, for a readable report.
func failures(out []byte) string {
	var keep []string
	for line := range strings.SplitSeq(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--- FAIL") || strings.HasPrefix(trimmed, "FAIL") || strings.Contains(line, "_test.go:") && !strings.Contains(line, "inherited, versions rewritten") {
			keep = append(keep, line)
		}
	}
	if len(keep) > 200 {
		keep = append(keep[:200], "...")
	}
	return strings.Join(keep, "\n")
}

// Every built-in profile's captures_from names known scenarios other than
// the default page (which a new version always recaptures), each held by
// a profile with captures of it.
func TestCapturesFromNamesCapturedScenarios(t *testing.T) {
	for _, name := range profiles.Names() {
		p, err := profiles.ByName(name)
		if err != nil {
			t.Fatal(err)
		}
		for sc, from := range p.CapturesFrom {
			if sc == capture.DefaultScenario {
				t.Errorf("%s: captures_from.%s: the default page is never inherited", name, sc)
				continue
			}
			if _, err := server.ScenarioByName(sc); err != nil && !strings.HasPrefix(sc, "replay-") {
				t.Errorf("%s: captures_from.%s: %v", name, sc, err)
			}
			files, err := filepath.Glob(captureDir + from + "." + sc + ".*")
			if err != nil {
				t.Fatal(err)
			}
			if len(files) == 0 {
				t.Errorf("%s: captures_from.%s: %s has no captures of it", name, sc, from)
			}
		}
	}
}
