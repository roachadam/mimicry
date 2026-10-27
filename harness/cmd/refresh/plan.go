package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/pageshape"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

const (
	modeQuick = "quick"
	modeFull  = "full"

	capturesDir = "testdata/captures"
	pagesDir    = "testdata/pages"
	profilesDir = "../profiles/data"
)

// refreshRun is one run of the tool, saved as plan.json in its staging
// directory so -resume can pick it up.
type refreshRun struct {
	Dir   string `json:"dir"`
	Stamp string `json:"stamp"`
	Mode  string `json:"mode"`
	Mini  string `json:"mini"`
	// Serial captures the mini's browsers one after the other in quick
	// mode too.
	Serial bool    `json:"serial,omitempty"`
	Plans  []*plan `json:"plans"`
}

// plan is what the run does for one browser.
type plan struct {
	Target  string `json:"target"`
	Browser string `json:"browser"`
	// Host is the ssh host the browser runs on, "" for this Mac.
	Host string `json:"host,omitempty"`
	// Installed is the browser's version where it runs.
	Installed string `json:"installed"`
	// Name and Version (the major) are the profile's.
	Name    string `json:"name"`
	Version string `json:"version"`
	// Previous is the profile the new one is copied from: the browser's
	// newest older one, or the profile itself when InPlace.
	Previous string    `json:"previous"`
	InPlace  bool      `json:"in_place"`
	Captures []planned `json:"captures"`
	// CapturesFrom is the profile's captures_from once refreshed.
	CapturesFrom map[string]string `json:"captures_from,omitempty"`
	Estimate     time.Duration     `json:"estimate"`
	Problems     []string          `json:"problems,omitempty"`
	Manual       []string          `json:"manual,omitempty"`
	Warnings     []string          `json:"warnings,omitempty"`
	// Measured is the capture tool's summary of each written scenario.
	Measured []summaryLine `json:"measured,omitempty"`
	Started  time.Time     `json:"started"`
	Finished time.Time     `json:"finished"`
}

// planned is one scenario to capture.
type planned struct {
	Scenario   string        `json:"scenario"`
	Samples    int           `json:"samples"`
	LocalRoots bool          `json:"local_roots,omitempty"`
	Estimate   time.Duration `json:"estimate"`
}

// label is the scenario as its capture is named.
func (c planned) label() string {
	if c.LocalRoots {
		return c.Scenario + ".local-roots"
	}

	return c.Scenario
}

// summaryLine is a line of the capture tool's -summary file.
type summaryLine struct {
	Browser   string  `json:"browser"`
	Scenario  string  `json:"scenario"`
	Name      string  `json:"name"`
	UserAgent string  `json:"user_agent"`
	Samples   int     `json:"samples"`
	Seconds   float64 `json:"seconds"`
	File      string  `json:"file"`
}

// preflightReport is the capture tool's -preflight output.
type preflightReport struct {
	User        string `json:"user"`
	ConsoleUser string `json:"console_user"`
	Browsers    []struct {
		Name       string `json:"name"`
		App        string `json:"app"`
		Version    string `json:"version"`
		Unapproved bool   `json:"unapproved"`
	} `json:"browsers"`
	CAPath      string `json:"ca_path"`
	CATrusted   bool   `json:"ca_trusted"`
	Resolver    bool   `json:"resolver"`
	Aioquic     string `json:"aioquic"`
	SafariProxy string `json:"safari_proxy"`
}

// machine is where p's browser runs.
func (r *refreshRun) machine(p *plan) *machine {
	if p.Host == "" {
		return &machine{
			dir: r.Dir,
		}
	}

	return &machine{
		host: p.Host,
		dir:  "mimicry-capture/refresh-" + r.Stamp,
	}
}

// bin is the path of the tool's binary name on m.
func (m *machine) bin(name string) string {
	if m.host == "" {
		return filepath.Join(m.dir, name)
	}

	return "./" + name
}

func (r *refreshRun) save() error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(r.Dir, "plan.json"), append(data, '\n'), 0o600)
}

func loadRun(dir string) (*refreshRun, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		return nil, err
	}
	var r refreshRun
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}

	return &r, nil
}

func (r *refreshRun) blocked() bool {
	return slices.ContainsFunc(r.Plans, func(p *plan) bool {
		return len(p.Problems) > 0
	})
}

// preflight builds the capture tool, runs its -preflight where each
// browser runs and plans each browser's refresh.
func (r *refreshRun) preflight(ctx context.Context, cfg config) error {
	if err := os.MkdirAll(r.Dir, 0o750); err != nil {
		return err
	}
	log.Printf("building the capture tool")
	bin, err := build(ctx, r.Dir, "capture")
	if err != nil {
		return err
	}
	builtins, err := loadProfiles(profilesDir)
	if err != nil {
		return err
	}
	reports := map[string]*preflightReport{}
	for _, name := range cfg.targets {
		t := targets[name]
		p := &plan{
			Target:  name,
			Browser: t.browser,
		}
		if t.onMini {
			p.Host = cfg.mini
		}
		r.Plans = append(r.Plans, p)
		rep, ok := reports[p.Host]
		if !ok {
			m := r.machine(p)
			if err := m.put(ctx, bin); err != nil {
				return err
			}
			log.Printf("preflight on %s", m)
			if rep, err = runPreflight(ctx, m); err != nil {
				return err
			}
			reports[p.Host] = rep
		}
		if err := r.planTarget(p, rep, builtins, cfg); err != nil {
			return err
		}
	}

	return nil
}

func runPreflight(ctx context.Context, m *machine) (*preflightReport, error) {
	out, err := m.output(ctx, m.bin("capture"), "-preflight")
	if err != nil {
		return nil, err
	}
	var rep preflightReport
	if err := json.Unmarshal(out, &rep); err != nil {
		return nil, fmt.Errorf("preflight on %s: %w", m, err)
	}

	return &rep, nil
}

// installedVersion is browser's version in rep, "" if it is not installed.
func (rep *preflightReport) installedVersion(browser string) string {
	for _, b := range rep.Browsers {
		if b.Name == browser {
			return b.Version
		}
	}

	return ""
}

// loadProfiles decodes every profile in dir, by name.
func loadProfiles(dir string) (map[string]*profile.Profile, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := map[string]*profile.Profile{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		p, err := profile.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out[p.Name] = p
	}

	return out, nil
}

// major is the version's leading number, false if it has none.
func major(version string) (int, bool) {
	head, _, _ := strings.Cut(version, ".")
	v, err := strconv.Atoi(head)

	return v, err == nil
}

// planTarget fills in p from the preflight of its machine.
func (r *refreshRun) planTarget(p *plan, rep *preflightReport, builtins map[string]*profile.Profile, cfg config) error {
	m := r.machine(p)
	p.Installed = rep.installedVersion(p.Browser)
	if p.Installed == "" {
		p.Problems = append(p.Problems, fmt.Sprintf("%s is not installed on %s", p.Browser, m))
		return nil
	}
	v, ok := major(p.Installed)
	if !ok {
		p.Problems = append(p.Problems, fmt.Sprintf("%s's version %q on %s has no major number", p.Browser, p.Installed, m))
		return nil
	}
	p.Version = strconv.Itoa(v)
	p.Name = p.Browser + "-" + p.Version + "-macos"
	if _, ok := builtins[p.Name]; ok {
		p.InPlace = true
		p.Previous = p.Name
	} else {
		newest := 0
		for _, b := range builtins {
			bv, err := strconv.Atoi(b.Version)
			if err != nil || b.Browser != p.Browser || b.Platform != "macos" {
				continue
			}
			if bv > newest {
				newest = bv
				p.Previous = b.Name
			}
		}
		switch {
		case p.Previous == "":
			p.Problems = append(p.Problems, fmt.Sprintf("no built-in %s profile to start %s from", p.Browser, p.Name))
		case newest > v:
			p.Problems = append(p.Problems, fmt.Sprintf("%s %s on %s is older than the newest profile, %s: update it", p.Browser, p.Installed, m, p.Previous))
		}
	}
	for _, b := range rep.Browsers {
		if b.Name == p.Browser && b.Unapproved {
			p.Problems = append(p.Problems, fmt.Sprintf("Gatekeeper has not approved %s's first launch on %s, so opening it shows a dialog: open it once at that Mac's screen, or run\n    %s",
				b.App, m, onMachine(p.Host, "xattr -d com.apple.quarantine "+shellQuote(b.App))))
		}
	}
	if p.Host != "" && rep.ConsoleUser != rep.User {
		p.Problems = append(p.Problems, fmt.Sprintf("%s's screen belongs to %q, not %s (\"root\": the login window); browsers opened over ssh show on the logged-in session: log in as %s there (Screen Sharing works)",
			m, rep.ConsoleUser, rep.User, rep.User))
	}
	if !rep.CATrusted {
		p.Problems = append(p.Problems, fmt.Sprintf("the capture CA is not trusted on %s (Safari stops at a warning, Firefox's pages across hosts fail, Chrome ignores Alt-Svc); at that Mac's screen (it asks for the password):\n    security add-trusted-cert -r trustRoot -p ssl -k ~/Library/Keychains/login.keychain-db %s",
			m, shellQuote(rep.CAPath)))
	}
	if !rep.Resolver {
		msg := fmt.Sprintf("/etc/resolver/test does not send *.test to the capture tool on %s; create it once:\n    %s", m, onMachine(p.Host, "sudo mkdir -p /etc/resolver && printf 'nameserver 127.0.0.1\\nport 15353\\n' | sudo tee /etc/resolver/test"))
		if p.Browser == "safari" {
			p.Problems = append(p.Problems, msg)
		} else {
			p.Warnings = append(p.Warnings, msg+" (only Safari needs it)")
		}
	}
	if err := r.planCaptures(p, rep, cfg); err != nil {
		return err
	}
	p.CapturesFrom = inherit(p, builtins)
	if p.Browser != "safari" {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s updates itself; an update applied between two launches splits the run, which the tool then refuses (every capture must name %s, and the version must still be %s afterwards)",
			p.Browser, p.Name, p.Installed))
	}

	return nil
}

// onMachine is a command line run on host ("" here).
func onMachine(host, line string) string {
	if host == "" {
		return line
	}

	return "ssh -t " + host + " " + shellQuote(line)
}

// planCaptures lists the scenarios p captures and what they need.
func (r *refreshRun) planCaptures(p *plan, rep *preflightReport, cfg config) error {
	var set []server.Planned
	switch {
	case cfg.scenarios != "":
		for entry := range strings.SplitSeq(cfg.scenarios, ",") {
			name, n, _ := strings.Cut(entry, ":")
			samples := 3
			if n != "" {
				var err error
				if samples, err = strconv.Atoi(n); err != nil || samples < 1 {
					return fmt.Errorf("-scenarios %q: samples must be a positive number", entry)
				}
			}
			sc, err := scenarioFor(name)
			if err != nil {
				return err
			}
			set = append(set, server.Planned{
				Scenario: sc,
				Samples:  samples,
			})
		}
	case r.Mode == modeQuick:
		set = server.QuickScenarios
	default:
		set = server.FullScenarios()
		replays, err := replayScenarios()
		if err != nil {
			return err
		}
		for _, sc := range replays {
			set = append(set, server.Planned{
				Scenario: sc,
				Samples:  3,
			})
		}
	}
	shared := p.Browser == "safari"
	for _, s := range set {
		sc := s.Scenario
		switch {
		case shared && p.Host != "" && sc.Clicks():
			p.Warnings = append(p.Warnings, fmt.Sprintf("skipping %s: Safari is clicked through the system's mouse events, which an ssh session cannot send; the profile keeps or inherits what it has", sc.Name))
			continue
		case shared && sc.ProxyAuth:
			p.Warnings = append(p.Warnings, fmt.Sprintf("skipping %s: Safari's proxy credentials need the com.apple.proxy.http.global device profile and a person typing them (docs/capturing.md, Proxy scenarios); the profile keeps or inherits what it has, capture it by hand", sc.Name))
			continue
		case sc.NeedsAioquic() && rep.Aioquic != "":
			p.Problems = append(p.Problems, fmt.Sprintf("%s needs python3 with aioquic on %s: %s; install it with\n    %s", sc.Name, r.machine(p), rep.Aioquic, onMachine(p.Host, "python3 -m pip install --user aioquic")))
		}
		// Chrome and Firefox refuse QUIC with the capture CA's
		// certificates unless told otherwise (-h3-local-roots). The
		// committed captures keep both kinds for h3-discovery and
		// h3-page, the unforced one for h3-blackhole (its QUIC never
		// answers) and the forced one for the rest.
		forced := !shared && sc.HTTP3() && sc != server.H3Blackhole
		unforced := shared || !sc.HTTP3() || sc == server.H3Discovery || sc == server.H3Page || sc == server.H3Blackhole
		for _, lr := range []bool{false, true} {
			if (lr && !forced) || (!lr && !unforced) {
				continue
			}
			c := planned{
				Scenario:   sc.Name,
				Samples:    s.Samples,
				LocalRoots: lr,
				Estimate:   estimate(sc, p.Browser, s.Samples),
			}
			p.Captures = append(p.Captures, c)
			p.Estimate += c.Estimate
		}
	}
	// Unforced runs first, then the forced ones: one capture tool run each.
	slices.SortStableFunc(p.Captures, func(a, b planned) int {
		switch {
		case a.LocalRoots == b.LocalRoots:
			return 0
		case a.LocalRoots:
			return 1
		default:
			return -1
		}
	})
	if !slices.ContainsFunc(p.Captures, func(c planned) bool {
		return c.Scenario == capture.DefaultScenario
	}) && !cfg.captureOnly {
		return fmt.Errorf("%s: the default scenario (%s) must be captured: the profile comes from it", p.Target, capture.DefaultScenario)
	}
	if shared {
		p.Manual = append(p.Manual, fmt.Sprintf("Safari on %s: no content blocker or other extension enabled, default privacy settings; it opens tabs in your own window", r.machine(p)))
		if slices.ContainsFunc(set, func(s server.Planned) bool {
			return s.Scenario.Proxy != ""
		}) && rep.SafariProxy != "pac" {
			p.Problems = append(p.Problems, fmt.Sprintf("the proxy scenarios need the system to send Safari to the capture tool's proxy script on %s (now: %s):\n    %s\n  and after the run:\n    %s",
				r.machine(p), rep.SafariProxy,
				onMachine(p.Host, "sudo networksetup -setautoproxyurl <service> http://127.0.0.1:15356/mimicry.pac"),
				onMachine(p.Host, "sudo networksetup -setautoproxystate <service> off")))
		}
		if p.Host == "" && slices.ContainsFunc(set, func(s server.Planned) bool {
			return s.Scenario.Clicks()
		}) {
			p.Manual = append(p.Manual, "nav-redirect-click clicks Safari through the system's mouse events: the app running the tool needs Accessibility access, and hands off the mouse")
		}
	}

	return nil
}

// scenarioFor is the scenario named name: a built-in one or
// replay-<site> for testdata/pages/<site>.json.
func scenarioFor(name string) (*server.Scenario, error) {
	site, ok := strings.CutPrefix(name, "replay-")
	if !ok {
		return server.ScenarioByName(name)
	}
	shape, err := pageshape.Load(filepath.Join(pagesDir, site+".json"))
	if err != nil {
		return nil, err
	}

	return server.Replay(shape)
}

// replayScenarios are the replays of every page in testdata/pages.
func replayScenarios() ([]*server.Scenario, error) {
	files, err := filepath.Glob(filepath.Join(pagesDir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]*server.Scenario, 0, len(files))
	for _, f := range files {
		sc, err := scenarioFor("replay-" + strings.TrimSuffix(filepath.Base(f), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}

	return out, nil
}

// estimate is a ceiling on how long samples of sc take: each launches the
// browser (Chrome waits out its warm-up first) and runs the page for its
// length, or about 8 s for the single-host pages (slow-idle: 30 s).
func estimate(sc *server.Scenario, browser string, samples int) time.Duration {
	page := 8 * time.Second
	if _, length, multi := sc.MultiHost(); multi {
		page = length
	}
	if sc.Name == "slow-idle" {
		page = 30 * time.Second
	}
	launch := 6 * time.Second
	if browser == "chrome" {
		launch += 5 * time.Second
	}

	return time.Duration(samples) * (page + launch)
}

// inherit is captures_from for p: every scenario it does not capture, and
// every replay, from the profile holding the previous profile's captures
// of it. The default scenario is always captured.
func inherit(p *plan, builtins map[string]*profile.Profile) map[string]string {
	prev := builtins[p.Previous]
	if prev == nil {
		return nil
	}
	captured := map[string]bool{}
	for _, c := range p.Captures {
		captured[c.Scenario] = true
	}
	names := make([]string, 0, len(server.Scenarios))
	for _, sc := range server.Scenarios {
		names = append(names, sc.Name)
	}
	if replays, err := filepath.Glob(filepath.Join(pagesDir, "*.json")); err == nil {
		for _, f := range replays {
			names = append(names, "replay-"+strings.TrimSuffix(filepath.Base(f), ".json"))
		}
	}
	out := map[string]string{}
	for _, name := range names {
		if name == capture.DefaultScenario || captured[name] {
			continue
		}
		if src := capture.Source(prev, name); src != p.Name {
			out[name] = src
		}
	}
	if len(out) == 0 {
		return nil
	}

	return out
}

// planText is the plan for a person to read.
func (r *refreshRun) planText() string {
	var w strings.Builder
	fmt.Fprintf(&w, "\nRefresh plan (%s mode), staging in %s\n", r.Mode, r.Dir)
	for _, p := range r.Plans {
		fmt.Fprintf(&w, "\n%s on %s: installed %s", p.Target, r.machine(p), p.Installed)
		switch {
		case p.Name == "":
			w.WriteString("\n")
		case p.InPlace:
			fmt.Fprintf(&w, ", refreshing %s in place\n", p.Name)
		default:
			fmt.Fprintf(&w, ", new profile %s from %s\n", p.Name, p.Previous)
		}
		for _, c := range p.Captures {
			fmt.Fprintf(&w, "  capture %-34s %d sample(s), about %s\n", c.label(), c.Samples, c.Estimate.Round(time.Second))
		}
		bySource := map[string][]string{}
		for sc, src := range p.CapturesFrom {
			bySource[src] = append(bySource[src], sc)
		}
		for _, src := range slices.Sorted(maps.Keys(bySource)) {
			scs := bySource[src]
			slices.Sort(scs)
			fmt.Fprintf(&w, "  inherit from %s (%d): %s\n", src, len(scs), strings.Join(scs, ", "))
		}
		fmt.Fprintf(&w, "  estimated capture time: %s\n", p.Estimate.Round(time.Second))
		for _, s := range p.Manual {
			fmt.Fprintf(&w, "  manual: %s\n", s)
		}
		for _, s := range p.Warnings {
			fmt.Fprintf(&w, "  warning: %s\n", s)
		}
		for _, s := range p.Problems {
			fmt.Fprintf(&w, "  PROBLEM: %s\n", s)
		}
	}
	var total time.Duration
	for _, steps := range r.schedule() {
		var d time.Duration
		for _, step := range steps {
			var longest time.Duration
			for _, p := range step {
				longest = max(longest, p.Estimate)
			}
			if len(step) > 1 {
				fmt.Fprintf(&w, "\n%s capture at once on %s (-serial: one after the other).", planTargets(step), r.machine(step[0]))
			}
			d += longest
		}
		total = max(total, d)
	}
	fmt.Fprintf(&w, "\nEstimated capture time: about %s (a ceiling: pages often finish early), then per profile the generators and tests (several minutes) and realcheck (a few minutes, on its machine's screen).\n\n",
		total.Round(time.Second))

	return w.String()
}

// schedule groups the plans by machine (each machine's run at once), and
// each machine's into steps run one after the other, each step's plans at
// once: Chrome and Firefox together in quick mode, anything else alone.
func (r *refreshRun) schedule() map[string][][]*plan {
	out := map[string][][]*plan{}
	for _, p := range r.Plans {
		steps := out[p.Host]
		i := -1
		if r.Mode == modeQuick && !r.Serial && p.Browser != "safari" {
			i = slices.IndexFunc(steps, func(step []*plan) bool {
				return step[0].Browser != "safari"
			})
		}
		if i >= 0 {
			steps[i] = append(steps[i], p)
		} else {
			steps = append(steps, []*plan{
				p,
			})
		}
		out[p.Host] = steps
	}

	return out
}

func planTargets(ps []*plan) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Target
	}

	return strings.Join(names, " and ")
}
