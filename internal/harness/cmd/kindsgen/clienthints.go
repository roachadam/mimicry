package main

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// hintsNavigation is the client-hints page's navigation after the opt-in
// to every hint (server.ClientHints), which deriveClientHints reads the
// hints' order and values from.
const hintsNavigation = "/h3/next.nav"

// downlinkGaps are, per capture source, the downlink values sent when
// they stray beyond Chrome's noise around one estimate (downlinkInBand)
// and why: the estimate itself moved during the run. The values must
// still be Chrome's steps within the pinned range, and a pin no value
// needs is an error.
var downlinkGaps = map[string]downlinkGap{
	"chrome-157-linux": {
		lo:  1.3,
		hi:  1.75,
		why: "1.3 to 1.75 Mbps, and www.ch-a.test got both in sample 2: Chrome's per-host multiplier is fixed (0.9 + (hash % 21) * 0.01, content/browser/client_hints/client_hints.cc), so its network quality estimate rose by 30% or more within the sample; the centre (1.55) is still the profile's estimate",
	},
}

// downlinkGap is the range of downlink values (Mbps) a moving estimate
// gave, and why.
type downlinkGap struct {
	lo  float64
	hi  float64
	why string
}

// hintName reports whether a header name is a client hint: every
// sec-ch-*, and the legacy names.
func hintName(name string) bool {
	return strings.HasPrefix(name, "sec-ch-") || slices.Contains([]string{
		"device-memory",
		"dpr",
		"viewport-width",
		"width",
		"rtt",
		"downlink",
		"ect",
		"save-data",
	}, name)
}

// sentRequest is one captured request of the client-hints page: its path,
// its occurrence among the sample's requests for that path (a restart is
// the second), its step's kind ("navigate", "iframe", "fetch", ...; the
// typed / is "navigate") and the headers its page set (lowercase, sorted),
// and its header names (lowercase) and values in order.
type sentRequest struct {
	at     string
	path   string
	nth    int
	kind   string
	page   []string
	names  []string
	values []string
}

// document reports a request for a document: a navigation's or a
// frame's, whose hints go as one run in Hints' order.
func (r sentRequest) document() bool {
	return r.kind == "navigate" || r.kind == "iframe"
}

// blinkHintOrder is the order Blink adds client hints to a subresource's
// header map (FrameFetchContext::AddClientHintsIfNecessary): a lead from
// the source, which every captured subresource's run checks
// (checkTableRun).
var blinkHintOrder = []string{
	"device-memory",
	"sec-ch-device-memory",
	profile.HintRTT,
	profile.HintDownlink,
	"ect",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-arch",
	"sec-ch-ua-platform",
	"sec-ch-ua-platform-version",
	"sec-ch-ua-model",
	"sec-ch-ua-full-version",
	"sec-ch-ua-full-version-list",
	"sec-ch-ua-bitness",
	"sec-ch-ua-wow64",
	"sec-ch-ua-form-factors",
	"save-data",
	"sec-ch-prefers-reduced-transparency",
	"sec-ch-prefers-reduced-motion",
	"sec-ch-prefers-color-scheme",
	"dpr",
	"sec-ch-dpr",
	"viewport-width",
	"sec-ch-viewport-width",
	"sec-ch-viewport-height",
	"width",
	"sec-ch-width",
}

// deriveClientHints derives p.ClientHints from the client-hints captures
// (server.ClientHintsCaptures); without one, or where the browser sent no
// opted-in hint, it stays nil. Every sample must agree, and every captured
// request carrying hints must be what the model sends: a navigation's in
// Hints' order where its low-entropy ones go (after RestartAfter when
// restarted), another request's in its header table's order, and the
// values the machine gives.
func deriveClientHints(p *profile.Spec, dir string) error {
	// The machine the profile was copied with, for the hints its browser
	// never sends (machineOf).
	var seed profile.Machine
	if p.ClientHints != nil {
		seed = p.ClientHints.Machine
	}

	p.ClientHints = nil

	files, err := server.ClientHintsCaptures(dir, p)
	if err != nil {
		return err
	}

	steps := map[string]server.H3Step{
		"/": {
			KindsStep: server.KindsStep{
				Kind: "navigate",
			},
		},
	}
	for _, st := range server.ClientHintsSteps() {
		steps[st.Path()] = st
	}

	var sent []sentRequest
	for _, f := range files {
		pages, err := loadFile(f)
		if err != nil {
			return err
		}

		for si, page := range pages {
			sent = append(sent, sentRequests(filepath.Base(f.Path), si, page, steps)...)
		}
	}

	opted := slices.ContainsFunc(sent, func(r sentRequest) bool {
		return slices.ContainsFunc(r.names, func(n string) bool {
			return hintName(n) && !profile.LowEntropyHint(n)
		})
	})
	if !opted {
		// Firefox and Safari: no hint beyond the lists', and no restart.
		for _, r := range sent {
			if r.nth > 0 {
				return fmt.Errorf("%s: sent twice, though no hint was ever opted in to", r.at)
			}
		}

		return nil
	}

	ch, err := hintsOf(sent, seed)
	if err != nil {
		return err
	}

	for _, name := range blinkHintOrder {
		if slices.Contains(ch.Hints, name) {
			ch.TableOrder = append(ch.TableOrder, name)
		}
	}

	if len(ch.TableOrder) != len(ch.Hints) {
		return fmt.Errorf("hints %v are not all in Blink's order %v", ch.Hints, blinkHintOrder)
	}

	p.ClientHints = ch

	return checkHints(p, sent)
}

// sentRequests lists the page's requests over every protocol, numbering
// each path's occurrences in the order they were sent on their connection.
func sentRequests(file string, sample int, page *capture.Page, steps map[string]server.H3Step) []sentRequest {
	var out []sentRequest

	seen := map[string]int{}
	add := func(proto, path string, names, values []string) {
		path, _, _ = strings.Cut(path, "?")

		st := steps[path]
		set := make([]string, 0, len(st.Headers))

		for name := range st.Headers {
			set = append(set, strings.ToLower(name))
		}

		slices.Sort(set)

		out = append(out, sentRequest{
			at:     fmt.Sprintf("%s: sample %d, %s %s #%d", file, sample+1, proto, path, seen[path]+1),
			path:   path,
			nth:    seen[path],
			kind:   st.Kind,
			page:   set,
			names:  names,
			values: values,
		})
		seen[path]++
	}

	for _, tr := range page.H2 {
		for _, r := range tr.Requests {
			var names, values []string
			for _, ins := range r.Instructions {
				if ins.Name != "" && !strings.HasPrefix(ins.Name, ":") {
					names, values = append(names, ins.Name), append(values, ins.Value)
				}
			}

			add("HTTP/2", r.Path, names, values)
		}
	}

	for _, r := range page.H1 {
		var names, values []string
		for _, line := range r.Headers {
			name, value, _ := strings.Cut(line, ":")

			names, values = append(names, strings.ToLower(name)), append(values, strings.TrimSpace(value))
		}

		// The request line's target: "GET /path HTTP/1.1".
		_, target, _ := strings.Cut(r.Line, " ")

		target, _, _ = strings.Cut(target, " ")
		add("HTTP/1.1", target, names, values)
	}

	for _, c := range page.QUIC {
		if c.H3 == nil {
			continue
		}

		for _, r := range c.H3.Requests {
			var names, values []string
			for _, f := range r.Section.Fields {
				if f.Name != "" && !strings.HasPrefix(f.Name, ":") {
					names, values = append(names, f.Name), append(values, f.Value)
				}
			}

			add("HTTP/3", r.Path, names, values)
		}
	}

	return out
}

// hintsOf derives ClientHints from the opted-in navigation's hints, the
// restarts' place, and the values; seed is the machine the profile was
// copied with (machineOf).
func hintsOf(sent []sentRequest, seed profile.Machine) (*profile.ClientHints, error) {
	var (
		ch   *profile.ClientHints
		errs []error
	)

	for _, r := range sent {
		if r.path != hintsNavigation {
			continue
		}

		var hints []string
		for _, n := range r.names {
			if hintName(n) {
				hints = append(hints, n)
			}
		}

		got := &profile.ClientHints{
			Hints: hints,
		}

		m, full, err := machineOf(r, seed)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.at, err)
		}

		got.Machine, got.FullVersion = m, full
		got.ChromiumFullVersion = chromiumVersion(r, full)

		switch {
		case ch == nil:
			ch = got
		case !slices.Equal(ch.Hints, got.Hints):
			errs = append(errs, fmt.Errorf("%s: hints %v, where another sample sends %v", r.at, got.Hints, ch.Hints))
		case !machineEqual(ch.Machine, got.Machine) || ch.FullVersion != got.FullVersion || ch.ChromiumFullVersion != got.ChromiumFullVersion:
			errs = append(errs, fmt.Errorf("%s: machine %+v (%s, Chromium %q), where another sample says %+v (%s, Chromium %q)", r.at, got.Machine, got.FullVersion, got.ChromiumFullVersion, ch.Machine, ch.FullVersion, ch.ChromiumFullVersion))
		}
	}

	if ch == nil {
		return nil, fmt.Errorf("no capture has %s, the navigation after the opt-in", hintsNavigation)
	}

	// The restarts: their hints follow one header.
	for _, r := range sent {
		if r.kind != "navigate" || r.nth == 0 {
			continue
		}

		first := slices.IndexFunc(r.names, hintName)
		if first <= 0 {
			errs = append(errs, fmt.Errorf("%s: a restart with no hint after another header", r.at))

			continue
		}

		after := r.names[first-1]
		switch {
		case ch.RestartAfter == "":
			ch.RestartAfter = after
		case ch.RestartAfter != after:
			errs = append(errs, fmt.Errorf("%s: a restart's hints follow %s, another's %s", r.at, after, ch.RestartAfter))
		}
	}

	if ch.RestartAfter == "" {
		errs = append(errs, errors.New("no navigation was restarted (Critical-CH)"))
	}

	// The downlink estimate: the centre of the noisy values sent.
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, r := range sent {
		for i, n := range r.names {
			if n != profile.HintDownlink {
				continue
			}

			v, err := strconv.ParseFloat(r.values[i], 64)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: downlink %q", r.at, r.values[i]))

				continue
			}

			lo, hi = min(lo, v), max(hi, v)
		}
	}

	if !math.IsInf(lo, 0) {
		ch.Machine.Downlink = math.Round((lo+hi)/2*20) / 20
	}

	return ch, errors.Join(errs...)
}

// machineOf reads the machine and the browser's full version from an
// opted-in navigation's hint values. A hint the browser never sends
// (Brave: dpr, device memory, viewport, form factors, the preferences,
// the network hints and sec-ch-ua-full-version) keeps seed's value, which
// no request of the profile carries; the full version is then the
// Chromium brand's in sec-ch-ua-full-version-list.
func machineOf(r sentRequest, seed profile.Machine) (profile.Machine, string, error) {
	var (
		m    = seed
		full string
		errs []error
	)

	sent := func(name string) bool {
		return slices.Contains(r.names, name)
	}
	value := func(name string) string {
		if i := slices.Index(r.names, name); i >= 0 {
			return r.values[i]
		}

		return ""
	}
	str := func(name string, into *string) {
		if !sent(name) {
			return
		}

		s, err := strconv.Unquote(value(name))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %q is no string", name, value(name)))
		}

		*into = s
	}
	num := func(name string, into *float64) {
		if !sent(name) {
			return
		}

		f, err := strconv.ParseFloat(value(name), 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %q is no number", name, value(name)))
		}

		*into = f
	}
	text := func(name string, into *string) {
		if sent(name) {
			*into = value(name)
		}
	}

	if sent("sec-ch-ua-full-version") {
		str("sec-ch-ua-full-version", &full)
	} else {
		full = chromiumVersion(r, "")
	}

	str("sec-ch-ua-platform-version", &m.PlatformVersion)
	str("sec-ch-ua-arch", &m.Arch)
	str("sec-ch-ua-bitness", &m.Bitness)
	str("sec-ch-ua-model", &m.Model)

	if sent("sec-ch-ua-wow64") {
		m.WoW64 = value("sec-ch-ua-wow64") == "?1"
	}

	if sent("sec-ch-ua-form-factors") {
		m.FormFactors = nil

		for f := range strings.SplitSeq(value("sec-ch-ua-form-factors"), ", ") {
			s, err := strconv.Unquote(f)
			if err != nil {
				errs = append(errs, fmt.Errorf("sec-ch-ua-form-factors %q is no list of strings", value("sec-ch-ua-form-factors")))

				break
			}

			m.FormFactors = append(m.FormFactors, s)
		}
	}

	num("sec-ch-dpr", &m.DPR)

	var width, height float64
	if sent("sec-ch-viewport-width") {
		num("sec-ch-viewport-width", &width)

		m.ViewportWidth = int(width)
	}

	if sent("sec-ch-viewport-height") {
		num("sec-ch-viewport-height", &height)

		m.ViewportHeight = int(height)
	}

	num("sec-ch-device-memory", &m.DeviceMemory)
	text("sec-ch-prefers-color-scheme", &m.ColorScheme)
	text("sec-ch-prefers-reduced-motion", &m.ReducedMotion)
	text("sec-ch-prefers-reduced-transparency", &m.ReducedTransparency)
	text("ect", &m.ECT)

	if sent(profile.HintDownlink) {
		// A placeholder until hintsOf has every sample's: checkHints
		// compares values against it otherwise.
		m.Downlink = 1
	}

	return m, full, errors.Join(errs...)
}

// chromiumVersion is the Chromium brand's version in r's
// sec-ch-ua-full-version-list where it is not the browser's own full
// version (Edge's), else "".
func chromiumVersion(r sentRequest, full string) string {
	i := slices.Index(r.names, "sec-ch-ua-full-version-list")
	if i < 0 {
		return ""
	}

	const brand = `"Chromium";v="`
	for e := range strings.SplitSeq(r.values[i], ", ") {
		if v, ok := strings.CutPrefix(e, brand); ok {
			if v = strings.TrimSuffix(v, `"`); v != full {
				return v
			}
		}
	}

	return ""
}

// machineEqual compares machines but their downlink, which hintsOf
// derives from every sample.
func machineEqual(a, b profile.Machine) bool {
	return a.PlatformVersion == b.PlatformVersion && a.Arch == b.Arch && a.Bitness == b.Bitness && a.Model == b.Model &&
		a.WoW64 == b.WoW64 && slices.Equal(a.FormFactors, b.FormFactors) && a.DPR == b.DPR &&
		a.ViewportWidth == b.ViewportWidth && a.ViewportHeight == b.ViewportHeight && a.DeviceMemory == b.DeviceMemory &&
		a.ColorScheme == b.ColorScheme && a.ReducedMotion == b.ReducedMotion && a.ReducedTransparency == b.ReducedTransparency && a.ECT == b.ECT
}

// checkHints checks every captured request's hints against p.ClientHints:
// each value is the one the model gives (the network hints in its steps
// and range), a navigation's hints are one run in Hints' order, and
// another request's (with the table's other own headers) are in its
// header table's order for that order of entry.
func checkHints(p *profile.Spec, sent []sentRequest) error {
	ch := p.ClientHints

	var (
		errs    []error
		strayed bool
	)

	for _, r := range sent {
		var run []string
		for i, n := range r.names {
			if !hintName(n) {
				continue
			}

			run = append(run, n)

			if err := checkHintValue(p, n, r.values[i]); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.at, err))
			}

			if mbps, err := strconv.ParseFloat(r.values[i], 64); err == nil && n == profile.HintDownlink && !downlinkInBand(p, mbps) {
				strayed = true
			}
		}

		if len(run) == 0 {
			continue
		}

		inOrder := slices.IsSortedFunc(run, func(a, b string) int {
			return slices.Index(ch.Hints, a) - slices.Index(ch.Hints, b)
		})

		switch {
		case slices.ContainsFunc(run, func(n string) bool {
			return !slices.Contains(ch.Hints, n)
		}):
			// Width and Sec-CH-Width: an image with sizes, which mimicry
			// cannot express; checked by the e2e replay's gap.
		case r.document():
			first := slices.Index(r.names, run[0])
			if !inOrder || !slices.Equal(r.names[first:first+len(run)], run) {
				errs = append(errs, fmt.Errorf("%s: a navigation's hints %v are not one run in the order %v", r.at, run, ch.Hints))
			}

		default:
			if err := checkTableRun(p, r, run); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if _, moved := downlinkGaps[capture.Source(p, server.ClientHints.Name)]; moved && !strayed {
		errs = append(errs, errors.New("downlinkGaps pins this profile, but every downlink is within 10% of its estimate: remove the pin"))
	}

	return errors.Join(errs...)
}

// checkTableRun checks that a request other than a navigation sends its
// hints, the header table's other own members (user-agent) and the headers
// its page set in the table's order (decision 0021), its hints entering in
// tableOrder, then the other own members.
func checkTableRun(p *profile.Spec, r sentRequest, run []string) error {
	table := p.Context.HeaderTable

	var others, asPage, page []string
	for _, n := range r.names {
		switch {
		case slices.Contains(table.Own, n) && !hintName(n):
			others = append(others, n)
		case slices.Contains(table.AsPage, n):
			asPage = append(asPage, n)
		}
	}

	for _, n := range r.page {
		if !slices.Contains(table.AsPage, n) {
			page = append(page, n)
		}
	}

	var entering []string
	for _, n := range p.ClientHints.TableOrder {
		if slices.Contains(run, n) {
			entering = append(entering, n)
		}
	}

	entering = append(entering, others...)

	own := *table

	own.Own = entering

	members := slices.Concat(entering, asPage)

	want, ok := headerorder.TableOrder(&own, members, page)
	if !ok {
		return fmt.Errorf("%s: the header table cannot hold %v", r.at, members)
	}

	var got []string
	for _, n := range r.names {
		if slices.Contains(members, n) || slices.Contains(page, n) {
			got = append(got, n)
		}
	}

	if !slices.Equal(got, want) {
		return fmt.Errorf("%s: sends %v, where the header table orders %v", r.at, got, want)
	}

	first := slices.Index(r.names, got[0])
	if !slices.Equal(r.names[first:first+len(got)], got) {
		return fmt.Errorf("%s: its header table's names %v are not one run", r.at, got)
	}

	return nil
}

// checkHintValue checks the value of hint name: the model's, or for the
// network hints one Chrome's rounding gives.
func checkHintValue(p *profile.Spec, name, value string) error {
	switch name {
	case profile.HintRTT:
		ms, err := strconv.Atoi(value)
		if err != nil || ms < 0 || ms > 3000 || ms%50 != 0 {
			return fmt.Errorf("rtt %q is not a multiple of 50 ms up to 3000", value)
		}

		return nil

	case profile.HintDownlink:
		mbps, err := strconv.ParseFloat(value, 64)
		if err != nil || mbps < 0 || mbps > 10 || math.Abs(mbps*20-math.Round(mbps*20)) > 1e-9 {
			return fmt.Errorf("downlink %q is not a multiple of 0.05 Mbps up to 10", value)
		}

		gap, moved := downlinkGaps[capture.Source(p, server.ClientHints.Name)]

		switch {
		case moved && (mbps < gap.lo-1e-9 || mbps > gap.hi+1e-9):
			return fmt.Errorf("downlink %q is beyond the pinned %v to %v (%s)", value, gap.lo, gap.hi, gap.why)
		case !moved && !downlinkInBand(p, mbps):
			return fmt.Errorf("downlink %q is beyond 10%% of %v", value, p.ClientHints.Machine.Downlink)
		}

		return nil
	}

	want, ok := p.ClientHintValue(name, &p.ClientHints.Machine)
	if !ok {
		// Width and Sec-CH-Width: not modelled.
		return nil
	}

	if want != value {
		return fmt.Errorf("%s is %q, where the model sends %q", name, value, want)
	}

	return nil
}

// downlinkInBand reports whether mbps is one Chrome's noise (0.90 to 1.10)
// and rounding give from p's estimate.
func downlinkInBand(p *profile.Spec, mbps float64) bool {
	m := p.ClientHints.Machine.Downlink

	return mbps >= m*0.9-0.025 && mbps <= m*1.1+0.025
}
