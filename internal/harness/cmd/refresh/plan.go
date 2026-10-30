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

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/pageshape"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

const (
	modeQuick = "quick"
	modeFull  = "full"

	capturesDir = "testdata/captures"
	pagesDir    = "testdata/pages"
	profilesDir = "../../profiles/data"
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
	// Scenarios is -scenarios, which replaces the mode's set.
	Scenarios string `json:"scenarios,omitempty"`
	// Keep leaves the Windows PC's working directory in place (-keep).
	Keep bool `json:"-"`
}

// plan is what the run does for one browser.
type plan struct {
	Target  string `json:"target"`
	Browser string `json:"browser"`
	// Host is the ssh host the browser runs on, "" for this Mac.
	Host string `json:"host,omitempty"`
	// Platform is the profile's platform: platformMacOS ("" in runs saved
	// before Windows) or platformWindows.
	Platform string `json:"platform,omitempty"`
	// NewPlatform is the first profile of the browser on Platform: copied
	// from another platform's, so it captures every scenario and
	// inherits nothing.
	NewPlatform bool `json:"newPlatform,omitempty"`
	// Installed is the browser's version where it runs.
	Installed string `json:"installed"`
	// Name and Version (the major) are the profile's.
	Name    string `json:"name"`
	Version string `json:"version"`
	// Previous is the profile the new one is copied from: the browser's
	// newest older one, or the profile itself when InPlace.
	Previous string    `json:"previous"`
	InPlace  bool      `json:"inPlace"`
	Captures []planned `json:"captures"`
	// CapturesFrom is the profile's capturesFrom once refreshed.
	CapturesFrom map[string]string `json:"capturesFrom,omitempty"`
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
	LocalRoots bool          `json:"localRoots,omitempty"`
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
	UserAgent string  `json:"userAgent"`
	Samples   int     `json:"samples"`
	Seconds   float64 `json:"seconds"`
	File      string  `json:"file"`
}

// preflightReport is the capture tool's -preflight output.
type preflightReport struct {
	User        string `json:"user"`
	ConsoleUser string `json:"consoleUser"`
	Browsers    []struct {
		Name       string `json:"name"`
		App        string `json:"app"`
		Version    string `json:"version"`
		Unapproved bool   `json:"unapproved"`
	} `json:"browsers"`
	CAPath      string `json:"caPath"`
	CATrusted   bool   `json:"caTrusted"`
	Resolver    bool   `json:"resolver"`
	Aioquic     string `json:"aioquic"`
	SafariProxy string `json:"safariProxy"`
	// Set on Windows only: Platform is "windows", Admin whether the ssh
	// user is an administrator (certutil needs it), Certutil its path, and
	// Missing everything that stops a capture there.
	Platform string   `json:"platform,omitempty"`
	Admin    bool     `json:"admin,omitempty"`
	Certutil string   `json:"certutil,omitempty"`
	Missing  []string `json:"missing,omitempty"`
}

// machine is where p's browser runs.
func (r *refreshRun) machine(p *plan) *machine {
	if p.Host == "" {
		return &machine{
			dir: r.Dir,
		}
	}

	return &machine{
		host:    p.Host,
		dir:     "mimicry-capture/refresh-" + r.Stamp,
		windows: p.Platform == platformWindows,
	}
}

// windowsDir is m.dir as Windows names it.
func (m *machine) windowsDir() string {
	return `%USERPROFILE%\` + strings.ReplaceAll(m.dir, "/", `\`) + `\`
}

// captureArgs are the capture tool's arguments every run of it on m
// takes: on Windows, the capture CA in the working directory, so nothing
// stays in the user's profile.
func (m *machine) captureArgs(args ...string) []string {
	if m.windows {
		args = append(args, "-ca-dir", "ca")
	}

	return args
}

// bin is the path of the tool's binary name on m.
func (m *machine) bin(name string) string {
	if m.host == "" {
		return filepath.Join(m.dir, name)
	}

	if m.windows {
		return `.\` + name + ".exe"
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

// preflight builds the capture tool for each platform, runs its
// -preflight where each browser runs and plans each browser's refresh.
func (r *refreshRun) preflight(ctx context.Context, cfg config) error {
	if err := os.MkdirAll(r.Dir, 0o750); err != nil {
		return err
	}

	builtins, err := loadProfiles(profilesDir)
	if err != nil {
		return err
	}

	bins := map[bool]string{}
	reports := map[string]*preflightReport{}

	for _, name := range cfg.targets {
		t := targets[name]
		p := &plan{
			Target:   name,
			Browser:  t.browser,
			Platform: t.platform(),
		}

		switch t.remote {
		case remoteMini:
			p.Host = cfg.mini
		case remoteWindows:
			p.Host = cfg.windows
		}

		r.Plans = append(r.Plans, p)

		rep, ok := reports[p.Host]

		if !ok {
			m := r.machine(p)

			bin, built := bins[m.windows]
			if !built {
				if m.windows {
					log.Printf("building the capture tool for windows/amd64")
				} else {
					log.Printf("building the capture tool")
				}

				if bin, err = build(ctx, r.Dir, "capture", m.windows); err != nil {
					return err
				}

				bins[m.windows] = bin
			}

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
	out, err := m.output(ctx, m.bin("capture"), m.captureArgs("-preflight")...)
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
	if m.windows {
		// Everything the PC lacks, even with no browser to plan.
		r.checkWindows(p, rep)
	}

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
	p.Name = p.Browser + "-" + p.Version + "-" + p.Platform

	if _, ok := builtins[p.Name]; ok {
		p.InPlace = true
		p.Previous = p.Name
	} else {
		var newest int

		p.Previous, newest = newestProfile(builtins, p.Browser, p.Platform)

		switch {
		case p.Previous == "" && p.Platform != platformMacOS:
			// The first profile on the platform starts from the macOS one of
			// its version, or the newest macOS one: everything it keeps is
			// recaptured or regenerated.
			p.Previous = p.Browser + "-" + p.Version + "-" + platformMacOS
			if builtins[p.Previous] == nil {
				p.Previous, _ = newestProfile(builtins, p.Browser, platformMacOS)
			}

			if p.Previous == "" {
				p.Problems = append(p.Problems, fmt.Sprintf("no built-in %s profile to start %s from", p.Browser, p.Name))
			} else {
				p.NewPlatform = true
			}

		case p.Previous == "":
			p.Problems = append(p.Problems, fmt.Sprintf("no built-in %s profile to start %s from", p.Browser, p.Name))
		case newest > v:
			p.Problems = append(p.Problems, fmt.Sprintf("%s %s on %s is older than the newest profile, %s: update it", p.Browser, p.Installed, m, p.Previous))
		}
	}

	if !m.windows {
		r.checkMac(p, rep)
	}

	if err := r.planCaptures(p, rep, cfg); err != nil {
		return err
	}

	if !p.NewPlatform {
		p.CapturesFrom = inherit(p, builtins)
	}

	if p.Browser != "safari" {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s updates itself; an update applied between two launches splits the run, which the tool then refuses (every capture must name %s, and the version must still be %s afterwards)",
			p.Browser, p.Name, p.Installed))
	}

	return nil
}

// newestProfile is the built-in profile of browser on platform with the
// highest version, and that version; "" if there is none.
func newestProfile(builtins map[string]*profile.Profile, browser, platform string) (string, int) {
	name, newest := "", 0

	for _, b := range builtins {
		bv, err := strconv.Atoi(b.Version)
		if err != nil || b.Browser != browser || b.Platform != platform {
			continue
		}

		if bv > newest {
			newest = bv
			name = b.Name
		}
	}

	return name, newest
}

// checkMac adds what a capture on a Mac needs and rep lacks to p.
func (r *refreshRun) checkMac(p *plan, rep *preflightReport) {
	m := r.machine(p)

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
}

// checkWindows adds what a capture on a Windows PC needs and rep lacks to
// p, and what happens to that PC. The capture CA is trusted only during a
// capture and the test hosts reach Chrome through its own flags, so
// neither is checked.
func (r *refreshRun) checkWindows(p *plan, rep *preflightReport) {
	m := r.machine(p)

	if rep.Platform != platformWindows {
		p.Problems = append(p.Problems, fmt.Sprintf("the capture tool's preflight on %s does not report Windows (platform %q)", m, rep.Platform))
	}

	for _, s := range rep.Missing {
		p.Problems = append(p.Problems, fmt.Sprintf("%s: %s", m, s))
	}

	p.Manual = append(p.Manual,
		fmt.Sprintf("%s logged in at %s's screen and the PC left alone: capture opens a visible Chrome on that desktop through a one-shot scheduled task (never headless), so an ssh session's own desktop is never used", rep.User, m),
		fmt.Sprintf("capture adds its CA to %s's LocalMachine Root store (certutil -addstore Root) when it starts and deletes it (certutil -delstore Root) when it stops, both in capture.log; refresh then runs capture -cleanup there (the CA, leftover mimicry-capture-* scheduled tasks), even after a failure or ctrl-C", m),
		fmt.Sprintf("Chrome reaches the capture hosts through --host-resolver-rules: no hosts file or DNS change on %s", m))
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

	case r.Mode == modeQuick && !p.NewPlatform:
		set = server.QuickScenarios
	default:
		// Full mode, or the first profile on a platform, which has no
		// earlier one there to inherit from.
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
			install := onMachine(p.Host, "python3 -m pip install --user aioquic")
			if r.machine(p).windows {
				install = "python -m pip install --user aioquic   # in PowerShell on " + p.Host
			}

			p.Problems = append(p.Problems, fmt.Sprintf("%s needs python3 with aioquic on %s: %s; install it with\n    %s", sc.Name, r.machine(p), rep.Aioquic, install))
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

// inherit is capturesFrom for p: every scenario it does not capture, and
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

		if p.NewPlatform {
			if r.Scenarios == "" {
				fmt.Fprintf(&w, "  first %s profile on %s: nothing there to inherit, so it captures the full set (every scenario and replay) whatever -mode, and inherits nothing; the generators redo what it keeps of %s from its captures\n",
					p.Browser, p.Platform, p.Previous)
			} else {
				fmt.Fprintf(&w, "  first %s profile on %s: nothing there to inherit, so it inherits nothing; -scenarios replaces the full set it would capture, and the scenarios left out have no capture for %s\n",
					p.Browser, p.Platform, p.Name)
			}
		}

		if m := r.machine(p); m.windows {
			r.windowsSteps(&w, p, m)
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

// windowsSteps is what the run does on p's Windows PC m, for the plan.
func (r *refreshRun) windowsSteps(w *strings.Builder, p *plan, m *machine) {
	files := `capture.exe`
	if slices.ContainsFunc(p.Captures, func(c planned) bool {
		return strings.HasPrefix(c.Scenario, "replay-")
	}) {
		files += ` and testdata\pages`
	}

	fate := "removed"
	if r.Keep {
		fate = "kept (-keep)"
	}

	name := p.Name
	if name == "" {
		name = p.Browser + "-<major>-" + platformWindows
	}

	fmt.Fprintf(w, "  on %s (Windows over ssh; each step a powershell -NoProfile command) in %s:\n", m, m.windowsDir())
	fmt.Fprintf(w, "    1. built here for windows/amd64 and copied there by scp: %s (realcheck.exe after install); capture.exe -preflight ran there\n", files)
	fmt.Fprintf(w, "    2. capture.exe -browsers %s -ca-dir ca -browser-data browser-data: the unforced scenarios, then the -h3-local-roots ones; Chrome opens on the logged-in user's desktop through a one-shot scheduled task\n", p.Browser)
	fmt.Fprintf(w, "    3. the captures (%s.*) copied back after each run (tar there, scp) into %s, checked, and once derived installed into %s\n",
		name, r.stagedCaptures(p), capturesDir)
	fmt.Fprintf(w, "    4. at the end, even after a failure or ctrl-C: whatever runs from that directory stopped, capture.exe -cleanup (the capture CA out of the Root store, leftover scheduled tasks deleted), the directory %s\n", fate)
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
