package main

import (
	"context"
	"crypto/sha1" //nolint:gosec // a certificate store names a certificate by its SHA-1 thumbprint
	"errors"
	"fmt"
	"log"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// On Windows a capture runs over ssh as an administrator: it trusts the
// capture CA in the machine's Root store for the run (Chrome trusts the
// machine's roots, so it also takes HTTP/3 through Alt-Svc) and opens Chrome
// on the console user's desktop through scheduled tasks. Chrome maps the
// capture hosts with --host-resolver-rules, so DNS is never touched.

// windowsHost is what a capture needs of this PC.
type windowsHost struct {
	// user runs the tool, console is logged on at the console ("" for
	// nobody).
	user, console string
	// admin: the tool runs elevated.
	admin bool
	// certutil and schtasks are the tools' paths, "" when absent.
	certutil, schtasks string
	// chrome is chrome.exe's path, "" when Chrome is not installed.
	chrome string
}

// checkHost looks at this PC.
func checkHost(ctx context.Context) (windowsHost, error) {
	var h windowsHost

	u, err := user.Current()
	if err != nil {
		return h, err
	}

	h.user = u.Username
	if h.console, err = browser.ConsoleUser(ctx); err != nil {
		return h, fmt.Errorf("finding the console user: %w", err)
	}

	out, err := browser.PowerShell(ctx, `([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)`)
	if err != nil {
		return h, fmt.Errorf("checking for administrator rights: %w", err)
	}

	h.admin = out == "True"
	h.certutil, _ = exec.LookPath("certutil.exe")
	h.schtasks, _ = exec.LookPath("schtasks.exe")

	if found := browser.Find(); len(found) > 0 {
		h.chrome = found[0].Path
	}

	return h, nil
}

// problems lists, as instructions, what keeps a capture from running here.
func (h windowsHost) problems() []string {
	var p []string
	if !h.admin {
		p = append(p, "the capture tool does not run elevated as an administrator: each run trusts the capture CA in the machine's Root store (certutil -addstore Root) and removes it after; connect over ssh as an administrator (OpenSSH gives administrators an elevated session)")
	}

	switch {
	case h.console == "":
		p = append(p, browser.ErrNoConsoleUser.Error())
	case !strings.EqualFold(h.console, h.user):
		p = append(p, fmt.Sprintf("the console session belongs to %s, not %s: Chrome opens on the console user's desktop as that user and must share the run's directory with this tool; log on at the screen as %s", h.console, h.user, h.user))
	}

	if h.certutil == "" {
		p = append(p, "certutil.exe is not on PATH (Windows ships it in System32): it trusts and removes the capture CA")
	}

	if h.schtasks == "" {
		p = append(p, "schtasks.exe is not on PATH (Windows ships it in System32): it opens Chrome on the console user's desktop")
	}

	if h.chrome == "" {
		p = append(p, "Chrome is not installed: looked for "+strings.Join(browser.ChromeLocations(), ", "))
	}

	return p
}

// prepareHost fails fast if this PC cannot capture, then trusts the capture
// CA in the machine's Root store. The returned function removes it; the run
// calls it however it ends, short of being killed (then -cleanup).
func prepareHost(ctx context.Context, ca *server.CA) (func(), error) {
	h, err := checkHost(ctx)
	if err != nil {
		return nil, err
	}

	if p := h.problems(); len(p) > 0 {
		return nil, fmt.Errorf("this PC cannot capture yet:\n  - %s", strings.Join(p, "\n  - "))
	}

	path, err := filepath.Abs(ca.CertPath)
	if err != nil {
		return nil, err
	}

	thumb := fmt.Sprintf("%X", sha1.Sum(ca.Cert.Raw)) //nolint:gosec // the store's name for the certificate

	if out, err := exec.CommandContext(ctx, h.certutil, "-addstore", "Root", path).CombinedOutput(); err != nil { //nolint:gosec // the CA is ours
		return nil, fmt.Errorf("trusting the capture CA: certutil -addstore Root %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}

	log.Printf("trusted the capture CA %s (SHA-1 %s) in the machine's Root store for this run; it is removed when the run ends", path, thumb)

	return func() {
		if err := untrust(context.Background(), h.certutil, thumb); err != nil {
			log.Printf("%v; remove it as an administrator:\n  certutil -delstore Root %s\n(or run capture -cleanup)", err, thumb)

			return
		}

		log.Printf("removed the capture CA (SHA-1 %s) from the machine's Root store", thumb)
	}, nil
}

// untrust removes the certificate with SHA-1 thumbprint thumb from the
// machine's Root store.
func untrust(ctx context.Context, certutil, thumb string) error {
	if out, err := exec.CommandContext(ctx, certutil, "-delstore", "Root", thumb).CombinedOutput(); err != nil { //nolint:gosec // the CA is ours
		return fmt.Errorf("removing the capture CA (SHA-1 %s) from the machine's Root store: certutil -delstore Root %s: %w: %s", thumb, thumb, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// cleanUp is -cleanup: it removes what a killed run left behind: Chrome
// processes on profiles under dataDir, the tool's scheduled tasks, and every
// capture CA (found by its subject, whichever directory it came from) in the
// machine's Root store. It does nothing, successfully, when nothing is left.
func cleanUp(dataDir string) error {
	ctx := context.Background()

	done, err := browser.Cleanup(ctx, dataDir)
	for _, d := range done {
		log.Print(d)
	}

	certutil, lookErr := exec.LookPath("certutil.exe")
	if lookErr != nil {
		return errors.Join(err, lookErr)
	}

	out, psErr := browser.PowerShell(ctx, `Get-ChildItem Cert:\LocalMachine\Root | Where-Object {
	$_.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false) -eq `+browser.PSQuote(server.CACommonName)+`
} | ForEach-Object { $_.Thumbprint }`)
	if psErr != nil {
		return errors.Join(err, fmt.Errorf("listing capture CAs in the machine's Root store: %w", psErr))
	}

	for thumb := range strings.FieldsSeq(out) {
		if uerr := untrust(ctx, certutil, thumb); uerr != nil {
			err = errors.Join(err, uerr)
			continue
		}

		log.Printf("removed a capture CA (SHA-1 %s) from the machine's Root store", thumb)
	}

	return err
}

// startDNSStub does nothing on Windows: only Safari needs the stub, and
// Chrome maps the capture hosts itself.
func startDNSStub(context.Context) {}

// consoleUser is the user logged on at the console, "" when nobody is.
func consoleUser(ctx context.Context) (string, error) {
	return browser.ConsoleUser(ctx)
}

// describeExecutable reports Chrome's executable and its product version.
func describeExecutable(ctx context.Context, b *browser.Browser, pb *preflightBrowser) error {
	pb.App = b.Path

	var err error

	pb.Version, err = b.Version(ctx)

	return err
}

// preflightPlatform adds what a Windows capture needs: elevation, a console
// user who runs the tool, certutil, schtasks and Chrome.
func preflightPlatform(ctx context.Context, r *preflightReport) error {
	h, err := checkHost(ctx)
	if err != nil {
		return err
	}

	r.Platform = "windows"
	r.Admin = h.admin
	r.Certutil = h.certutil
	r.Missing = h.problems()

	return nil
}
