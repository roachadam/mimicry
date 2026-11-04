package e2e

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

// The client-hints page through mimicry (runH3Page, each request told
// whether its page delegates its hints: server.ClientHintsDelegation),
// against every sample: Accept-CH kept per origin, given to the page's own
// origin and to the third parties it delegates to, withheld from the
// origin its policy leaves out, narrowed and cleared (Clear-Site-Data),
// and Critical-CH restarts over HTTP/2, HTTP/1.1 and HTTP/3. Request for
// request, in order of occurrence per path (a restarted navigation is
// its path twice), each must go over the protocol, host and connection a
// sample's did (a restart on its first sending's connection), with the
// header lines, names in order and values, of one sample. The network
// hints rtt and downlink are noisy per host: only their format and range
// are checked (hintValue). Firefox and Safari send no hints and restart
// nothing, and must match their captures as well.
func TestClientHintsMatchesBrowser(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			sc := server.ClientHints
			want := h3Captures(t, p, sc)
			got := runH3Page(t, p, sc, want[0])
			skip, unused := gapsFor(t, capture.Source(p, sc.Name)+"/"+sc.Name)

			compareHintRequests(t, p, got, want, skip)
			unused()
		})
	}
}

// hintSent is one request as TestClientHintsMatchesBrowser compares it:
// how it went and its header lines, ports and network hint values
// replaced (hintLines).
type hintSent struct {
	via   string
	lines string
}

// hintRequests are page's requests by path and occurrence ("/h3/crit.nav
// #2"), the keys in the order wireRequests lists them. p, when not nil,
// is the profile whose network hint values are checked (hintLines).
func hintRequests(page *capture.Page, p *profile.Spec) (keys []string, sent map[string]hintSent) {
	sent = map[string]hintSent{}

	seen := map[string]int{}

	for _, w := range wireRequests(page) {
		seen[w.path]++

		key := fmt.Sprintf("%s #%d", w.path, seen[w.path])

		keys = append(keys, key)
		sent[key] = hintSent{
			via:   w.via.String(),
			lines: strings.Join(hintLines(w, p), "\n"),
		}
	}

	return keys, sent
}

// hintLines are w's header lines, pseudo-headers and the request line
// left out, ports replaced. A network hint's value (rtt, downlink) is
// replaced by its name when it has the form and range Chrome gives it,
// for the profile p (always, for a browser's sample: p nil), and kept
// otherwise, so it differs.
func hintLines(w *wireRequest, p *profile.Spec) []string {
	var out []string

	for i, l := range w.lines {
		if strings.HasPrefix(l, ":") || (i == 0 && w.proto == "HTTP/1.1") {
			continue
		}

		name, value, _ := strings.Cut(l, ": ")
		if n := strings.ToLower(name); (n == profile.HintRTT || n == profile.HintDownlink) && (p == nil || hintValue(p, n, value)) {
			value = "<" + n + ">"
		}

		out = append(out, normalisePort(name+": "+value))
	}

	return out
}

// hintValue reports whether value is one Chrome could give network hint
// name as profile p: rtt a multiple of 50 ms up to 3 s; downlink a
// multiple of 0.05 Mbps within 10% of the machine's (before rounding),
// at most 10; each written as Chrome writes the number.
func hintValue(p *profile.Spec, name, value string) bool {
	if p.ClientHints == nil {
		return false
	}

	if name == profile.HintRTT {
		n, err := strconv.Atoi(value)

		return err == nil && strconv.Itoa(n) == value && n%50 == 0 && n >= 0 && n <= 3000
	}

	v, err := strconv.ParseFloat(value, 64)
	d := p.ClientHints.Machine.Downlink
	steps := v * 20

	return err == nil && strconv.FormatFloat(v, 'f', -1, 64) == value &&
		math.Abs(steps-math.Round(steps)) < 1e-9 &&
		v >= math.Round(0.9*d*20)/20 && v <= min(10, math.Round(1.1*d*20)/20)
}

// compareHintRequests holds got's requests to the samples' (want), by
// path and occurrence: how each went, and its header lines.
func compareHintRequests(t *testing.T, p *profile.Spec, got *capture.Page, want []*capture.Page, skip func(string) bool) {
	t.Helper()

	gotKeys, gotSent := hintRequests(got, p)

	var keys []string

	samples := make([]map[string]hintSent, len(want))

	for i, w := range want {
		var k []string

		k, samples[i] = hintRequests(w, nil)
		for _, key := range k {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}

	for _, key := range gotKeys {
		if !slices.Contains(keys, key) && !skip("transport "+key) {
			t.Errorf("%s: sent (%s), the browser did not", key, gotSent[key].via)
		}
	}

	for _, key := range keys {
		var vias, forms []string

		for _, s := range samples {
			if x, ok := s[key]; ok {
				if !slices.Contains(vias, x.via) {
					vias = append(vias, x.via)
				}

				if !slices.Contains(forms, x.lines) {
					forms = append(forms, x.lines)
				}
			}
		}

		g, ok := gotSent[key]

		switch {
		case (!ok || !slices.Contains(vias, g.via)) && skip("transport "+key):
		case !ok:
			t.Errorf("%s: not sent (browser: %s)", key, strings.Join(vias, " or "))

			continue

		case !slices.Contains(vias, g.via):
			t.Errorf("%s: went %s, browser %s", key, g.via, strings.Join(vias, " or "))
		}

		switch {
		case !ok || slices.Contains(forms, g.lines):
		case skip("header list " + key):
			// The one header list gap is Width (noWidth): the rest must
			// still be the browser's.
			if !slices.ContainsFunc(forms, func(f string) bool {
				return withoutWidth(f) == g.lines
			}) {
				t.Errorf("%s: header lines differ from every sample's beyond Width and Sec-CH-Width; against the first that sent it: %s", key, firstDifference(g.lines, withoutWidth(forms[0])))
			}

		default:
			t.Errorf("%s: header lines differ from every sample's; against the first that sent it: %s", key, firstDifference(g.lines, forms[0]))
		}
	}
}

// withoutWidth is a hintSent's lines without Width and Sec-CH-Width,
// which mimicry does not send (noWidth).
func withoutWidth(lines string) string {
	return strings.Join(slices.DeleteFunc(strings.Split(lines, "\n"), func(l string) bool {
		name, _, _ := strings.Cut(strings.ToLower(l), ": ")

		return name == "width" || name == "sec-ch-width"
	}), "\n")
}

// withoutHints are lines without the client hints among them: sec-ch-*,
// the legacy device and network hints and save-data, by name in any case.
func withoutHints(lines []string) []string {
	legacy := []string{"dpr", "viewport-width", "width", "device-memory", profile.HintRTT, profile.HintDownlink, "ect", "save-data"}

	return slices.DeleteFunc(slices.Clone(lines), func(l string) bool {
		name, _, _ := strings.Cut(strings.ToLower(l), ": ")

		return strings.HasPrefix(name, "sec-ch-") || slices.Contains(legacy, name)
	})
}
