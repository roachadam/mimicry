package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// result is how a verification step went.
type result struct {
	command  string
	duration time.Duration
	err      error
	// summary is what the report shows: the failures' tail, or
	// realcheck's comparison.
	summary string
	// output is where its whole output is.
	output string
}

// runTests runs the tests that check p's profile against its captures
// (MIMICRY_PROFILES selects it): the e2e suite and the generators'
// regenerate tests here, the profiles package in the library, under
// caffeinate so a laptop does not sleep through them.
func (r *refreshRun) runTests(ctx context.Context, p *plan) *result {
	logFile := filepath.Join(r.Dir, p.Target, "tests.log")
	res := &result{
		command: "MIMICRY_PROFILES=" + p.Name + " go test -count=1 ./e2e/ ./cmd/hellogen/ ./cmd/h3gen/ ./cmd/kindsgen/ (harness), go test -count=1 ./profiles/ ./cmd/profilegen/ (library)",
		output:  logFile,
	}
	start := time.Now()
	log.Printf("%s: running the tests for %s (several minutes)", p.Target, p.Name)
	var errs []string
	for _, step := range []struct {
		dir  string
		args []string
	}{
		{".", []string{"test", "-count=1", "-timeout=30m", "./e2e/", "./cmd/hellogen/", "./cmd/h3gen/", "./cmd/kindsgen/"}},
		{"..", []string{"test", "-count=1", "./profiles/", "./cmd/profilegen/"}},
	} {
		name, args := "go", step.args
		if _, err := exec.LookPath("caffeinate"); err == nil {
			name, args = "caffeinate", append([]string{"-i", "go"}, args...)
		}
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the tool's own commands
		cmd.Dir = step.dir
		cmd.Env = append(os.Environ(), "MIMICRY_PROFILES="+p.Name)
		out, err := cmd.CombinedOutput()
		appendFile(logFile, fmt.Sprintf("$ (in %s) %s %s\n%s\n", step.dir, name, strings.Join(args, " "), out))
		if err != nil {
			pkgs := slices.DeleteFunc(slices.Clone(step.args[1:]), func(a string) bool {
				return strings.HasPrefix(a, "-")
			})
			errs = append(errs, fmt.Sprintf("%s: %v", strings.Join(pkgs, " "), err))
			res.summary += failures(string(out))
		}
	}
	res.duration = time.Since(start)
	if len(errs) > 0 {
		res.err = fmt.Errorf("%s", strings.Join(errs, "; "))
		log.Printf("%s: tests failed; see %s", p.Target, logFile)
	} else {
		log.Printf("%s: tests passed in %s", p.Target, res.duration.Round(time.Second))
	}
	return res
}

// failures keeps a go test output's FAIL lines and the lines before each.
func failures(out string) string {
	lines := strings.Split(out, "\n")
	var keep []string
	for i, l := range lines {
		if strings.Contains(l, "--- FAIL") || strings.HasPrefix(l, "FAIL") {
			keep = append(keep, lines[max(0, i-6):i+1]...)
		}
	}
	if len(keep) > 200 {
		keep = keep[:200]
	}
	return strings.Join(keep, "\n") + "\n"
}

func appendFile(path, s string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(s)
	_ = f.Close()
}

// ipv4 matches what looks like an IPv4 address, kept out of the report:
// the echo services see the machine's public address.
var ipv4 = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

// runRealcheck runs cmd/realcheck where p's browser runs: the browser and
// the new profile against the echo endpoints and the protected sites. It
// takes over that machine's screen for a few minutes. Built after install,
// so the profile is built in.
func (r *refreshRun) runRealcheck(ctx context.Context, p *plan) *result {
	m := r.machine(p)
	local := filepath.Join(r.Dir, p.Target, "realcheck")
	out := local
	if m.host != "" {
		out = "realcheck-" + p.Target
	}
	res := &result{
		command: fmt.Sprintf("realcheck -browsers %s -profiles %s (on %s)", p.Browser, p.Name, m),
		output:  filepath.Join(local, "compare.md"),
	}
	start := time.Now()
	defer func() {
		res.duration = time.Since(start)
	}()
	bin, err := build(ctx, r.Dir, "realcheck")
	if err != nil {
		res.err = err
		return res
	}
	if err := m.put(ctx, bin); err != nil {
		res.err = err
		return res
	}
	log.Printf("%s: realcheck on %s (takes over its screen for a few minutes)", p.Target, m)
	runErr := m.stream(ctx, p.Target+" realcheck", filepath.Join(r.Dir, p.Target, "realcheck.log"), m.bin("realcheck"),
		"-browsers", p.Browser, "-profiles", p.Name, "-out", out)
	if err := m.fetch(context.WithoutCancel(ctx), out, local); err != nil {
		runErr = errors.Join(runErr, err)
	}
	res.err = runErr
	data, err := os.ReadFile(filepath.Join(local, "compare.md"))
	if err != nil {
		res.err = errors.Join(res.err, err)
		return res
	}
	// The echo comparison only: the sites' table is in compare.md.
	summary, _, _ := strings.Cut(string(data), "# Protected sites")
	res.summary = ipv4.ReplaceAllString(summary, "<ip>")
	return res
}
