package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Target is the capture server browsers connect to.
type Target struct {
	// Host is the capture hostname, under .test. Chrome and Firefox resolve
	// it to 127.0.0.1 through their own settings; Safari, which cannot be
	// configured that way, through the system and the capture tool's DNS
	// stub (internal/dnsstub). Either way it is a hostname: browsers omit
	// server_name for IP addresses.
	Host string
	Port int
	// Hosts are the other hostnames a replayed page reaches, all served
	// on Port.
	Hosts []string
	// HostCerts says the server presents each host a certificate of its
	// own from the capture CA (pages across hosts, even a page of one
	// host), so Firefox trusts the system's roots, where the CA is
	// trusted, rather than an exception for CertSHA256.
	HostCerts bool
	// CertSHA256 is the SHA-256 of the server certificate (DER), which Firefox
	// is told to accept.
	CertSHA256 [32]byte
	// Proxy, when set, is the proxy the browser goes through
	// ("http://127.0.0.1:port" or "socks5://127.0.0.1:port").
	Proxy *url.URL
	// Plain sends the browser to http:// instead of https://.
	Plain bool
	// Path is the start URL's path; "" for "/".
	Path string
	// ProxyAuth, when set, are the credentials the browser answers its
	// proxy's authentication challenges with: Chrome over the DevTools
	// protocol, Firefox over WebDriver BiDi, as a user typing them into the
	// login dialog.
	ProxyAuth *url.Userinfo
	// H3LocalRoots lets Chrome and Firefox use QUIC with a certificate
	// that does not chain to a root built into the browser, as the capture
	// CA's: both otherwise abandon the QUIC handshake and stay on TCP. It
	// is a capture condition (see Conditions).
	H3LocalRoots bool
	// debugPort is Firefox's remote debugging port when ProxyAuth is set.
	debugPort int
}

// h3UnusedPort is the port of the origins Chrome is told to force QUIC on
// (H3LocalRoots): none the page visits, so nothing is forced, but their
// hosts are allowed a root that is not built in.
const h3UnusedPort = 1

// Conditions describes what b is set to differently from its defaults for
// t, beyond resolving the capture hosts and accepting the capture
// certificate, each with why; a capture records them.
func (b *Browser) Conditions(t Target) []string {
	if !t.H3LocalRoots {
		return nil
	}
	switch b.Name {
	case "chrome":
		return []string{
			"--origin-to-force-quic-on=" + chromeQUICOrigins(t) + ": Chrome refuses a QUIC certificate not issued by a known root (ERR_QUIC_CERT_ROOT_NOT_KNOWN, net/quic/crypto/proof_verifier_chromium.cc) unless its host is among these origins' hosts; it forces QUIC only on these origins, whose port the page never uses, so HTTP/3 is still discovered through Alt-Svc",
		}
	case "firefox":
		return []string{
			`network.http.http3.disable_when_third_party_roots_found=false: Firefox abandons an HTTP/3 connection whose certificate chain ends at a root that is not built in (Http3Session::Authenticated), as the capture CA's, trusted through security.enterprise_roots.enabled`,
		}
	}
	return nil
}

// chromeQUICOrigins are the origins of --origin-to-force-quic-on for t:
// every capture host on h3UnusedPort.
func chromeQUICOrigins(t Target) string {
	hosts := append([]string{t.Host}, t.Hosts...)
	origins := make([]string, len(hosts))
	for i, h := range hosts {
		origins[i] = h + ":" + strconv.Itoa(h3UnusedPort)
	}
	return strings.Join(origins, ",")
}

func (t Target) url(host string) string {
	scheme := "https://"
	if t.Plain {
		scheme = "http://"
	}
	path := t.Path
	if path == "" {
		path = "/"
	}
	return scheme + host + ":" + strconv.Itoa(t.Port) + path
}

// Browser is an installed browser that can be launched against a Target.
type Browser struct {
	// Name matches capture.ParseUserAgent's browser names.
	Name string
	// Path is the executable, used where there is no App.
	Path string
	// App is the macOS application bundle. When set, the browser is launched
	// through open(1), so it runs as its own application, as when a user
	// starts it. Started as a child process instead, it inherits the caller's
	// macOS privacy restrictions, and Firefox exits because it cannot read its
	// own Application Support folder.
	App string
	// Shared marks a browser that cannot run a separate instance or profile:
	// it opens a tab in the user's own window. Only Safari.
	Shared bool
	// prepare writes any profile files into dir and returns the arguments.
	prepare func(dir string, t Target, headless bool) ([]string, error)
	// warmup, when set, starts the browser on about:blank and hands it the
	// capture URL this much later through a second invocation of binary on
	// the same profile, which the running instance takes over.
	warmup time.Duration
	binary string
}

// chromeWarmup is how long a fresh Chrome profile settles before the capture
// page. Within its first seconds it rebuilds its certificate verifiers (with
// a new cookie store and server properties cache) and marks every open
// connection as going away (net log: CERT_VERIFY_PROC_CREATED, then
// QUIC_SESSION_POOL_MARK_ALL_ACTIVE_SESSIONS_GOING_AWAY), so a capture page
// loaded at once moves to a second connection partway through. A
// long-running Chrome never shows a server this; --disable-component-update
// does not prevent it.
const chromeWarmup = 5 * time.Second

var macOSApps = map[string]string{
	"chrome":  "/Applications/Google Chrome.app",
	"firefox": "/Applications/Firefox.app",
	"safari":  "/Applications/Safari.app",
}

var linuxCommands = map[string][]string{
	"chrome": {
		"google-chrome-stable",
		"google-chrome",
	},
	"firefox": {
		"firefox",
	},
}

// Find returns the supported browsers installed here: Chrome, Firefox, then
// Safari.
func Find() []*Browser {
	var found []*Browser
	for _, name := range []string{"chrome", "firefox", "safari"} {
		b := &Browser{
			Name: name,
		}
		if app := macOSApps[name]; app != "" {
			if _, err := os.Stat(app); err == nil {
				b.App = app
			}
		}
		if b.App == "" {
			for _, cmd := range linuxCommands[name] {
				if p, err := exec.LookPath(cmd); err == nil {
					b.Path = p
					break
				}
			}
		}
		if b.App == "" && b.Path == "" {
			continue
		}
		switch name {
		case "chrome":
			b.prepare = chromeArgs
			b.warmup = chromeWarmup
			b.binary = b.Path
			if b.App != "" {
				b.binary = filepath.Join(b.App, "Contents", "MacOS", "Google Chrome")
			}
		case "firefox":
			b.prepare = firefoxProfile
		case "safari":
			b.Shared = true
		}
		found = append(found, b)
	}
	return found
}

// URL is where b goes for t.
func (b *Browser) URL(t Target) string {
	return t.url(t.Host)
}

// Session is a running browser.
type Session struct {
	cmd  *exec.Cmd
	dir  string
	app  bool
	done chan struct{}
	// tabURL is set for a shared browser: Close closes its tabs on this URL
	// rather than stopping the user's browser.
	tabURL string
	// devtools is the debugging connection answering proxy challenges.
	devtools io.Closer
}

// Open launches b on t. Chrome and Firefox get a new profile directory and
// their own instance; headless hides their windows. Safari opens a tab in the
// user's own window, ignores headless, and must trust the server's
// certificate.
func (b *Browser) Open(ctx context.Context, t Target, headless bool) (*Session, error) {
	// Safari takes its proxy from the system (the capture tool's PAC
	// script) and asks the user for proxy credentials in a dialog: with
	// ProxyAuth, someone at the screen types them.
	if b.Shared {
		url := b.URL(t)
		if err := exec.CommandContext(ctx, "open", "-a", b.App, url).Run(); err != nil { //nolint:gosec // the app is a known browser location
			return nil, fmt.Errorf("opening %s: %w", b.Name, err)
		}
		return &Session{
			tabURL: url,
		}, nil
	}
	dir, err := os.MkdirTemp("", "mimicry-"+b.Name+"-")
	if err != nil {
		return nil, err
	}
	// Firefox meets the proxy challenge on its first page, so with ProxyAuth
	// it starts on about:blank and is navigated once the intercept is set.
	bidi := t.ProxyAuth != nil && b.Name == "firefox"
	if bidi {
		if t.debugPort, err = freePort(); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
	}
	args, err := b.prepare(dir, t, headless)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if b.warmup > 0 || bidi {
		args = append(args, "about:blank")
	} else {
		args = append(args, b.URL(t))
	}
	var cmd *exec.Cmd
	if b.App != "" {
		// -n: a new instance even if the user's browser is open. -W: open
		// exits when the browser does, so Wait tracks its lifetime.
		cmd = exec.CommandContext(ctx, "open", append([]string{"-n", "-W", "-a", b.App, "--args"}, args...)...) //nolint:gosec // the app is a known browser location
	} else {
		cmd = exec.CommandContext(ctx, b.Path, args...) //nolint:gosec // the path is a known browser location
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("starting %s: %w", b.Name, err)
	}
	s := &Session{
		cmd:  cmd,
		dir:  dir,
		app:  b.App != "",
		done: make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	if t.ProxyAuth != nil {
		switch {
		case bidi:
			s.devtools, err = firefoxAuth(ctx, t.debugPort, b.URL(t), t.ProxyAuth)
		case b.Name == "chrome":
			s.devtools, err = chromeAuth(ctx, dir, t.ProxyAuth)
		default:
			err = fmt.Errorf("%s cannot be given proxy credentials", b.Name)
		}
		if err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	if b.warmup > 0 {
		if err := b.handOff(ctx, s, dir, b.URL(t)); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return s, nil
}

// handOff waits out the warm-up, then passes url to the running instance by
// invoking the browser again on the same profile directory.
func (b *Browser) handOff(ctx context.Context, s *Session, dir, url string) error {
	select {
	case <-time.After(b.warmup):
	case <-s.done:
		return fmt.Errorf("%s exited during its warm-up", b.Name)
	case <-ctx.Done():
		return ctx.Err()
	}
	if out, err := exec.CommandContext(ctx, b.binary, "--user-data-dir="+dir, url).CombinedOutput(); err != nil { //nolint:gosec // the binary is a known browser location; dir is our own
		return fmt.Errorf("handing %s the capture URL: %w: %s", b.Name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Close stops the browser, killing it if it has not exited within five
// seconds, and removes its profile. For Safari it closes the capture tab.
func (s *Session) Close() error {
	if s.devtools != nil {
		_ = s.devtools.Close()
	}
	if s.tabURL != "" {
		script := `tell application "Safari" to close (every tab of every window whose URL starts with "` + s.tabURL + `")`
		if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil { //nolint:noctx,gosec // must run after the caller's context ends; the URL is ours
			return fmt.Errorf("closing the Safari tab: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	s.stop(false)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		s.stop(true)
		<-s.done
	}
	// Helper processes (Chrome's renderers, Firefox's content processes) can
	// outlive the main one by a moment and keep writing to the profile.
	if s.app {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			if exec.Command("pgrep", "-f", s.dir).Run() != nil { //nolint:noctx,gosec // bounded by the loop; s.dir is our own temp directory
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return os.RemoveAll(s.dir)
}

// stop signals the browser. A browser started through open(1) is not our
// child, so it is found by its profile directory, which no other process uses.
func (s *Session) stop(kill bool) {
	if s.app {
		signal := "-TERM"
		if kill {
			signal = "-KILL"
		}
		_ = exec.Command("pkill", signal, "-f", s.dir).Run() //nolint:noctx,gosec // must run after the caller's context ends; s.dir is our own temp directory
		if kill {
			_ = s.cmd.Process.Kill()
		}
		return
	}
	if kill {
		_ = s.cmd.Process.Kill()
		return
	}
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = s.cmd.Process.Kill()
	}
}

func chromeArgs(dir string, t Target, headless bool) ([]string, error) {
	rules := make([]string, 0, 1+len(t.Hosts))
	for _, h := range append([]string{t.Host}, t.Hosts...) {
		rules = append(rules, "MAP "+h+" 127.0.0.1")
	}
	args := []string{
		"--user-data-dir=" + dir,
		"--no-first-run",
		"--no-default-browser-check",
		"--host-resolver-rules=" + strings.Join(rules, ","),
		// Certificate checks happen after the ClientHello and do not change it.
		"--ignore-certificate-errors",
	}
	if t.Proxy != nil {
		args = append(args, "--proxy-server="+t.Proxy.String())
	}
	if t.ProxyAuth != nil {
		args = append(args, "--remote-debugging-port=0", "--remote-allow-origins="+debugOrigin)
	}
	if t.H3LocalRoots {
		args = append(args, "--origin-to-force-quic-on="+chromeQUICOrigins(t))
	}
	if headless {
		args = append(args, "--headless=new")
	}
	return args, nil
}

// firefoxProfile writes prefs that map the capture hosts to 127.0.0.1 and
// silence first-run pages, plus a certificate exception for the server. A
// page served with a certificate per host has Firefox trust the system's
// roots instead, where the capture CA is trusted.
func firefoxProfile(dir string, t Target, headless bool) ([]string, error) {
	prefs := []string{
		`user_pref("network.dns.localDomains", "` + strings.Join(append([]string{t.Host}, t.Hosts...), ",") + `");`,
		`user_pref("network.dns.disableIPv6", true);`,
		`user_pref("browser.shell.checkDefaultBrowser", false);`,
		`user_pref("browser.startup.homepage_override.mstone", "ignore");`,
		`user_pref("browser.aboutwelcome.enabled", false);`,
		`user_pref("datareporting.policy.dataSubmissionEnabled", false);`,
		`user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);`,
	}
	if t.HostCerts {
		prefs = append(prefs, `user_pref("security.enterprise_roots.enabled", true);`)
	}
	if t.H3LocalRoots {
		prefs = append(prefs, `user_pref("network.http.http3.disable_when_third_party_roots_found", false);`)
	}
	if t.Proxy != nil {
		host, port := t.Proxy.Hostname(), t.Proxy.Port()
		prefs = append(prefs, `user_pref("network.proxy.type", 1);`)
		switch t.Proxy.Scheme {
		case "socks5":
			prefs = append(prefs,
				`user_pref("network.proxy.socks", "`+host+`");`,
				`user_pref("network.proxy.socks_port", `+port+`);`,
				`user_pref("network.proxy.socks_version", 5);`,
			)
		default:
			prefs = append(prefs,
				`user_pref("network.proxy.http", "`+host+`");`,
				`user_pref("network.proxy.http_port", `+port+`);`,
				`user_pref("network.proxy.ssl", "`+host+`");`,
				`user_pref("network.proxy.ssl_port", `+port+`);`,
			)
		}
	}
	if t.ProxyAuth != nil {
		// The remote agent otherwise applies its "recommended" automation
		// preferences, some of which change networking (speculative
		// connections, captive-portal checks).
		prefs = append(prefs, `user_pref("remote.prefs.recommended", false);`)
	}
	if err := os.WriteFile(filepath.Join(dir, "user.js"), []byte(strings.Join(prefs, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "cert_override.txt"), []byte(firefoxOverride(t)), 0o600); err != nil {
		return nil, err
	}
	args := []string{
		"--profile", dir,
		"--new-instance",
	}
	if t.ProxyAuth != nil {
		args = append(args, "--remote-debugging-port", strconv.Itoa(t.debugPort), "--remote-allow-origins", debugOrigin)
	}
	if headless {
		args = append(args, "--headless")
	}
	return args, nil
}

// firefoxOverride is a cert_override.txt accepting the server certificate for
// the target's host and port.
func firefoxOverride(t Target) string {
	pairs := make([]string, len(t.CertSHA256))
	for i, b := range t.CertSHA256 {
		pairs[i] = fmt.Sprintf("%02X", b)
	}
	return "# PSM Certificate Override Settings file\n" +
		"# This is a generated file!  Do not edit.\n" +
		t.Host + ":" + strconv.Itoa(t.Port) + ":\tOID.2.16.840.1.101.3.4.2.1\t" + strings.Join(pairs, ":") + "\t\n"
}
