//go:build !windows && !linux

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/browser"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/clientcert"
)

// keychainPrefix names the temporary keychains that hold a run's client
// identity, by which -cleanup finds them.
const keychainPrefix = "mimicry-client-cert-"

// autoSelectKey is Chromium's policy naming the identities to send
// without asking.
const autoSelectKey = "AutoSelectCertificateForUrls"

// policyDomains are the defaults domains Chrome and Edge read policies
// from: Chrome's is its bundle identifier (Chrome for Testing's too), and
// Edge's com.microsoft.Edge.
var policyDomains = []string{
	"com.google.Chrome",
	"com.google.chrome.for.testing",
	"com.microsoft.Edge",
}

// clientCertRefusal says why b cannot take a client-certificate scenario
// here, "" when it can: Firefox needs NSS's certutil and pk12util for its
// profile's database.
func clientCertRefusal(b *browser.Browser) string {
	if b.Name != "firefox" {
		return ""
	}

	for _, tool := range []string{"certutil", "pk12util"} {
		if _, err := exec.LookPath(tool); err != nil {
			return "NSS's " + tool + " is not on PATH, with which Firefox's profile gets the client identity: brew install nss"
		}
	}

	return ""
}

// installClientCert gives Chrome or Edge the identity in the PKCS#12 file
// p12: a temporary keychain holding it joins the user's search list, its
// key open to the browser's team without asking, and the browser's
// AutoSelectCertificateForUrls gains entries for hosts. Firefox reads its
// profile's database instead (browser.Target.ClientCert).
func installClientCert(ctx context.Context, b *browser.Browser, p12 string, hosts []string) (func(context.Context) error, error) {
	if !capture.Chromium(b.Name) {
		return func(context.Context) error {
			return nil
		}, nil
	}

	team, err := teamID(ctx, b.App)
	if err != nil {
		return nil, err
	}

	domain, err := policyDomain(ctx, b)
	if err != nil {
		return nil, err
	}

	kc, err := newKeychain(ctx, p12, team)
	if err != nil {
		return nil, err
	}

	remove := func(ctx context.Context) error {
		return errors.Join(removeAutoSelect(ctx, domain), deleteKeychain(ctx, kc))
	}

	args := append([]string{"write", domain, autoSelectKey, "-array-add"}, plistStrings(clientcert.AutoSelectEntries(hosts))...)
	if err := runTool(ctx, "defaults", args...); err != nil {
		return nil, errors.Join(err, remove(context.WithoutCancel(ctx)))
	}

	return remove, nil
}

// newKeychain makes a keychain holding the identity in p12, unlocked
// without a timeout, its key usable by Apple's tools and team's apps, and
// adds it to the end of the user's search list. Its password is random and
// forgotten: nothing opens it again but the tool's clean-up, which deletes
// it.
func newKeychain(ctx context.Context, p12, team string) (string, error) {
	secret := make([]byte, 16)

	_, _ = rand.Read(secret)

	password := hex.EncodeToString(secret)

	path := filepath.Join(os.TempDir(), keychainPrefix+hex.EncodeToString(secret[:4])+".keychain-db")

	if err := runTool(ctx, "security", "create-keychain", "-p", password, path); err != nil {
		return "", err
	}

	list, err := searchList(ctx)
	if err != nil {
		return "", errors.Join(err, deleteKeychain(context.WithoutCancel(ctx), path))
	}

	for _, args := range [][]string{
		{"list-keychains", "-d", "user", "-s"},
		{"set-keychain-settings", path},
		{"unlock-keychain", "-p", password, path},
		{"import", p12, "-k", path, "-f", "pkcs12", "-P", clientcert.Password, "-A"},
		{"set-key-partition-list", "-S", "apple-tool:,apple:,teamid:" + team, "-s", "-k", password, path},
	} {
		if args[0] == "list-keychains" {
			args = append(append(args, list...), path)
		}

		if err := runTool(ctx, "security", args...); err != nil {
			return "", errors.Join(err, deleteKeychain(context.WithoutCancel(ctx), path))
		}
	}

	return path, nil
}

// deleteKeychain deletes a keychain, which also takes it out of the
// search list.
func deleteKeychain(ctx context.Context, path string) error {
	return runTool(ctx, "security", "delete-keychain", path)
}

// searchList is the user's keychain search list.
func searchList(ctx context.Context) ([]string, error) {
	out, err := inSession(ctx, "security", "list-keychains", "-d", "user")
	if err != nil {
		return nil, err
	}

	var list []string
	for line := range strings.Lines(string(out)) {
		if p := strings.Trim(strings.TrimSpace(line), `"`); p != "" {
			list = append(list, p)
		}
	}

	return list, nil
}

// teamIDPattern finds the team identifier in codesign's description of
// an app.
var teamIDPattern = regexp.MustCompile(`(?m)^TeamIdentifier=(\w+)$`)

// teamID is the code-signing team of app, whose apps a keychain's key
// partition list lets in.
func teamID(ctx context.Context, app string) (string, error) {
	out, err := exec.CommandContext(ctx, "codesign", "-dv", "--verbose=2", app).CombinedOutput() //nolint:gosec // a known browser location
	if err != nil {
		return "", fmt.Errorf("codesign -dv %s: %w: %s", app, err, strings.TrimSpace(string(out)))
	}

	m := teamIDPattern.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("no TeamIdentifier in codesign -dv %s", app)
	}

	return string(m[1]), nil
}

// policyDomain is the defaults domain b reads policies from: Edge's
// com.microsoft.Edge, Chrome's its bundle identifier.
func policyDomain(ctx context.Context, b *browser.Browser) (string, error) {
	if b.Name == "edge" {
		return "com.microsoft.Edge", nil
	}

	out, err := exec.CommandContext(ctx, "defaults", "read", filepath.Join(b.App, "Contents", "Info"), "CFBundleIdentifier").Output() //nolint:gosec // a known browser location
	if err != nil {
		return "", fmt.Errorf("reading %s's bundle identifier: %w", b.App, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// removeAutoSelect takes the run's entries (those naming the client CA)
// out of domain's AutoSelectCertificateForUrls, deleting it once empty;
// entries of the user's own stay.
func removeAutoSelect(ctx context.Context, domain string) error {
	plist, err := inSession(ctx, "defaults", "export", domain, "-")
	if err != nil {
		// No such domain: nothing to remove.
		return nil //nolint:nilerr // defaults fails for a domain never written
	}

	cmd := exec.CommandContext(ctx, "plutil", "-extract", autoSelectKey, "json", "-o", "-", "-")

	cmd.Stdin = strings.NewReader(string(plist))

	out, err := cmd.Output()
	if err != nil {
		return nil //nolint:nilerr // plutil fails for a missing key: nothing to remove
	}

	var entries []string
	if err := json.Unmarshal(out, &entries); err != nil {
		return fmt.Errorf("%s %s: %w", domain, autoSelectKey, err)
	}

	var keep []string
	for _, e := range entries {
		if !strings.Contains(e, clientcert.CACommonName) {
			keep = append(keep, e)
		}
	}

	switch {
	case len(keep) == len(entries):
		return nil
	case len(keep) == 0:
		return runTool(ctx, "defaults", "delete", domain, autoSelectKey)
	default:
		return runTool(ctx, "defaults", append([]string{"write", domain, autoSelectKey, "-array"}, plistStrings(keep)...)...)
	}
}

// plistStrings are values as defaults(1) takes strings: in XML, since it
// reads a bare value beginning with "{" as a dictionary.
func plistStrings(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		var b strings.Builder

		_ = xml.EscapeText(&b, []byte(v))
		out[i] = "<string>" + b.String() + "</string>"
	}

	return out
}

// cleanUpClientCerts removes what a killed run left of its client
// identities: the one-shot launchd jobs still loaded (inSession), the
// keychains (in the search list, or made before newKeychain added them
// to it), and the policy entries. One step failing does not stop the
// others.
func cleanUpClientCerts(ctx context.Context) ([]string, error) {
	var (
		done []string
		errs []error
	)

	jobs, jerr := bootOutJobs(ctx)

	done = append(done, jobs...)
	errs = append(errs, jerr)

	keychains := map[string]bool{}

	list, lerr := searchList(ctx)

	errs = append(errs, lerr)

	for _, kc := range list {
		if strings.HasPrefix(filepath.Base(kc), keychainPrefix) {
			keychains[kc] = true
		}
	}

	made, gerr := filepath.Glob(filepath.Join(os.TempDir(), keychainPrefix+"*.keychain-db"))

	errs = append(errs, gerr)

	for _, kc := range made {
		keychains[kc] = true
	}

	for _, kc := range slices.Sorted(maps.Keys(keychains)) {
		if err := deleteKeychain(ctx, kc); err != nil {
			// Not in the search list: the file alone is left.
			if rerr := os.Remove(kc); rerr != nil {
				errs = append(errs, err)

				continue
			}
		}

		done = append(done, "deleted the keychain "+kc)
	}

	for _, domain := range policyDomains {
		if err := removeAutoSelect(ctx, domain); err != nil {
			errs = append(errs, err)
		}
	}

	return done, errors.Join(errs...)
}

// sessionJobPrefix begins the labels of inSession's launchd jobs, and
// sessionDirPrefix the names of their temporary directories.
const (
	sessionJobPrefix = "org.mimicry.capture."
	sessionDirPrefix = "mimicry-session-"
)

// sessionJob finds the labels of inSession's jobs in launchctl print's
// description of the GUI domain.
var sessionJob = regexp.MustCompile(regexp.QuoteMeta(sessionJobPrefix) + `[\w.-]+`)

// bootOutJobs boots out of the user's GUI domain the one-shot jobs a
// killed run left loaded (inSession), and removes their directories.
func bootOutJobs(ctx context.Context) ([]string, error) {
	domain := "gui/" + strconv.Itoa(os.Getuid())

	var (
		done []string
		errs []error
	)

	out, err := exec.CommandContext(ctx, "launchctl", "print", domain).Output() //nolint:gosec // the user's own domain
	if err != nil {
		// No GUI session: nothing of ours can be loaded there.
		out = nil
	}

	for _, label := range slices.Compact(slices.Sorted(slices.Values(sessionJob.FindAllString(string(out), -1)))) {
		if b, err := exec.CommandContext(ctx, "launchctl", "bootout", domain+"/"+label).CombinedOutput(); err != nil { //nolint:gosec // our own job
			errs = append(errs, fmt.Errorf("launchctl bootout %s/%s: %w: %s", domain, label, err, strings.TrimSpace(string(b))))
			continue
		}

		done = append(done, "booted out the launchd job "+label)
	}

	dirs, gerr := filepath.Glob(filepath.Join(os.TempDir(), sessionDirPrefix+"*"))

	errs = append(errs, gerr)

	for _, d := range dirs {
		if err := os.RemoveAll(d); err != nil {
			errs = append(errs, err)
		}
	}

	return done, errors.Join(errs...)
}

// runTool runs a command in the user's GUI session (inSession).
func runTool(ctx context.Context, name string, args ...string) error {
	_, err := inSession(ctx, name, args...)

	return err
}

// inAqua reports whether the tool runs in the logged-in user's GUI (Aqua)
// session.
var inAqua = sync.OnceValue(func() bool {
	out, err := exec.Command("launchctl", "managername").Output()
	return err == nil && strings.TrimSpace(string(out)) == "Aqua"
})

// inSession runs name with args in the logged-in user's GUI (Aqua)
// session, where the browser runs, and returns its standard output; an
// error carries its standard error. From an ssh session (cmd/refresh on
// the second Mac) a one-shot launchd job runs it there: a keychain
// unlocked from ssh stays locked to the browser, which asks for its
// password on screen.
func inSession(ctx context.Context, name string, args ...string) ([]byte, error) {
	fail := func(err error, stderr []byte) error {
		return fmt.Errorf("%s %s: %w: %s", name, args[0], err, strings.TrimSpace(string(stderr)))
	}

	if inAqua() {
		var stderr bytes.Buffer

		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed tools; the arguments are the run's own

		cmd.Stderr = &stderr

		out, err := cmd.Output()
		if err != nil {
			return nil, fail(err, stderr.Bytes())
		}

		return out, nil
	}

	dir, err := os.MkdirTemp("", sessionDirPrefix)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	label := sessionJobPrefix + filepath.Base(dir)
	files := map[string]string{
		"OUT":    filepath.Join(dir, "out"),
		"ERR":    filepath.Join(dir, "err"),
		"STATUS": filepath.Join(dir, "status"),
	}

	var plist strings.Builder

	plist.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>` + label + `</string><key>RunAtLoad</key><true/><key>EnvironmentVariables</key><dict>`)

	for k, v := range files {
		plist.WriteString("<key>" + k + "</key>" + plistStrings([]string{v})[0])
	}

	plist.WriteString("</dict><key>ProgramArguments</key><array>")

	script := `"$@" >"$OUT" 2>"$ERR"; echo $? >"$STATUS.part" && mv "$STATUS.part" "$STATUS"`
	for _, a := range plistStrings(append([]string{"/bin/sh", "-c", script, "sh", name}, args...)) {
		plist.WriteString(a)
	}

	plist.WriteString("</array></dict></plist>\n")

	path := filepath.Join(dir, "job.plist")
	if err := os.WriteFile(path, []byte(plist.String()), 0o600); err != nil {
		return nil, err
	}

	domain := "gui/" + strconv.Itoa(os.Getuid())
	if out, err := exec.CommandContext(ctx, "launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil { //nolint:gosec // our own job
		return nil, fmt.Errorf("running %s in the GUI session (launchctl bootstrap %s): %w: %s; is the user logged in at the screen?", name, domain, err, strings.TrimSpace(string(out)))
	}

	defer func() {
		_ = exec.Command("launchctl", "bootout", domain+"/"+label).Run() //nolint:noctx,gosec // must run after the caller's context ends; our own job
	}()

	deadline := time.Now().Add(time.Minute)

	for {
		status, err := os.ReadFile(files["STATUS"])
		if err == nil {
			out, _ := os.ReadFile(files["OUT"])
			stderr, _ := os.ReadFile(files["ERR"])

			if code := strings.TrimSpace(string(status)); code != "0" {
				return nil, fail(fmt.Errorf("exit status %s", code), stderr)
			}

			return out, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s %s did not finish in the GUI session within a minute", name, args[0])
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
