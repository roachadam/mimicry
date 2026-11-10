// Command refresh brings a browser's built-in profile up to the version
// installed where that browser is captured (chrome, firefox, edge, brave
// and safari-mini on the second Mac via -mini, safari here,
// chrome-windows, firefox-windows, edge-windows and brave-windows on a
// Windows PC via -windows, chrome-linux, firefox-linux and brave-linux on
// a Linux machine via -linux, safari-ios, chrome-ios and firefox-ios on an
// iPhone this Mac serves via -listen):
//
//	go run ./cmd/refresh -browser chrome,firefox -mode quick -mini <alias>   # in internal/harness/
//	go run ./cmd/refresh -browser chrome-windows,firefox-windows -windows <alias>
//	go run ./cmd/refresh -browser safari-ios,chrome-ios,firefox-ios -listen <this Mac's IPv4>
//
// The first profile of a browser (edge, brave) starts from the newest
// Chrome profile of its platform, whose engine it shares, and captures
// every scenario, inheriting nothing (decision 0035).
//
// Per browser it preflights, captures into a staging directory, derives the
// new profile and runs the generators, installs it, and verifies it with the
// e2e tests and cmd/realcheck, writing
// internal/harness/reports/refresh-<name>-<date>.md. -dry-run stops after the
// plan; -resume DIR continues a failed run; -capture-only and -derive-only
// stop before the repository changes.
package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"maps"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(log.Ltime)

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "refresh:", err)
		os.Exit(1)
	}
}

// config is the command line.
type config struct {
	targets     []string
	mode        string
	mini        string
	staging     string
	windows     string
	linux       string
	wsl         string
	wslUser     string
	keep        bool
	listen      string
	iosDevice   string
	chromeApp   string
	firefoxApp  string
	scenarios   string
	yes         bool
	dryRun      bool
	captureOnly bool
	deriveOnly  bool
	resume      string
	serial      bool
	verify      bool
	realcheck   bool
}

func run() error {
	var cfg config

	browsers := flag.String("browser", "", "comma-separated browsers to refresh: chrome, firefox, edge, brave (on -mini), safari (this Mac), safari-mini (the mini's Safari), chrome-windows, firefox-windows, edge-windows, brave-windows (on -windows), chrome-linux, firefox-linux, brave-linux (on -linux), safari-ios, chrome-ios, firefox-ios (the iPhone, served on -listen)")

	flag.StringVar(&cfg.mode, "mode", modeQuick, "quick (the fingerprint layer; the rest inherited from the previous version) or full (every scenario and replay)")
	flag.StringVar(&cfg.mini, "mini", "", "your ssh alias for the second Mac (chrome, firefox, edge, brave, safari-mini)")
	flag.StringVar(&cfg.windows, "windows", "", "your ssh alias for the Windows PC (chrome-windows, firefox-windows, edge-windows, brave-windows; required for them)")
	flag.StringVar(&cfg.linux, "linux", "", "your ssh alias for the Linux machine, or with -wsl for the Windows PC whose WSL distribution it is (chrome-linux, firefox-linux, brave-linux; required for them)")
	flag.StringVar(&cfg.wsl, "wsl", "", "the WSL distribution on -linux's Windows PC the Linux browsers run in (wsl -l), instead of a Linux machine of its own")
	flag.StringVar(&cfg.wslUser, "wsl-user", "", "the user in -wsl's distribution the browsers run as (default: the distribution's default user; Chrome refuses root)")
	flag.StringVar(&cfg.listen, "listen", "", "this Mac's IPv4 address on the iPhone's Wi-Fi network (safari-ios, chrome-ios, firefox-ios; required for them)")
	flag.StringVar(&cfg.iosDevice, "ios-device", "", "the iPhone's UDID (safari-ios, chrome-ios, firefox-ios; default: the paired iPhone connected to this Mac)")
	flag.StringVar(&cfg.chromeApp, "chrome-app", "", "a Chrome build to capture on every machine of the run instead of the installed Chrome, as that machine names it (Chrome for Testing, for a version no longer installable as the brand; docs/capturing.md)")
	flag.StringVar(&cfg.firefoxApp, "firefox-app", "", "a Firefox to capture on every machine of the run instead of the installed one, as that machine names it (an older release beside it: an application on macOS, firefox.exe on Windows)")
	flag.BoolVar(&cfg.keep, "keep", false, "leave the run's working directory on the Windows PC (the capture CA is removed regardless)")
	flag.StringVar(&cfg.staging, "staging", ".refresh", "directory for staged builds, captures, profiles and logs (a new subdirectory per run)")
	flag.StringVar(&cfg.scenarios, "scenarios", "", "capture these name:samples instead of the mode's set (with -capture-only, to try the tool)")
	flag.BoolVar(&cfg.yes, "yes", false, "do not ask before capturing")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "preflight and print the plan, then stop")
	flag.BoolVar(&cfg.captureOnly, "capture-only", false, "stop once the captures are staged and checked")
	flag.BoolVar(&cfg.deriveOnly, "derive-only", false, "stop once the profile is derived in staging (the generators run on it), before installing anything")
	flag.StringVar(&cfg.resume, "resume", "", "derive, install and verify from this staging run directory instead of capturing")
	flag.BoolVar(&cfg.serial, "serial", false, "capture the mini's browsers one after the other in quick mode too")
	flag.BoolVar(&cfg.verify, "verify", true, "run the profile's e2e tests after installing it")
	flag.BoolVar(&cfg.realcheck, "realcheck", true, "run cmd/realcheck for the browser after installing the profile")
	flag.Parse()

	if *browsers == "" && cfg.resume == "" {
		flag.Usage()
		os.Exit(2)
	}

	if cfg.mode != modeQuick && cfg.mode != modeFull {
		return fmt.Errorf("-mode %q: quick or full", cfg.mode)
	}

	if *browsers != "" {
		cfg.targets = strings.Split(*browsers, ",")

		var mini, windows, linux, iphone bool
		for _, t := range cfg.targets {
			tg, ok := targets[t]
			if !ok {
				return fmt.Errorf("-browser %q: one of %s", t, strings.Join(slices.Sorted(maps.Keys(targets)), ", "))
			}

			mini = mini || tg.remote == remoteMini
			windows = windows || tg.remote == remoteWindows
			linux = linux || tg.remote == remoteLinux
			iphone = iphone || tg.platform() == platformIOS
		}

		switch {
		case mini && cfg.mini == "":
			return errors.New("-browser chrome, firefox, edge, brave and safari-mini need -mini <your ssh alias for the second Mac>")
		case iphone && cfg.listen == "":
			return errors.New("-browser safari-ios, chrome-ios and firefox-ios need -listen <this Mac's IPv4 address on the iPhone's Wi-Fi network> (ipconfig getifaddr en0)")
		case iphone && !lanIPv4(cfg.listen):
			return fmt.Errorf("-listen %q: want this Mac's IPv4 address on the iPhone's network", cfg.listen)
		case windows && cfg.windows == "":
			return errors.New("-browser chrome-windows, firefox-windows, edge-windows and brave-windows need -windows <your ssh alias for the Windows PC>")
		case windows && mini && cfg.windows == cfg.mini:
			return fmt.Errorf("-windows and -mini are both %q: the Windows PC and the second Mac are two machines", cfg.mini)
		case linux && cfg.linux == "":
			return errors.New("-browser chrome-linux, firefox-linux and brave-linux need -linux <your ssh alias for the Linux machine, or the Windows PC with -wsl>")
		case linux && mini && cfg.linux == cfg.mini:
			return fmt.Errorf("-linux and -mini are both %q: the Linux machine and the second Mac are two machines", cfg.mini)
		case linux && cfg.wsl == "" && cfg.linux == cfg.windows:
			return fmt.Errorf("-linux and -windows are both %q: name its WSL distribution with -wsl", cfg.linux)
		case cfg.wslUser != "" && cfg.wsl == "":
			return fmt.Errorf("-wsl-user %q needs -wsl: on a Linux machine of its own the browsers run as -linux's ssh user", cfg.wslUser)
		case !wslName.MatchString(cfg.wsl) || !wslName.MatchString(cfg.wslUser):
			return fmt.Errorf("-wsl %q, -wsl-user %q: letters, digits, '.', '_' and '-' only", cfg.wsl, cfg.wslUser)
		}
	}

	if _, err := os.Stat("testdata/captures"); err != nil {
		return errors.New("run refresh in internal/harness/")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.resume != "" {
		r, err := loadRun(cfg.resume)
		if err != nil {
			return err
		}

		r.Keep = cfg.keep
		defer r.cleanWindows(ctx)
		defer r.cleanMacs(ctx)

		return r.finish(ctx, cfg)
	}

	dir, err := filepath.Abs(filepath.Join(cfg.staging, time.Now().Format("20060102-150405")))
	if err != nil {
		return err
	}

	r := &refreshRun{
		Dir:        dir,
		Mode:       cfg.mode,
		Mini:       cfg.mini,
		Stamp:      filepath.Base(dir),
		Serial:     cfg.serial,
		Scenarios:  cfg.scenarios,
		Keep:       cfg.keep,
		Listen:     cfg.listen,
		IOSDevice:  cfg.iosDevice,
		WSL:        cfg.wsl,
		WSLUser:    cfg.wslUser,
		ChromeApp:  cfg.chromeApp,
		FirefoxApp: cfg.firefoxApp,
	}
	// The Windows PCs' working directories go, and the capture CA with
	// them, however the run ends; so do the Macs' client identities.
	defer r.cleanWindows(ctx)
	defer r.cleanMacs(ctx)

	if err := r.preflight(ctx, cfg); err != nil {
		return err
	}

	fmt.Print(r.planText())

	if err := r.save(); err != nil {
		return err
	}

	if r.blocked() {
		return errors.New("preflight found problems; fix them and run again")
	}

	if cfg.dryRun {
		log.Printf("dry run: plan saved in %s", r.Dir)

		return nil
	}

	if !cfg.yes && !confirm(os.Stdin) {
		return errors.New("not confirmed")
	}

	if err := r.capture(ctx); err != nil {
		return err
	}

	if cfg.captureOnly {
		log.Printf("captures staged in %s; resume with -resume %s", r.Dir, r.Dir)

		return nil
	}

	return r.finish(ctx, cfg)
}

// cleanWindows runs machine.cleanWindows once on each Windows PC of the
// run, after an interruption too, logging to cleanup-<host>.log in staging.
func (r *refreshRun) cleanWindows(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)

	done := map[string]bool{}

	for _, p := range r.Plans {
		m := r.machine(p)
		if !m.windows || done[m.host] {
			continue
		}

		done[m.host] = true

		log.Printf("cleaning up on %s", m)

		if err := m.cleanWindows(ctx, filepath.Join(r.Dir, "cleanup-"+m.host+".log"), r.Keep); err != nil {
			dir := `$env:USERPROFILE\` + strings.ReplaceAll(m.dir, "/", `\`)
			log.Printf("cleaning up on %s failed (%v); there, in an administrator PowerShell:\n    cd %s; .\\capture.exe -cleanup -browser-data browser-data; cd ~; Remove-Item -Recurse -Force %s",
				m, err, dir, dir)
		}
	}
}

// cleanMacs runs capture -cleanup once on each Mac whose plans captured a
// client-certificate scenario, after an interruption too, logging to
// cleanup-<host>.log in staging: a capture killed with its ssh session
// (the second Mac) leaves a keychain, policy entries or a launchd job
// behind, which the next run's browsers would read.
func (r *refreshRun) cleanMacs(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)

	done := map[string]bool{}

	for _, p := range r.Plans {
		m := r.machine(p)
		if m.goos() != "darwin" || m.iphone || p.Started.IsZero() || !p.clientCerts() || done[m.host] {
			continue
		}

		done[m.host] = true

		log.Printf("removing client identities left on %s", m)

		if err := m.stream(ctx, "cleanup", filepath.Join(r.Dir, "cleanup-"+cmp.Or(m.host, "local")+".log"), m.bin("capture"), "-cleanup"); err != nil {
			log.Printf("removing client identities on %s failed (%v); there, in the working directory: ./capture -cleanup", m, err)
		}
	}
}

// wslName is what -wsl and -wsl-user may be (or empty): they go into the
// command line of the Windows PC's ssh shell unquoted (wslLine).
var wslName = regexp.MustCompile(`^[A-Za-z0-9._-]*$`)

// confirm asks on standard output and reads the answer from in.
func confirm(in *os.File) bool {
	fmt.Print("Capture now? [y/N] ")

	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return false
	}

	answer = strings.ToLower(strings.TrimSpace(answer))

	return answer == "y" || answer == "yes"
}

// lanIPv4 reports whether s is an IPv4 address another device on the
// network can reach: neither loopback nor unspecified.
func lanIPv4(s string) bool {
	a, err := netip.ParseAddr(s)

	return err == nil && a.Is4() && !a.IsLoopback() && !a.IsUnspecified()
}

// finish derives, installs, verifies and reports each staged profile, one
// at a time: they share profiles/data and go generate.
func (r *refreshRun) finish(ctx context.Context, cfg config) error {
	var failed []string
	for _, p := range r.Plans {
		if err := r.finishPlan(ctx, cfg, p); err != nil {
			log.Printf("%s: %v", p.Target, err)

			failed = append(failed, p.Target)
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("failed: %s (staged in %s; fix and run -resume %s)", strings.Join(failed, ", "), r.Dir, r.Dir)
	}

	return nil
}

func (r *refreshRun) finishPlan(ctx context.Context, cfg config, p *plan) error {
	rep := &report{
		plan: p,
		run:  r,
	}

	derived, err := r.derive(ctx, p, rep)
	if err != nil {
		return err
	}

	rep.derived = derived
	if !cfg.deriveOnly {
		if err := r.install(ctx, p, derived, rep); err != nil {
			return err
		}

		rep.installed = true
		if cfg.verify {
			rep.tests = r.runTests(ctx, p)
		}

		// realcheck drives the browsers on this Mac, not the iPhone's.
		if cfg.realcheck && p.Platform != platformIOS {
			rep.realcheck = r.runRealcheck(ctx, p)
		}
	}

	path, err := rep.write()
	if err != nil {
		return err
	}

	log.Printf("%s: report in %s", p.Target, path)

	if rep.tests != nil && rep.tests.err != nil {
		return fmt.Errorf("tests failed: %w", rep.tests.err)
	}

	return nil
}
