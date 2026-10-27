// Command refresh brings a browser's built-in profile up to the version
// installed where that browser is captured (chrome, firefox and safari-mini
// on the Mac mini via -mini, safari here):
//
//	go run ./cmd/refresh -browser chrome,firefox -mode quick   # in harness/
//
// Per browser it preflights, captures into a staging directory, derives the
// new profile and runs the generators, installs it, and verifies it with the
// e2e tests and cmd/realcheck, writing harness/reports/refresh-<name>-<date>.md.
// -dry-run stops after the plan; -resume DIR continues a failed run;
// -capture-only and -derive-only stop before the repository changes.
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
