package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/clientcert"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// stagedCaptures is where p's captures are staged.
func (r *refreshRun) stagedCaptures(p *plan) string {
	return filepath.Join(r.Dir, p.Target, "captures")
}

// capture runs every plan's captures on its machine, machines at once,
// and checks what they wrote.
func (r *refreshRun) capture(ctx context.Context) error {
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)

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

		args := m.captureArgs(
			"-browsers", p.tool(),
			"-out", out,
			"-summary", summary,
			"-pages", pages,
			"-scenarios", strings.Join(entries, ","),
		)
		if m.windows {
			// The browser's throwaway profiles in the working directory.
			args = append(args, "-browser-data", "browser-data")
		}

		if m.iphone {
			args = append(args, "-listen", r.Listen, "-ios-driver", string(browser.IOSDevicectl))
		}

		if forced {
			args = append(args, "-h3-local-roots")
		}

		log.Printf("%s: capturing on %s: %s", p.Target, m, strings.Join(entries, " "))

		if m.wsl != "" && p.needsPort443() {
			// Lost when the WSL VM stops, so set before each step; only
			// lowered, so a distribution letting users bind lower ports
			// keeps that.
			const lower = `[ "$(cat /proc/sys/net/ipv4/ip_unprivileged_port_start)" -le 443 ] || sysctl -qw net.ipv4.ip_unprivileged_port_start=443`
			if out, err := ssh(ctx, m.host, wslLine(m.wsl, "root", lower)).CombinedOutput(); err != nil {
				return fmt.Errorf("letting users bind 443 in %s: %w: %s", m, err, out)
			}
		}

		removePolicy := func() {}

		// Root-owned, so refresh writes it, only around the step.
		if m.wsl != "" && p.clientCertPolicy() != "" && slices.ContainsFunc(entries, clientCertEntry) {
			remove, perr := writeClientCertPolicy(ctx, m, p.clientCertPolicy())
			if perr != nil {
				return perr
			}

			removePolicy = remove
		}

		err := m.stream(ctx, p.Target, logFile, m.bin("capture"), args...)

		removePolicy()

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

	data, err := m.readFile(ctx, summary)
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

// clientCertEntry reports whether a -scenarios entry (name:samples) is a
// client-certificate scenario.
func clientCertEntry(entry string) bool {
	name, _, _ := strings.Cut(entry, ":")
	sc, err := server.ScenarioByName(name)

	return err == nil && sc.ClientCert() != ""
}

// writeClientCertPolicy writes, as root in m's WSL distribution, the policy
// file with which browser (chrome, edge) sends the client-certificate
// scenarios' identity unasked (clientcert.LinuxPolicy), creating the
// directories missing on its path, and returns what removes the file and
// those directories again, which logs a failure for removal by hand.
func writeClientCertPolicy(ctx context.Context, m *machine, browser string) (func(), error) {
	file := clientcert.LinuxPolicyFiles[browser]
	policy := base64.StdEncoding.EncodeToString(clientcert.LinuxPolicy(server.ClientCertHosts()))

	var dirs []string
	for d := filepath.Dir(file); d != "/" && d != "."; d = filepath.Dir(d) {
		dirs = append([]string{shellQuote(d)}, dirs...)
	}

	// Prints each directory it creates.
	write := "for d in " + strings.Join(dirs, " ") + `; do [ -d "$d" ] || { mkdir "$d" && echo "$d"; } || exit 1; done; ` +
		"printf %s " + policy + " | base64 -d > " + shellQuote(file) + " && chmod 644 " + shellQuote(file)

	out, err := ssh(ctx, m.host, wslLine(m.wsl, "root", write)).Output()
	if err != nil {
		return nil, fmt.Errorf("writing %s as root in %s: %w: %s", file, m, err, out)
	}

	steps := []string{
		"rm -f " + shellQuote(file),
	}
	for _, d := range slices.Backward(strings.Fields(string(out))) {
		steps = append(steps, "rmdir "+shellQuote(d))
	}

	remove := strings.Join(steps, "; ")

	return func() {
		if out, err := ssh(context.WithoutCancel(ctx), m.host, wslLine(m.wsl, "root", remove)).CombinedOutput(); err != nil {
			log.Printf("removing %s as root in %s: %v: %s; remove it by hand", file, m, err, out)
		}
	}, nil
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
// with one user agent, that misses a planned scenario, during which the
// browser was updated, or whose iPhone captures came from safaridriver's
// automation window where it does not stand in for the phone's own Safari.
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

		name := l.Scenario
		if name == "" {
			name = capture.DefaultScenario
		}

		if sc, err := server.ScenarioByName(name); l.Driver == string(browser.IOSSafaridriver) && err == nil && sc.AutomationDiffersOnIOS() != "" {
			problems = append(problems, fmt.Sprintf("scenario %s was captured through safaridriver: %s", l.Scenario, sc.AutomationDiffersOnIOS()))
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

	if v := rep.installedVersion(p.tool()); v != p.Installed {
		problems = append(problems, fmt.Sprintf("%s is %s on %s now, %s when the run started: it updated during the run; capture again", p.Browser, v, r.machine(p), p.Installed))
	}

	if len(problems) > 0 {
		return fmt.Errorf("captures in %s refused:\n  %s", r.stagedCaptures(p), strings.Join(problems, "\n  "))
	}

	log.Printf("%s: %d captures staged in %s, all from %s", p.Target, len(p.Measured), r.stagedCaptures(p), p.Installed)

	return nil
}
