package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// stagedCaptures is where p's captures are staged.
func (r *refreshRun) stagedCaptures(p *plan) string {
	return filepath.Join(r.Dir, p.Target, "captures")
}

// capture runs every plan's captures on its machine, machines at once,
// and checks what they wrote.
func (r *refreshRun) capture(ctx context.Context) error {
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for _, steps := range r.schedule() {
		wg.Go(func() {
			for _, step := range steps {
				var swg sync.WaitGroup
				for _, p := range step {
					swg.Go(func() {
						if err := r.capturePlan(ctx, p); err != nil {
							mu.Lock()
							errs = append(errs, fmt.Errorf("%s: %w", p.Target, err))
							mu.Unlock()
						}
					})
				}
				swg.Wait()
				if ctx.Err() != nil {
					return
				}
			}
		})
	}
	wg.Wait()
	if err := r.save(); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// capturePlan runs p's captures: one capture tool run for the unforced
// scenarios, one with -h3-local-roots for the forced ones, then copies
// them into staging and checks them.
func (r *refreshRun) capturePlan(ctx context.Context, p *plan) error {
	m := r.machine(p)
	staged := r.stagedCaptures(p)
	if err := os.MkdirAll(staged, 0o750); err != nil {
		return err
	}
	logFile := filepath.Join(r.Dir, p.Target, "capture.log")
	out := m.path("out-" + p.Target)
	summary := m.path("summary-" + p.Target + ".jsonl")
	pages, err := filepath.Abs(pagesDir)
	if err != nil {
		return err
	}
	if p.Host != "" {
		pages = "pages"
		if slices.ContainsFunc(p.Captures, func(c planned) bool {
			return strings.HasPrefix(c.Scenario, "replay-")
		}) {
			if err := m.put(ctx, pagesDir); err != nil {
				return err
			}
		}
	}
	if p.Host == "" {
		// Captured straight into staging.
		out = staged
	}
	p.Started = time.Now()
	for _, forced := range []bool{false, true} {
		var entries []string
		for _, c := range p.Captures {
			if c.LocalRoots == forced {
				entries = append(entries, c.Scenario+":"+strconv.Itoa(c.Samples))
			}
		}
		if len(entries) == 0 {
			continue
		}
		args := []string{
			"-browsers", p.Browser,
			"-out", out,
			"-summary", summary,
			"-pages", pages,
			"-scenarios", strings.Join(entries, ","),
		}
		if forced {
			args = append(args, "-h3-local-roots")
		}
		log.Printf("%s: capturing on %s: %s", p.Target, m, strings.Join(entries, " "))
		err := m.stream(ctx, p.Target, logFile, m.bin("capture"), args...)
		if p.Host != "" {
			// Whatever was written comes back, even after a failure.
			if ferr := m.fetch(context.WithoutCancel(ctx), out, staged); ferr != nil {
				err = errors.Join(err, ferr)
			}
		}
		if err != nil {
			return err
		}
	}
	p.Finished = time.Now()
	data, err := m.output(ctx, "cat", summary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.Dir, p.Target, "summary.jsonl"), data, 0o600); err != nil {
		return err
	}
	if p.Measured, err = parseSummary(data); err != nil {
		return err
	}

	return r.checkCaptures(ctx, p)
}

func parseSummary(data []byte) ([]summaryLine, error) {
	var out []summaryLine
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var l summaryLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("capture summary: %w", err)
		}
		out = append(out, l)
	}

	return out, sc.Err()
}

// checkCaptures refuses a run whose captures do not all name p's profile
// with one user agent, that misses a planned scenario, or during which the
// browser was updated.
func (r *refreshRun) checkCaptures(ctx context.Context, p *plan) error {
	var problems []string
	agents := map[string][]string{}
	got := map[string]bool{}
	for _, l := range p.Measured {
		got[l.Scenario] = true
		agents[l.UserAgent] = append(agents[l.UserAgent], l.Scenario)
		if l.Name != p.Name {
			problems = append(problems, fmt.Sprintf("scenario %s was captured as %s, not %s", l.Scenario, l.Name, p.Name))
		}
	}
	if len(agents) > 1 {
		for ua, scs := range agents {
			problems = append(problems, fmt.Sprintf("user agent %q in %s", ua, strings.Join(scs, ", ")))
		}
		problems = append(problems, "the captures disagree on the user agent: the browser changed during the run")
	}
	for _, c := range p.Captures {
		label := c.label()
		if c.Scenario == "page" {
			label = ""
		}
		if !got[label] && !got[c.label()] {
			problems = append(problems, "no capture of "+c.label())
		}
	}
	rep, err := runPreflight(ctx, r.machine(p))
	if err != nil {
		return err
	}
	if v := rep.installedVersion(p.Browser); v != p.Installed {
		problems = append(problems, fmt.Sprintf("%s is %s on %s now, %s when the run started: it updated during the run; capture again", p.Browser, v, r.machine(p), p.Installed))
	}
	if len(problems) > 0 {
		return fmt.Errorf("captures in %s refused:\n  %s", r.stagedCaptures(p), strings.Join(problems, "\n  "))
	}
	log.Printf("%s: %d captures staged in %s, all from %s", p.Target, len(p.Measured), r.stagedCaptures(p), p.Installed)

	return nil
}
