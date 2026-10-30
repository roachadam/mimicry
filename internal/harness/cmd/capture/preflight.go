package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/dnsstub"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// preflightReport is what -preflight prints: what a capture on this Mac (or
// Windows PC) needs, checked the way a capture run checks it. cmd/refresh
// decodes it.
type preflightReport struct {
	// User runs the tool; ConsoleUser owns the login window's session
	// (/dev/console; on Windows the console session's user, "" when nobody
	// is logged on). Browsers opened over ssh show on that session, so
	// they must be the same user.
	User        string             `json:"user"`
	ConsoleUser string             `json:"consoleUser"`
	Browsers    []preflightBrowser `json:"browsers"`
	// CAPath is the capture CA's certificate (created if missing). Safari
	// needs CATrusted, and Chrome processes Alt-Svc only then. On Windows
	// a capture run trusts it for the run only, so it is normally false.
	CAPath    string `json:"caPath"`
	CATrusted bool   `json:"caTrusted"`
	// Resolver says whether /etc/resolver/test sends *.test to the DNS
	// stub (Safari needs it).
	Resolver bool `json:"resolver"`
	// Aioquic is why python3 with aioquic is unavailable (h3-v2 needs
	// it), empty when it is available.
	Aioquic string `json:"aioquic,omitempty"`
	// SafariProxy is how the system sends Safari to the capture proxy
	// (safariProxyConfig): "none", "pac" or "manual".
	SafariProxy string `json:"safariProxy"`
	// Platform, Admin, Certutil and Missing are set on Windows only:
	// Platform is "windows"; Admin says the tool runs elevated; Certutil is
	// certutil.exe's path ("" if absent); Missing lists, as instructions,
	// everything that keeps a capture from running here.
	Platform string   `json:"platform,omitempty"`
	Admin    bool     `json:"admin,omitempty"`
	Certutil string   `json:"certutil,omitempty"`
	Missing  []string `json:"missing,omitempty"`
}

// preflightBrowser is an installed browser.
type preflightBrowser struct {
	Name string `json:"name"`
	// App is the application bundle (Windows: the executable).
	App string `json:"app"`
	// Version is the bundle's CFBundleShortVersionString (Windows: the
	// executable's product version).
	Version string `json:"version"`
	// Unapproved is set when Gatekeeper has not yet approved the app's
	// first launch: its quarantine flags lack 0x40, so opening it shows a
	// dialog nobody at an ssh session can answer.
	Unapproved bool `json:"unapproved,omitempty"`
}

func printPreflight(caDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	r := preflightReport{
		Resolver: dnsstub.ResolverInstalled(),
	}

	u, err := user.Current()
	if err != nil {
		return err
	}

	r.User = u.Username
	if r.ConsoleUser, err = consoleUser(ctx); err != nil {
		return err
	}

	for _, b := range browser.Find() {
		pb := preflightBrowser{
			Name: b.Name,
			App:  b.App,
		}
		if b.App != "" {
			if pb.Version, err = bundleVersion(ctx, b.App); err != nil {
				return fmt.Errorf("%s: %w", b.Name, err)
			}

			pb.Unapproved = quarantined(ctx, b.App)
		} else if err := describeExecutable(ctx, b, &pb); err != nil {
			return fmt.Errorf("%s: %w", b.Name, err)
		}

		r.Browsers = append(r.Browsers, pb)
	}

	if caDir == "" {
		if caDir, err = server.DefaultCADir(); err != nil {
			return err
		}
	}

	ca, err := server.LoadOrCreateCA(caDir, server.CADomains, time.Now())
	if err != nil {
		return err
	}

	cert, err := ca.Issue(server.CAHosts, time.Now())
	if err != nil {
		return err
	}

	r.CAPath = ca.CertPath
	r.CATrusted = server.TrustedBySystem(cert, server.CaptureHost)

	if err := server.AioquicAvailable(ctx); err != nil {
		r.Aioquic = err.Error()
	}

	r.SafariProxy = [...]string{
		safariProxyNone:   "none",
		safariProxyPAC:    "pac",
		safariProxyManual: "manual",
	}[safariProxyConfig(ctx)]

	if err := preflightPlatform(ctx, &r); err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	return enc.Encode(r)
}

// bundleVersion reads an app's CFBundleShortVersionString.
func bundleVersion(ctx context.Context, app string) (string, error) {
	out, err := exec.CommandContext(ctx, "defaults", "read", app+"/Contents/Info", "CFBundleShortVersionString").Output() //nolint:gosec // a known browser location
	if err != nil {
		return "", fmt.Errorf("reading %s's version: %w", app, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// quarantined reports whether app carries a quarantine attribute whose
// flags lack 0x40, which Gatekeeper sets once a user approved the first
// launch.
func quarantined(ctx context.Context, app string) bool {
	out, err := exec.CommandContext(ctx, "xattr", "-p", "com.apple.quarantine", app).Output() //nolint:gosec // a known browser location
	if err != nil {
		return false
	}

	flags, _, _ := strings.Cut(strings.TrimSpace(string(out)), ";")
	v, err := strconv.ParseUint(flags, 16, 32)

	return err == nil && v&0x40 == 0
}
