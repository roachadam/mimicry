package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/pageshape"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
	"github.com/roachadam/mimicry/internal/platform"
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
	// Listen is this Mac's address on the iPhone's network (-listen) and
	// IOSDevice the iPhone's UDID (-ios-device, "" for any), for
	// safari-ios.
	Listen    string `json:"listen,omitempty"`
	IOSDevice string `json:"iosDevice,omitempty"`
	// WSL is the WSL distribution on -linux's host the Linux browsers run
	// in (-wsl, "" for a Linux machine of its own), as WSLUser (-wsl-user,
	// "" for the distribution's default user).
	WSL     string `json:"wsl,omitempty"`
	WSLUser string `json:"wslUser,omitempty"`
}

// plan is what the run does for one browser.
type plan struct {
	Target  string `json:"target"`
	Browser string `json:"browser"`
	// Tool is the capture tool's name for the browser when it is not
	// Browser (target.tool).
	Tool string `json:"tool,omitempty"`
	// Host is the ssh host the browser runs on, "" for this Mac.
	Host string `json:"host,omitempty"`
	// Platform is the profile's platform: platformMacOS ("" in runs saved
	// before Windows), platformWindows, platformLinux or platformIOS.
	Platform string `json:"platform,omitempty"`
	// NewPlatform is the first profile of the browser on Platform: copied
	// from another platform's, or for a Chromium browser with none to copy
	// (Edge's first), from Chrome's, so it captures every scenario and
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

// tool is the capture tool's name for p's browser (-browsers).
func (p *plan) tool() string {
	if p.Tool != "" {
		return p.Tool
	}

	return p.Browser
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
	// Driver is the iPhone's (browser.IOSDriver), empty for other browsers.
	Driver string `json:"driver,omitempty"`
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
	// Set on Windows and Linux only: Platform is "windows" or "linux",
	// Admin whether the ssh user is an administrator (Windows; certutil
	// needs it), Certutil its path, Missing everything that stops a
	// capture there, and UnprivilegedPortStart the lowest port a user may
	// bind (Linux; the urls scenario forwards 443).
	Platform              string   `json:"platform,omitempty"`
	Admin                 bool     `json:"admin,omitempty"`
	Certutil              string   `json:"certutil,omitempty"`
	Missing               []string `json:"missing,omitempty"`
	UnprivilegedPortStart int      `json:"unprivilegedPortStart,omitempty"`
}

// machine is where p's browser runs.
func (r *refreshRun) machine(p *plan) *machine {
	if p.Host == "" {
		m := &machine{
			dir: r.Dir,
		}
		if p.Platform == platformIOS {
			m.iphone = true
			m.iosDevice = r.IOSDevice
		}

		return m
	}

	m := &machine{
		host:    p.Host,
		dir:     "mimicry-capture/refresh-" + r.Stamp,
		windows: p.Platform == platformWindows,
	}
	if p.Platform == platformLinux {
		m.linux, m.wsl, m.wslUser = true, r.WSL, r.WSLUser
	}

	return m
}

// windowsDir is m.dir as Windows names it.
func (m *machine) windowsDir() string {
	return `%USERPROFILE%\` + strings.ReplaceAll(m.dir, "/", `\`) + `\`
}

// captureArgs are the capture tool's arguments every run of it on m
// takes: on Windows and Linux, the capture CA in the working directory, so
// nothing stays in the user's profile; for the iPhone, its UDID when given.
func (m *machine) captureArgs(args ...string) []string {
	if m.windows || m.linux {
		args = append(args, "-ca-dir", "ca")
	}

	if m.iosDevice != "" {
		args = append(args, "-ios-device", m.iosDevice)
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

	bins := map[string]string{}
	reports := map[string]*preflightReport{}

	for _, name := range cfg.targets {
		t := targets[name]
		p := &plan{
			Target:   name,
			Browser:  t.browser,
			Tool:     t.tool,
			Platform: t.platform(),
		}

		switch t.remote {
		case remoteMini:
			p.Host = cfg.mini
		case remoteWindows:
			p.Host = cfg.windows
		case remoteLinux:
			p.Host = cfg.linux
		}

		r.Plans = append(r.Plans, p)

		// The iPhone's preflight runs on this Mac too, with its UDID.
		m := r.machine(p)

		rep, ok := reports[m.String()]
		if !ok {
			bin, built := bins[m.goos()]
			if !built {
				log.Printf("building the capture tool for %s", m.goos())

				if bin, err = build(ctx, r.Dir, "capture", m.goos()); err != nil {
					return err
				}

				bins[m.goos()] = bin
			}

			if err := m.put(ctx, bin); err != nil {
				return err
			}

			log.Printf("preflight on %s", m)

			if rep, err = runPreflight(ctx, m); err != nil {
				return err
			}

			reports[m.String()] = rep
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
func loadProfiles(dir string) (map[string]*profile.Spec, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}

	out := map[string]*profile.Spec{}
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
func (r *refreshRun) planTarget(p *plan, rep *preflightReport, builtins map[string]*profile.Spec, cfg config) error {
	m := r.machine(p)
	switch {
	case m.windows:
		// Everything the PC lacks, even with no browser to plan.
		r.checkWindows(p, rep)
	case m.linux:
		r.checkLinux(p, rep)
	}

	p.Installed = rep.installedVersion(p.tool())

	if p.Installed == "" {
		if m.iphone {
			p.Problems = append(p.Problems, fmt.Sprintf("no paired iPhone with %s installed is connected to this Mac (xcrun devicectl list devices; -ios-device picks one by UDID): connect it by cable, unlock it and trust this Mac", browser.IOSApp(p.tool())))
		} else {
			p.Problems = append(p.Problems, fmt.Sprintf("%s is not installed on %s", p.Browser, m))
		}

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
		p.choosePrevious(builtins, v, m)
	}

	switch {
	case m.iphone:
		r.checkIPhone(p)
	case !m.windows && !m.linux:
		r.checkMac(p, rep)
	}

	if err := r.planCaptures(p, rep, cfg); err != nil {
		return err
	}

	if m.linux && p.needsPort443() && rep.UnprivilegedPortStart > 443 {
		if m.wsl != "" {
			p.Manual = append(p.Manual, fmt.Sprintf("a scenario names https://'s default port, which the capture tool forwards from 443; refresh lowers net.ipv4.ip_unprivileged_port_start from %d to 443 as root in %s before each capture step, whenever it is above 443 (it lasts until the WSL VM stops)",
				rep.UnprivilegedPortStart, m))
		} else {
			p.Problems = append(p.Problems, fmt.Sprintf("a scenario names https://'s default port, which the capture tool forwards from 443, but a user on %s may bind ports from %d only; there:\n    sudo sysctl -w net.ipv4.ip_unprivileged_port_start=443   # and in /etc/sysctl.d/ to keep it",
				m, rep.UnprivilegedPortStart))
		}
	}

	// The first profile on a platform inherits nothing, unless the
	// platform shares another's network stack (Safari on iOS, decision
	// 0051): it then inherits what it does not capture from that
	// platform's profile it starts from.
	if !p.NewPlatform || platform.Stack(p.Browser, p.Platform) != "" {
		p.CapturesFrom = inherit(p, builtins)
	}

	if p.Browser != "safari" {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s updates itself; an update applied between two launches splits the run, which the tool then refuses (every capture must name %s, and the version must still be %s afterwards)",
			p.Browser, p.Name, p.Installed))
	}

	return nil
}

// choosePrevious sets p.Previous for p.Name, which is no built-in profile
// (v its major version): the browser's newest profile on p.Platform. The
// first one there (NewPlatform) starts from the browser's macOS profile of
// its version, or its newest macOS one; a Chromium browser with neither
// (Edge's first profile) from the newest Chrome profile on p.Platform, or
// on macOS, whose engine and network stack it shares. Everything it keeps
// of that profile is recaptured or regenerated from its own captures, or
// hand-written and checked against them.
func (p *plan) choosePrevious(builtins map[string]*profile.Spec, v int, m *machine) {
	var newest int
	if p.Previous, newest = newestProfile(builtins, p.Browser, p.Platform); p.Previous != "" {
		if newest > v {
			p.Problems = append(p.Problems, fmt.Sprintf("%s %s on %s is older than the newest profile, %s: update it", p.Browser, p.Installed, m, p.Previous))
		}

		return
	}

	p.NewPlatform = true

	if name := p.Browser + "-" + p.Version + "-" + platformMacOS; builtins[name] != nil {
		p.Previous = name

		return
	}

	if p.Previous, _ = newestProfile(builtins, p.Browser, platformMacOS); p.Previous != "" {
		return
	}

	if capture.Chromium(p.Browser) {
		for _, on := range []string{p.Platform, platformMacOS} {
			if p.Previous, _ = newestProfile(builtins, "chrome", on); p.Previous != "" {
				return
			}
		}
	}

	p.NewPlatform = false
	p.Problems = append(p.Problems, fmt.Sprintf("no built-in %s profile to start %s from", p.Browser, p.Name))
}

// newestProfile is the built-in profile of browser on platform with the
// highest version, and that version; "" if there is none.
func newestProfile(builtins map[string]*profile.Spec, browser, platform string) (string, int) {
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
// capture and the test hosts reach the browser through its own flags or
// prefs, so neither is checked.
func (r *refreshRun) checkWindows(p *plan, rep *preflightReport) {
	m := r.machine(p)

	if rep.Platform != platformWindows {
		p.Problems = append(p.Problems, fmt.Sprintf("the capture tool's preflight on %s does not report Windows (platform %q)", m, rep.Platform))
	}

	for _, s := range rep.Missing {
		p.Problems = append(p.Problems, fmt.Sprintf("%s: %s", m, s))
	}

	hosts := "Chromium browsers (Chrome, Edge) reach the capture hosts through --host-resolver-rules"
	if p.Browser == "firefox" {
		hosts = "Firefox reaches the capture hosts through network.dns.localDomains in its throwaway profile and trusts the capture CA from the Root store (security.enterprise_roots.enabled)"
	}

	p.Manual = append(p.Manual,
		fmt.Sprintf("%s logged in at %s's screen and the PC left alone: capture opens a visible %s on that desktop through a one-shot scheduled task (never headless), so an ssh session's own desktop is never used", rep.User, m, p.Browser),
		fmt.Sprintf("capture adds its CA to %s's LocalMachine Root store (certutil -addstore Root) when it starts and deletes it (certutil -delstore Root) when it stops, both in capture.log; refresh then runs capture -cleanup there (the CA, leftover mimicry-capture-* scheduled tasks), even after a failure or ctrl-C", m),
		fmt.Sprintf("%s: no hosts file or DNS change on %s", hosts, m))
}

// checkLinux adds what a capture on a Linux machine needs and rep lacks to
// p, and how the browsers run there: on the tool's own Xvfb display
// unless the session has one (browser.EnsureDisplay), each trusting the
// capture CA in its throwaway profile.
func (r *refreshRun) checkLinux(p *plan, rep *preflightReport) {
	m := r.machine(p)

	if rep.Platform != platformLinux {
		p.Problems = append(p.Problems, fmt.Sprintf("the capture tool's preflight on %s does not report Linux (platform %q)", m, rep.Platform))
	}

	for _, s := range rep.Missing {
		p.Problems = append(p.Problems, fmt.Sprintf("%s: %s", m, s))
	}

	if rep.User == "root" {
		fix := "ssh to " + m.String() + " as a user that is not root"
		if m.wsl != "" {
			fix = "pass -wsl-user with a user of the distribution that is not root"
		}

		p.Problems = append(p.Problems, fmt.Sprintf("the browsers would run as root on %s, which Chromium browsers refuse without --no-sandbox: %s", m, fix))
	}

	trust := "Chromium browsers (Chrome, Edge) trust the capture CA in an NSS database under their throwaway profile (HOME is that profile for the run)"
	if p.Browser == "firefox" {
		trust = "Firefox trusts the capture CA in its throwaway profile's own certificate database, on pages across hosts"
	}

	changed := "nothing outside the run's directory on " + m.String() + " is changed"
	if m.wsl != "" {
		changed = "outside the run's directory on " + m.String() + ", only net.ipv4.ip_unprivileged_port_start may change: lowered to 443 when a scenario needs that port and users may not bind it"
	}

	p.Manual = append(p.Manual,
		fmt.Sprintf("%s opens on Xvfb, a virtual display the capture tool starts for the run (unless the session has $DISPLAY): an ordinary windowed browser, never headless, on no screen", p.Browser),
		fmt.Sprintf("%s; %s", trust, changed))
}

// checkIPhone adds to p what a capture of a browser on the iPhone needs:
// what this Mac can check, and what only someone holding the phone can
// set. The capture tool serves the iPhone on r.Listen, and a DNS stub
// there, for the run only.
func (r *refreshRun) checkIPhone(p *plan) {
	app := browser.IOSApp(p.tool())

	if err := exec.Command("xcrun", "--find", "devicectl").Run(); err != nil { //nolint:noctx // a quick local query while planning
		p.Problems = append(p.Problems, fmt.Sprintf("devicectl, which opens each capture URL in the iPhone's %s, is not available on this Mac (it comes with Xcode: xcrun devicectl)", app))
	}

	out, err := exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate").Output() //nolint:noctx // a quick local query while planning
	if err == nil && !strings.Contains(string(out), "State = 0") {
		p.Warnings = append(p.Warnings, "the macOS firewall is on: it must let the capture tool (built afresh each run) accept the iPhone's connections; allow it when macOS asks, or turn the firewall off for the run")
	}

	once := "once (docs/capturing.md, Safari on an iPhone): the capture CA installed and fully trusted on the iPhone; iCloud Private Relay off"
	if p.Browser == "safari" {
		once += "; Settings > Apps > Safari: no content blocker or other extension"
	}

	p.Manual = append(p.Manual,
		once,
		fmt.Sprintf("before capturing: the iPhone on this Mac's Wi-Fi network, Limit IP Address Tracking off for it, and its DNS (Settings > Wi-Fi > (i) > Configure DNS > Manual) %s alone; afterwards, DNS back to Automatic: without the capture tool running the iPhone has no DNS", r.Listen),
		fmt.Sprintf("while capturing: the iPhone connected by cable, unlocked, Auto-Lock Never, on power and left alone but for the proxy settings; devicectl opens each sample's URL in the phone's own %s, bringing it to the front, and its tabs stay open (close them afterwards)", app),
	)
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

	case r.machine(p).iphone:
		// Whatever -mode: the fingerprint layer and the rules checked
		// against macOS Safari's captures, which the rest is inherited
		// from (decision 0051).
		set = server.IOSScenarios()
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
	iphone := r.machine(p).iphone

	for _, s := range set {
		sc := s.Scenario
		why := sc.UnsupportedOnIOS()

		switch {
		case iphone && why != "":
			p.Warnings = append(p.Warnings, fmt.Sprintf("skipping %s, unsupported on the iPhone: %s", sc.Name, why))
			continue

		case iphone && sc.Clicks():
			p.Warnings = append(p.Warnings, fmt.Sprintf("skipping %s: %s (docs/capturing.md, Safari on an iPhone)", sc.Name, browser.IOSNoTaps))
			continue

		case shared && p.Host != "" && sc.Clicks():
			p.Warnings = append(p.Warnings, fmt.Sprintf("skipping %s: Safari is clicked through the system's mouse events, which an ssh session cannot send; the profile keeps or inherits what it has", sc.Name))
			continue

		case shared && !iphone && sc.ProxyAuth:
			p.Warnings = append(p.Warnings, fmt.Sprintf("skipping %s: Safari's proxy credentials need the com.apple.proxy.http.global device profile and a person typing them (docs/capturing.md, Proxy scenarios); the profile keeps or inherits what it has, capture it by hand", sc.Name))
			continue

		case sc.NeedsAioquic() && rep.Aioquic != "":
			install := onMachine(p.Host, "python3 -m pip install --user aioquic")
			switch m := r.machine(p); {
			case m.windows:
				install = "python -m pip install --user aioquic   # in PowerShell on " + p.Host
			case m.linux:
				install = "python3 -m pip install --user --break-system-packages 'aioquic>=1.0'   # as the capture user on " + m.String() + " (distributions' python3-aioquic may predate QUIC version 2)"
			}

			p.Problems = append(p.Problems, fmt.Sprintf("%s needs python3 with aioquic on %s: %s; install it with\n    %s", sc.Name, r.machine(p), rep.Aioquic, install))
		}

		// Chromium browsers and Firefox on a computer refuse QUIC with the
		// capture CA's certificates unless told otherwise
		// (-h3-local-roots). The committed captures keep both kinds for
		// h3-discovery and h3-page, the unforced one for h3-blackhole (its
		// QUIC never answers) and the forced one for the rest. A browser on
		// the iPhone takes no such flag: unforced only.
		forced := !shared && !iphone && sc.HTTP3() && sc != server.H3Blackhole
		unforced := shared || iphone || !sc.HTTP3() || sc == server.H3Discovery || sc == server.H3Page || sc == server.H3Blackhole

		for _, lr := range []bool{false, true} {
			if (lr && !forced) || (!lr && !unforced) {
				continue
			}

			c := planned{
				Scenario:   sc.Name,
				Samples:    s.Samples,
				LocalRoots: lr,
				Estimate:   estimate(sc, p.tool(), s.Samples),
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

	if iphone {
		// The direct scenarios first, unattended; then the proxy ones:
		// the capture tool asks for the iPhone's proxy setting and waits
		// for Enter.
		proxied := map[string]bool{}

		for _, s := range set {
			proxied[s.Scenario.Name] = s.Scenario.Proxy != ""
		}

		slices.SortStableFunc(p.Captures, func(a, b planned) int {
			switch {
			case proxied[a.Scenario] == proxied[b.Scenario]:
				return 0
			case proxied[a.Scenario]:
				return 1
			default:
				return -1
			}
		})

		if slices.ContainsFunc(p.Captures, func(c planned) bool {
			return proxied[c.Scenario]
		}) {
			p.Manual = append(p.Manual, fmt.Sprintf("after the direct scenarios, someone at the iPhone: the capture tool stops before the proxy scenarios, prints what to enter (Settings > Wi-Fi > (i) > Configure Proxy > Manual, server %s, port %d, Authentication off) and waits for Enter here; afterwards Configure Proxy > Off",
				r.Listen, browser.IOSProxyPort))
		}

		for _, c := range p.Captures {
			if prompt := browser.IOSPrompt(p.tool(), c.Scenario); prompt != "" {
				p.Manual = append(p.Manual, fmt.Sprintf("during %s, someone at the iPhone: %s; the capture tool says when", c.Scenario, prompt))
			}
		}
	}

	if !slices.ContainsFunc(p.Captures, func(c planned) bool {
		return c.Scenario == capture.DefaultScenario
	}) && !cfg.captureOnly {
		return fmt.Errorf("%s: the default scenario (%s) must be captured: the profile comes from it", p.Target, capture.DefaultScenario)
	}

	if shared && !iphone {
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
// browser (a Chromium browser waits out its warm-up first; a browser on
// the iPhone is opened through devicectl) and runs the page for its
// length, or about 8 s for the single-host pages (slow-idle: 30 s). tool
// is the capture tool's name for the browser.
func estimate(sc *server.Scenario, tool string, samples int) time.Duration {
	page := 8 * time.Second
	if _, length, multi := sc.MultiHost(); multi {
		page = length
	}

	if sc.Name == "slow-idle" {
		page = 30 * time.Second
	}

	launch := 6 * time.Second

	switch {
	case capture.Chromium(tool):
		launch += 5 * time.Second
	case tool == browser.IOSSafariName, tool == browser.IOSChromeName, tool == browser.IOSFirefoxName:
		launch = 15 * time.Second
	}

	return time.Duration(samples) * (page + launch)
}

// inherit is capturesFrom for p: every scenario it does not capture, and
// every replay, from the profile holding the previous profile's captures
// of it. The default scenario is always captured; on iOS a scenario whose
// macOS captures differ from the iPhone's (Scenario.NotInheritedOnIOS) is
// not inherited.
func inherit(p *plan, builtins map[string]*profile.Spec) map[string]string {
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
		if p.Platform == platformIOS && sc.NotInheritedOnIOS() != "" {
			continue
		}

		names = append(names, sc.Name)
	}

	if replays, err := filepath.Glob(filepath.Join(pagesDir, "*.json")); err == nil {
		for _, f := range replays {
			name := "replay-" + strings.TrimSuffix(filepath.Base(f), ".json")
			if p.Platform == platformIOS {
				if sc, err := scenarioFor(name); err != nil || sc.NotInheritedOnIOS() != "" {
					continue
				}
			}

			names = append(names, name)
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

		switch stack := platform.Stack(p.Browser, p.Platform); {
		case stack != "":
			fmt.Fprintf(&w, "  %s on %s shares %s's network stack (decision 0051): it captures the %s set whatever -mode, and inherits every other scenario and replay from %s %s's captures\n",
				p.Browser, p.Platform, stack, p.Platform, stack, p.Browser)

		case p.NewPlatform && r.Scenarios == "":
			fmt.Fprintf(&w, "  first %s profile on %s: nothing there to inherit, so it captures the full set (every scenario and replay) whatever -mode, and inherits nothing; the generators redo what it keeps of %s from its captures\n",
				p.Browser, p.Platform, p.Previous)

		case p.NewPlatform:
			fmt.Fprintf(&w, "  first %s profile on %s: nothing there to inherit, so it inherits nothing; -scenarios replaces the full set it would capture, and the scenarios left out have no capture for %s\n",
				p.Browser, p.Platform, p.Name)
		}

		switch m := r.machine(p); {
		case m.windows:
			r.windowsSteps(&w, p, m)
		case m.linux:
			linuxSteps(&w, p, m)
		case m.iphone:
			fmt.Fprintf(&w, "  on the iPhone, served from this Mac: capture -browsers %s -listen %s -ios-driver %s: the capture servers and a DNS stub (port 53, forwarding other names to this Mac's resolver) on %s for the run; devicectl opens each sample's URL in the phone's own %s, over USB; realcheck is not run (it drives browsers on this Mac)\n",
				p.tool(), r.Listen, browser.IOSDevicectl, r.Listen, browser.IOSApp(p.tool()))
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
	fmt.Fprintf(w, "    2. capture.exe -browsers %s -ca-dir ca -browser-data browser-data: the unforced scenarios, then the -h3-local-roots ones; %s opens on the logged-in user's desktop through a one-shot scheduled task\n", p.Browser, p.Browser)
	fmt.Fprintf(w, "    3. the captures (%s.*) copied back after each run (tar there, scp) into %s, checked, and once derived installed into %s\n",
		name, r.stagedCaptures(p), capturesDir)
	fmt.Fprintf(w, "    4. at the end, even after a failure or ctrl-C: whatever runs from that directory stopped, capture.exe -cleanup (the capture CA out of the Root store, leftover scheduled tasks deleted), the directory %s\n", fate)
}

// linuxSteps is what the run does on p's Linux machine m, for the plan.
func linuxSteps(w *strings.Builder, p *plan, m *machine) {
	how := "over ssh"
	if m.wsl != "" {
		user := m.wslUser
		if user == "" {
			user = "its default user"
		}

		how = "each step wsl.exe -d " + m.wsl + " as " + user + ", over ssh to the Windows PC"
	}

	fmt.Fprintf(w, "  on %s (%s) in ~/%s:\n", m, how, m.dir)
	fmt.Fprintf(w, "    1. built here for linux/amd64 and copied there (a tar stream): capture (realcheck after install); capture -preflight ran there\n")
	fmt.Fprintf(w, "    2. capture -browsers %s -ca-dir ca: the unforced scenarios, then the -h3-local-roots ones; %s opens on Xvfb unless the session has a display, trusting the capture CA in its throwaway profile\n", p.Browser, p.Browser)
	fmt.Fprintf(w, "    3. the captures copied back after each run (a tar stream) into the staging directory, checked, and once derived installed into %s\n", capturesDir)
}

// schedule groups the plans by machine (each machine's run at once), and
// each machine's into steps run one after the other, each step's plans at
// once: Chrome, Firefox and Edge together on a Mac in quick mode, anything
// else alone (a first profile on its platform too: it captures the full
// set). A Windows PC's browsers take turns: each capture run trusts the CA
// in the Root store only while it runs, and removing it as one run ends
// would pull it from under the other. A Linux machine's take turns too,
// and with the Windows PC's when it is that PC's WSL (one host). The
// iPhone's browsers take turns on the one phone.
func (r *refreshRun) schedule() map[string][][]*plan {
	out := map[string][][]*plan{}
	for _, p := range r.Plans {
		steps := out[p.Host]
		i := -1

		if r.Mode == modeQuick && !r.Serial && together(p) {
			i = slices.IndexFunc(steps, func(step []*plan) bool {
				return together(step[0])
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

// together reports whether p captures at once with others of its kind on
// its Mac in quick mode (schedule): Chrome, Firefox or Edge, refreshing a
// profile it has on macOS.
func together(p *plan) bool {
	return p.Browser != "safari" && !p.NewPlatform && p.Platform != platformWindows && p.Platform != platformLinux && p.Platform != platformIOS
}

func planTargets(ps []*plan) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Target
	}

	return strings.Join(names, " and ")
}

// needsPort443 reports whether a scenario p captures names https://'s
// default port (server.Scenario.DefaultPort).
func (p *plan) needsPort443() bool {
	return slices.ContainsFunc(p.Captures, func(c planned) bool {
		sc, err := server.ScenarioByName(c.Scenario)

		return err == nil && sc.DefaultPort()
	})
}
