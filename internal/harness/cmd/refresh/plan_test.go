package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// A new profile inherits a scenario only from a profile holding captures
// of it: Chrome 156 captured proxy-socks4 but, having no SOCKS4A, never
// proxy-socks4a, and its proxy-socks5 captures are Chrome 155's.
func TestInheritOnlyCapturedScenarios(t *testing.T) {
	t.Chdir("../..") // capturesDir is relative to internal/harness

	prev := profiles.Chrome156MacOS().Spec()
	got := inherit(&plan{
		Name:     "chrome-157-macos",
		Previous: prev.Name,
		Platform: prev.Platform,
	}, map[string]*profile.Spec{
		prev.Name: prev,
	})

	for scenario, want := range map[string]string{
		"proxy-socks4":  "chrome-156-macos",
		"proxy-socks4a": "",
		"proxy-socks5":  "chrome-155-macos",
	} {
		if got[scenario] != want {
			t.Errorf("capturesFrom[%q] = %q, want %q", scenario, got[scenario], want)
		}
	}
}

// A new profile starts from the browser's newest one on its platform; the
// first there from the browser's macOS one, and the first of a Chromium
// browser (Edge) from Chrome's on its platform, else on macOS, capturing
// everything and inheriting nothing (NewPlatform). A browser older than
// its newest profile, or one with nothing to start from, is a problem.
func TestChoosePrevious(t *testing.T) {
	spec := func(name string) *profile.Spec {
		parts := strings.Split(name, "-")

		return &profile.Spec{
			Name:     name,
			Browser:  parts[0],
			Version:  parts[1],
			Platform: parts[2],
		}
	}

	builtins := func(names ...string) map[string]*profile.Spec {
		out := map[string]*profile.Spec{}
		for _, n := range names {
			out[n] = spec(n)
		}

		return out
	}

	chrome := builtins("chrome-155-macos", "chrome-156-macos", "chrome-156-windows", "chrome-157-linux", "firefox-159-macos")

	for _, c := range []struct {
		name        string
		builtins    map[string]*profile.Spec
		browser     string
		platform    string
		version     int
		previous    string
		newPlatform bool
		problem     string
	}{
		{
			name:     "next version",
			builtins: chrome,
			browser:  "chrome",
			platform: platformMacOS,
			version:  157,
			previous: "chrome-156-macos",
		},
		{
			name:     "older than the newest",
			builtins: chrome,
			browser:  "chrome",
			platform: platformWindows,
			version:  155,
			previous: "chrome-156-windows",
			problem:  "older than the newest profile",
		},
		{
			name:        "first on a platform, from macOS",
			builtins:    chrome,
			browser:     "firefox",
			platform:    platformWindows,
			version:     159,
			previous:    "firefox-159-macos",
			newPlatform: true,
		},
		{
			name:        "first edge on macOS, from chrome there",
			builtins:    chrome,
			browser:     "edge",
			platform:    platformMacOS,
			version:     156,
			previous:    "chrome-156-macos",
			newPlatform: true,
		},
		{
			name:        "first edge on windows, from chrome there",
			builtins:    chrome,
			browser:     "edge",
			platform:    platformWindows,
			version:     157,
			previous:    "chrome-156-windows",
			newPlatform: true,
		},
		{
			name:        "first edge on linux, from chrome there",
			builtins:    chrome,
			browser:     "edge",
			platform:    platformLinux,
			version:     157,
			previous:    "chrome-157-linux",
			newPlatform: true,
		},
		{
			name:        "first edge with no chrome on its platform, from chrome on macOS",
			builtins:    builtins("chrome-155-macos", "chrome-156-macos"),
			browser:     "edge",
			platform:    platformWindows,
			version:     156,
			previous:    "chrome-156-macos",
			newPlatform: true,
		},
		{
			name:        "edge on a new platform, from edge on macOS",
			builtins:    builtins("chrome-156-windows", "edge-156-macos"),
			browser:     "edge",
			platform:    platformWindows,
			version:     156,
			previous:    "edge-156-macos",
			newPlatform: true,
		},
		{
			name:     "edge's next version",
			builtins: builtins("chrome-157-macos", "edge-156-macos"),
			browser:  "edge",
			platform: platformMacOS,
			version:  157,
			previous: "edge-156-macos",
		},
		{
			name:     "first edge with no chrome",
			builtins: builtins("firefox-159-macos"),
			browser:  "edge",
			platform: platformMacOS,
			version:  156,
			problem:  "no built-in edge profile",
		},
		{
			name:     "first firefox never starts from chrome",
			builtins: builtins("chrome-156-macos"),
			browser:  "firefox",
			platform: platformMacOS,
			version:  159,
			problem:  "no built-in firefox profile",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &plan{
				Browser:   c.browser,
				Platform:  c.platform,
				Installed: strconv.Itoa(c.version) + ".0.1.2",
				Version:   strconv.Itoa(c.version),
				Name:      c.browser + "-" + strconv.Itoa(c.version) + "-" + c.platform,
			}

			p.choosePrevious(c.builtins, c.version, &machine{})

			if p.Previous != c.previous || p.NewPlatform != c.newPlatform {
				t.Errorf("previous %q, new platform %v; want %q, %v", p.Previous, p.NewPlatform, c.previous, c.newPlatform)
			}

			problems := strings.Join(p.Problems, "\n")
			if (c.problem == "") != (problems == "") || !strings.Contains(problems, c.problem) {
				t.Errorf("problems %q; want one with %q", problems, c.problem)
			}
		})
	}
}
