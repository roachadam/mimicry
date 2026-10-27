// Command refresh brings a browser's built-in profile up to the version
// installed where that browser is captured:
//
//	go run ./cmd/refresh -browser chrome -mode quick           # in harness/
//	go run ./cmd/refresh -browser chrome,firefox -mode quick   # both at once on the Mac mini
//	go run ./cmd/refresh -browser safari -mode full -dry-run   # the plan only
//
// Browsers (-browser) and where they run: chrome and firefox on the Mac mini
// (-mini, an ssh host), safari on this Mac, safari-mini (the mini's Safari,
// a macOS version behind) on the mini. Per browser it:
//
//  1. Preflight: builds cmd/capture for darwin/arm64 and runs its
//     -preflight where the browser runs (copied there over ssh): the
//     installed version, which names the profile (a new major is a new
//     profile copied from the browser's newest; the same major refreshes it
//     in place), the logged-in console user, the capture CA's trust,
//     /etc/resolver/test, aioquic, Gatekeeper's first-launch approval. It
//     prints the plan, the estimated time and any manual step, and asks
//     before going on (-yes does not ask; -dry-run stops there).
//  2. Capture: -mode quick takes server.QuickScenarios (the fingerprint
//     layer: three samples where the browser picks per connection, one
//     elsewhere), -mode full every scenario and replay, three samples each.
//     Chrome and Firefox on the mini run at once in quick mode (their own
//     processes, ports and windows; no quick scenario clicks or needs focus),
//     one after the other in full mode (the click scenario moves the mouse).
//     Everything lands in a staging directory (-staging), never in
//     testdata/captures: every capture of a run must name the same browser
//     version, and the installed version must not change during the run.
//  3. Derive: the previous profile is copied, renamed, and given the new
//     capture's ClientHello, HTTP/2 preface, navigation headers and HTTP/1.1
//     navigation headers (slots such as Cookie keep their places); every
//     scenario not recaptured is inherited through captures_from from the
//     profile holding its captures. hellogen, h3gen and kindsgen then run on
//     the staged profile over the committed captures with the staged ones on
//     top; a refusal stops the refresh with the generator's message.
//  4. Install: the staged captures go to testdata/captures (in place: the
//     recaptured scenarios' old files go first), the profile to
//     profiles/data; beyond five supported versions per browser the oldest
//     is marked deprecated; go generate ./profiles.
//  5. Verify: the e2e and regenerate tests for the profile
//     (MIMICRY_PROFILES), and cmd/realcheck for the browser where it runs.
//     The report, harness/reports/refresh-<name>-<date>.md, holds the plan,
//     each scenario's measured capture time, captures_from, the profile's
//     differences from the previous one (version tokens normalised;
//     generated, captured and hand-written sections apart) and the results.
//
// -resume DIR derives, installs and verifies from a staging directory a
// failed run left (after pinning a gap a generator refused, say);
// -capture-only stops once the captures are staged, -derive-only once the
// derived profile is in staging, before anything in the repository changes.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
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
	browsers := flag.String("browser", "", "comma-separated browsers to refresh: chrome, firefox (on -mini), safari (this Mac), safari-mini (the mini's Safari)")
	flag.StringVar(&cfg.mode, "mode", modeQuick, "quick (the fingerprint layer; the rest inherited from the previous version) or full (every scenario and replay)")
	flag.StringVar(&cfg.mini, "mini", "mac-mini", "ssh host of the Mac mini")
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
		for _, t := range cfg.targets {
			if _, ok := targets[t]; !ok {
				return fmt.Errorf("-browser %q: one of %s", t, strings.Join(slices.Sorted(maps.Keys(targets)), ", "))
			}
		}
	}
	if _, err := os.Stat("testdata/captures"); err != nil {
		return errors.New("run refresh in harness/")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.resume != "" {
		r, err := loadRun(cfg.resume)
		if err != nil {
			return err
		}
		return r.finish(ctx, cfg)
	}

	dir, err := filepath.Abs(filepath.Join(cfg.staging, time.Now().Format("20060102-150405")))
	if err != nil {
		return err
	}
	r := &refreshRun{
		Dir:    dir,
		Mode:   cfg.mode,
		Mini:   cfg.mini,
		Stamp:  filepath.Base(dir),
		Serial: cfg.serial,
	}
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
		if cfg.realcheck {
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
