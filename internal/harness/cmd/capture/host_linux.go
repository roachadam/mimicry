package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/clientcert"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// prepareHost readies the machine for a run and returns what undoes it.
// Each browser trusts the capture CA in its own profile for the run
// (browser.Target.CAFile); without a display, the browsers' windows go on
// Xvfb (browser.EnsureDisplay).
func prepareHost(ctx context.Context, _ *server.CA) (func(), error) {
	return browser.EnsureDisplay(ctx)
}

// cleanUp is -cleanup. A run here changes nothing outside its browsers'
// profiles, which go with them, but for the client-certificate policy
// files root wrote (clientcert.LinuxPolicyFiles), which it names for root
// to remove.
func cleanUp(string) error {
	var left []string

	for _, path := range clientcert.LinuxPolicyFiles {
		if _, err := os.Stat(path); err == nil {
			left = append(left, path)
		}
	}

	if len(left) > 0 {
		slices.Sort(left)

		return fmt.Errorf("a client-certificate policy file is left; as root:\n  rm %s", strings.Join(left, " "))
	}

	return nil
}

// startDNSStub does nothing: Chrome, Edge and Firefox map the capture hosts
// themselves, and Safari, which resolves through the system, is not here.
func startDNSStub(_ context.Context, _ netip.Addr, clients netip.Prefix) error {
	if clients.IsValid() {
		return errors.New("-listen serves an iPhone's DNS from a Mac; not on Linux")
	}

	return nil
}

// consoleUser is the user running the tool: the browsers open on its
// display ($DISPLAY, or the tool's own Xvfb), not a login screen's.
func consoleUser(context.Context) (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}

	return u.Username, nil
}

// versionNumber finds the version in a browser's --version output
// ("Google Chrome 157.0.8103.38", "Microsoft Edge 156.0.4314.58",
// "Mozilla Firefox 159.0.1").
var versionNumber = regexp.MustCompile(`\d+(\.\d+)+`)

// describeExecutable fills in what preflight reports of a browser found
// as an executable: its path, and its version from --version.
func describeExecutable(ctx context.Context, b *browser.Browser, pb *preflightBrowser) error {
	pb.App = b.Path

	out, err := exec.CommandContext(ctx, b.Path, "--version").Output() //nolint:gosec // a known browser location
	if err != nil {
		return fmt.Errorf("reading %s's version: %w", b.Path, err)
	}

	pb.Version = versionNumber.FindString(string(out))
	if pb.Version == "" {
		return fmt.Errorf("no version in %s --version: %q", b.Path, strings.TrimSpace(string(out)))
	}

	return nil
}

// preflightPlatform adds Linux's own checks to r: NSS's certutil, with
// which each browser trusts the capture CA; a display or Xvfb (always
// Xvfb in WSL, whose WSLg display browser.EnsureDisplay never uses); and
// the lowest port a user may bind.
func preflightPlatform(_ context.Context, r *preflightReport) error {
	r.Platform = "linux"

	if p, err := exec.LookPath("certutil"); err == nil {
		r.Certutil = p
	} else {
		r.Missing = append(r.Missing, "NSS's certutil, with which each browser trusts the capture CA: install libnss3-tools (Debian, Ubuntu) or nss-tools (Fedora)")
	}

	noDisplay := os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
	if _, err := exec.LookPath("Xvfb"); err != nil && noDisplay {
		r.Missing = append(r.Missing, "Xvfb, the display for the browsers' windows here (no $DISPLAY, or WSL): install xvfb (Debian, Ubuntu) or xorg-x11-server-Xvfb (Fedora)")
	}

	// 1024 where the file is missing: the kernel's default.
	r.UnprivilegedPortStart = 1024
	if data, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			r.UnprivilegedPortStart = n
		}
	}

	if len(r.Browsers) == 0 {
		r.Missing = append(r.Missing, "Google Chrome (google-chrome-stable, Google's .deb or .rpm), Microsoft Edge (microsoft-edge-stable, Microsoft's .deb or .rpm) or Firefox (Mozilla's build: packages.mozilla.org or the tarball, not a snap or a distribution's own build)")
	}

	return nil
}
