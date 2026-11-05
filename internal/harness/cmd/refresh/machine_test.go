package main

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A WSL step's command line carries nothing the Windows host's shell
// (PowerShell or cmd) would read but one double-quoted word of base64 and
// fixed characters, and that word, run by bash, runs the step's own line:
// quotes, $, backslashes, newlines and non-ASCII text arrive as written.
func TestWSLLineRunsTheLineAsWritten(t *testing.T) {
	bash := ""

	for _, b := range []string{"bash", "/opt/homebrew/bin/bash", "/usr/local/bin/bash"} {
		p, err := exec.LookPath(b)
		if err != nil {
			continue
		}

		// bash 3.2 (macOS's) cannot source a process substitution; every
		// Linux distribution's bash can.
		v, err := exec.CommandContext(t.Context(), p, "-c", "echo ${BASH_VERSINFO[0]}").Output() //nolint:gosec // a bash found on PATH
		if major, _ := strconv.Atoi(strings.TrimSpace(string(v))); err == nil && major >= 4 {
			bash = p
			break
		}
	}

	if bash == "" {
		t.Skip("no bash 4 or later")
	}

	safe := regexp.MustCompile(`^wsl\.exe -d Ubuntu-24\.04( -u capture)? --cd ~ -- bash -c "(source <\(echo [A-Za-z0-9+/=]+ \| base64 -d\))"$`)

	line := "printf '%s|' \"double\" 'single' $'tab\\there' \"$((6*7))\" `echo back` 'ünïcødé'\necho 'second line' \\& done"
	want := "double|single|tab\there|42|back|ünïcødé|second line & done\n"

	for _, user := range []string{"capture", ""} {
		got := wslLine("Ubuntu-24.04", user, line)

		m := safe.FindStringSubmatch(got)
		if m == nil {
			t.Fatalf("user %q: %q is not one quoted base64 word after wsl.exe's own arguments", user, got)
		}

		out, err := exec.CommandContext(t.Context(), bash, "-c", m[2]).CombinedOutput() //nolint:gosec // the line under test, run locally
		if err != nil {
			t.Fatalf("user %q: running %q: %v: %s", user, m[2], err, out)
		}

		if string(out) != want {
			t.Errorf("user %q: ran as %q, want %q", user, out, want)
		}

		if user == "" && strings.Contains(got, " -u ") {
			t.Errorf("no user: %q names one", got)
		}
	}
}
