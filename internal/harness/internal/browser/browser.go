// Package browser launches real release builds of Chrome, Edge, Brave,
// Firefox and Safari against the capture server, and sends Safari, Chrome
// or Firefox on an iPhone to it.
package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Target is the capture server browsers connect to.
type Target struct {
	// Host is the capture hostname, under .test, so browsers send
	// server_name. Chrome, Edge, Brave and Firefox map it to Addr themselves;
	// Safari resolves it through the capture tool's DNS stub.
	Host string
	// Addr is the address the capture hosts resolve to and the server
	// listens on: 127.0.0.1, or this machine's address on the network for
	// a browser on another device (cmd/capture -listen).
	Addr string
	Port int
	// Hosts are the other hostnames a replayed page reaches, all served
	// on Port.
	Hosts []string
	// HostCerts says the server gives each host its own certificate from
	// the capture CA, so Firefox trusts the system roots instead of an
	// exception for CertSHA256.
	HostCerts bool
	// CertSHA256 is the SHA-256 of the server certificate (DER), which Firefox
	// is told to accept.
	CertSHA256 [32]byte
	// CAFile is the capture CA's PEM certificate. On Linux each run's
	// profile trusts it (trustInProfile), as the system roots do on macOS
	// (by hand, once) and Windows (for the run).
	CAFile string
	// Proxy, when set, is the proxy the browser goes through
	// ("http://127.0.0.1:port", "socks5://127.0.0.1:port", or
	// "https://<name>:port", a TLS proxy whose name the browser maps to
	// Loopback).
	Proxy *url.URL
	// Plain sends the browser to http:// instead of https://.
	Plain bool
	// Path is the start URL's path; "" for "/".
	Path string
	// Scenario is the capture scenario's name, for a browser launched
	// otherwise for some scenarios (Chrome and Firefox on an iPhone
	// restart for iosRedirectRestarts).
	Scenario string
	// ProxyAuth, when set, are the credentials the browser answers proxy
	// challenges with, as a user typing them into the login dialog.
	ProxyAuth *url.Userinfo
	// H3LocalRoots lets Chromium and Firefox use QUIC with a certificate
	// from a root not built in, as the capture CA's. It is a capture
	// condition; see decision 0026.
	H3LocalRoots bool
	// ClickTitle, when set, has the tool click the middle of every page
	// with this title, once per URL, so the page gets user activation.
	// Safari needs Accessibility access for it.
	ClickTitle string
	// ClientCert, when set, is the identity the browser holds for a server
	// asking for a client certificate: Firefox imports it into its
	// profile's own certificate database (on Windows it reads the user's
	// store instead, where the capture tool put it), as the Chromium browsers do
	// on Linux; on macOS and Windows they read the system's store, where
	// the capture tool put it for the scenario.
	ClientCert *ClientIdentity
	// debugPort is Firefox's remote debugging port, for ProxyAuth or
	// ClickTitle.
	debugPort int
}

// Loopback is Target.Addr unless the capture tool serves the network.
const Loopback = "127.0.0.1"

// h3UnusedPort is the port of the origins Chromium forces QUIC on: one the
// page never uses, so nothing is forced but the hosts may use a local root.
const h3UnusedPort = 1

// Conditions describes, with reasons, what b changes from its defaults for
// t beyond host mapping and certificate acceptance. A capture records them.
func (b *Browser) Conditions(t Target) []string {
	if !t.H3LocalRoots {
		return nil
	}

	switch {
	case chromium(b.Name):
		return []string{
			"--origin-to-force-quic-on=" + chromeQUICOrigins(t) + ": Chrome refuses a QUIC certificate not issued by a known root (ERR_QUIC_CERT_ROOT_NOT_KNOWN, net/quic/crypto/proof_verifier_chromium.cc) unless its host is among these origins' hosts; it forces QUIC only on these origins, whose port the page never uses, so HTTP/3 is still discovered through Alt-Svc",
		}

	case b.Name == "firefox":
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

// proxyHost is the name of t's proxy that the browser maps to Loopback,
// "" when the proxy is an address.
func (t Target) proxyHost() string {
	if t.Proxy == nil {
		return ""
	}

	if _, err := netip.ParseAddr(t.Proxy.Hostname()); err == nil {
		return ""
	}

	return t.Proxy.Hostname()
}

// trustsCA says the browser checks a certificate from the capture CA, not
// only the server's: the server gives each host its own (HostCerts), or
// the proxy is reached over TLS. Firefox then trusts the CA (in the
// system's roots, or on Linux its profile's) instead of an exception for
// CertSHA256.
func (t Target) trustsCA() bool {
	return t.HostCerts || t.Proxy != nil && t.Proxy.Scheme == "https"
}

// Browser is a browser that can be launched against a Target: one installed
// here, or one on an iPhone (IOS).
type Browser struct {
	// Name is how cmd/capture's -browsers names it; capture.ParseUserAgent
	// names its user agent as Agent says.
	Name string
	// Path is the executable, used where there is no App.
	Path string
	// App is the macOS application bundle. When set, the browser is launched
	// through open(1) as its own application: as a child process it inherits
	// our privacy restrictions, and Firefox exits.
	App string
	// Shared marks a browser that cannot run a separate instance or profile:
	// it opens a tab in the user's own window. Only Safari.
	Shared bool
	// prepare writes any profile files into dir and returns the arguments.
	prepare func(dir string, t Target, headless bool) ([]string, error)
	// warmup, when set, starts the browser on about:blank and hands it the
	// capture URL this much later through a second invocation of binary.
	warmup time.Duration
	binary string
	// ios, when set, is a browser on an iPhone reached over the network.
	ios *iosBrowser
}

// chromium reports whether the browser named name is Chrome, Edge or
// Brave, which share Chromium's launch flags, CDP driving and network
// stack: the same test as capture.Chromium, kept here so this package
// imports nothing of the module's (its depguard rule).
func chromium(name string) bool {
	return name == "chrome" || name == "edge" || name == "brave"
}

// chromeAgent are the browsers whose user agent is Chrome's, word for word:
// Brave's (decision 0067). capture.ParseUserAgent names them chrome, so a
// capture of one is named after the browser launched (Named).
var chromeAgent = map[string]bool{
	"brave": true,
}

// chromeWarmup is how long a fresh Chrome profile settles before the capture
// page. In its first seconds it rebuilds its certificate verifiers and marks
// every open connection as going away (net log: CERT_VERIFY_PROC_CREATED),
// which a long-running Chrome never does.
const chromeWarmup = 5 * time.Second

// ChromeApp is the Chrome application Find looks for on macOS; cmd/capture's
// -chrome-app points it at another build (Chrome for Testing).
var ChromeApp = "/Applications/Google Chrome.app"

// FirefoxApp is the Firefox Find uses instead of the installed one, ""
// for that one: an application on macOS, firefox.exe on Windows.
// cmd/capture's -firefox-app points it at an older release kept beside the
// installed one (Mozilla's archive keeps every release).
var FirefoxApp string

// DataDir is where the browsers' throwaway profile directories are made;
// "" is the system's temporary directory. On Windows the browser runs in the
// console user's session and the capture tool reads the profile from an
// ssh session, so cmd/capture puts it under the run's directory.
var DataDir string

// URL is where b goes for t.
func (b *Browser) URL(t Target) string {
	return t.url(t.Host)
}

// Agent is the browser and platform capture.ParseUserAgent finds in b's
// user agent; platform "" is any. Brave's is chrome.
func (b *Browser) Agent() (name, platform string) {
	if b.ios != nil {
		return b.ios.app.agent, "ios"
	}

	if chromeAgent[b.Name] {
		return "chrome", ""
	}

	return b.Name, ""
}

// Named is the browser a capture of b is named after where its user agent
// cannot tell (Brave's is Chrome's), "" where the user agent names it
// (capture.Capture.Identify).
func (b *Browser) Named() string {
	if chromeAgent[b.Name] {
		return b.Name
	}

	return ""
}

// Session is a running browser.
type Session struct {
	proc *os.Process
	dir  string
	app  bool
	// release frees what the platform's launch holds beyond the process
	// (Windows: its scheduled task); nil when there is nothing.
	release func() error
	done    chan struct{}
	// tabURL is set for a shared browser: Close closes its tabs on this URL
	// rather than stopping the user's browser.
	tabURL string
	// devtools is the debugging connection answering proxy challenges,
	// clicker the one clicking (Target.ClickTitle).
	devtools io.Closer
	clicker  io.Closer
	// device marks a session on another device, which Close leaves alone
	// but for remote, the WebDriver session when there is one.
	device bool
	remote io.Closer
	// output keeps the end of what a browser started as a child process
	// wrote (Linux), for ErrWarmupExit; nil elsewhere.
	output *tailWriter
}

// tailWriter keeps the last tailBytes written to it.
type tailWriter struct {
	mu  sync.Mutex
	buf []byte
}

// tailBytes is how much of a browser's output tailWriter keeps.
const tailBytes = 2048

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)
	if over := len(w.buf) - tailBytes; over > 0 {
		w.buf = append(w.buf[:0], w.buf[over:]...)
	}

	return len(p), nil
}

// String is what was kept, trimmed.
func (w *tailWriter) String() string {
	if w == nil {
		return ""
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	return strings.TrimSpace(string(w.buf))
}

// Open launches b on t. Chrome, Edge, Brave and Firefox get a new profile directory
// and their own instance; headless hides their windows. Safari opens a tab
// in the user's own window, ignores headless, and must trust the server's
// certificate. A browser on an iPhone is sent to t by its driver.
func (b *Browser) Open(ctx context.Context, t Target, headless bool) (*Session, error) {
	// Safari takes its proxy from the system PAC script and asks for proxy
	// credentials in a dialog, which someone at the screen fills in.
	if t.ProxyAuth != nil && t.ClickTitle != "" {
		return nil, errors.New("a capture both answering proxy challenges and clicking is not supported")
	}

	if b.ios != nil {
		return b.ios.open(ctx, b.URL(t), t)
	}

	if b.Shared {
		if t.ClickTitle != "" {
			if err := safariCanClick(ctx); err != nil {
				return nil, err
			}
		}

		url := b.URL(t)
		if err := exec.CommandContext(ctx, "open", "-a", b.App, url).Run(); err != nil { //nolint:gosec // the app is a known browser location
			return nil, fmt.Errorf("opening %s: %w", b.Name, err)
		}

		s := &Session{
			tabURL: url,
		}
		if t.ClickTitle != "" {
			s.clicker = safariClicker(ctx, t.ClickTitle)
		}

		return s, nil
	}

	dir, err := os.MkdirTemp(DataDir, "mimicry-"+b.Name+"-")
	if err != nil {
		return nil, err
	}

	// Firefox meets the proxy challenge on its first page, so with ProxyAuth
	// it starts on about:blank and is navigated once the intercept is set.
	bidi := t.ProxyAuth != nil && b.Name == "firefox"
	if b.Name == "firefox" && (t.ProxyAuth != nil || t.ClickTitle != "") {
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

	if err := trustInProfile(ctx, b, dir, t); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	if err := importClientCert(ctx, b, dir, t); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	if b.warmup > 0 || bidi {
		args = append(args, "about:blank")
	} else {
		args = append(args, b.URL(t))
	}

	s, err := b.start(ctx, dir, args)
	if err != nil {
		return nil, err
	}

	if t.ProxyAuth != nil {
		switch {
		case bidi:
			s.devtools, err = firefoxAuth(ctx, t.debugPort, b.URL(t), t.ProxyAuth)
		case chromium(b.Name):
			s.devtools, err = b.chromeAuth(ctx, s, dir, t.ProxyAuth)
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

	if t.ClickTitle != "" {
		switch {
		case chromium(b.Name):
			s.clicker, err = chromeClicker(ctx, dir, t.Host, t.ClickTitle)
		case b.Name == "firefox":
			s.clicker, err = firefoxClicker(ctx, t.debugPort, t.ClickTitle)
		default:
			err = fmt.Errorf("%s cannot be clicked", b.Name)
		}

		if err != nil {
			_ = s.Close()
			return nil, err
		}
	}

	return s, nil
}

// ErrWarmupExit is a browser that exited before its warm-up ended, before
// it was sent to the page: Brave on Linux did (2026-11-09, "The platform
// failed to initialize") once the processes of its earlier samples, left
// running by its launcher script, had piled up; stop and reap now end
// them.
var ErrWarmupExit = errors.New("exited during its warm-up")

// chromeAuth is chromeAuth for session s, which stops waiting for the
// debugging port when the browser exits (ErrWarmupExit).
func (b *Browser) chromeAuth(ctx context.Context, s *Session, dir string, user *url.Userinfo) (io.Closer, error) {
	wait, stop := context.WithCancel(ctx)
	defer stop()

	go func() {
		select {
		case <-s.done:
			stop()
		case <-wait.Done():
		}
	}()

	c, err := chromeAuth(wait, dir, user)
	if err != nil {
		select {
		case <-s.done:
			return nil, b.exited(s)
		default:
		}
	}

	return c, err
}

// exited is the ErrWarmupExit of session s, whose browser exited, with the
// end of what it wrote where that was kept.
func (b *Browser) exited(s *Session) error {
	if out := s.output.String(); out != "" {
		return fmt.Errorf("%s %w; its output ended: %s", b.Name, ErrWarmupExit, out)
	}

	return fmt.Errorf("%s %w", b.Name, ErrWarmupExit)
}

// handOff waits out the warm-up, then passes url to the running instance by
// invoking the browser again on the same profile directory.
func (b *Browser) handOff(ctx context.Context, s *Session, dir, url string) error {
	select {
	case <-time.After(b.warmup):
	case <-s.done:
		return b.exited(s)
	case <-ctx.Done():
		return ctx.Err()
	}

	return b.handTo(ctx, dir, url)
}

// Close stops the browser, killing it if it has not exited within five
// seconds, and removes its profile. For Safari it closes the capture tab; on
// an iPhone it ends the WebDriver session, if any.
func (s *Session) Close() error {
	var clickErr error
	if s.clicker != nil {
		clickErr = s.clicker.Close()
	}

	if s.devtools != nil {
		_ = s.devtools.Close()
	}

	return errors.Join(clickErr, s.quit())
}

// quit closes Safari's capture tab, ends a device's WebDriver session, or
// stops the browser and removes its profile.
func (s *Session) quit() error {
	if s.device {
		if s.remote != nil {
			return s.remote.Close()
		}

		// Opened by hand: the tab on the phone is the maintainer's.
		return nil
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

	s.reap()

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

	var err error
	if s.release != nil {
		err = s.release()
	}

	return errors.Join(err, s.removeProfile())
}

func chromeArgs(dir string, t Target, headless bool) ([]string, error) {
	rules := make([]string, 0, 2+len(t.Hosts))
	for _, h := range append([]string{t.Host}, t.Hosts...) {
		rules = append(rules, "MAP "+h+" "+t.Addr)
	}

	if h := t.proxyHost(); h != "" {
		rules = append(rules, "MAP "+h+" "+Loopback)
	}

	args := []string{
		"--user-data-dir=" + dir,
		"--no-first-run",
		"--no-default-browser-check",
		// Local storage encryption only (cookies and passwords at rest),
		// never on the wire: a build without its own login-keychain item
		// (Chrome for Testing) otherwise asks for the keychain on screen.
		// --use-mock-keychain is macOS's, --password-store=basic Linux's;
		// other platforms ignore them.
		"--use-mock-keychain",
		"--password-store=basic",
		"--host-resolver-rules=" + strings.Join(rules, ","),
		// Certificate checks happen after the ClientHello and do not change it.
		"--ignore-certificate-errors",
	}
	if t.Proxy != nil {
		args = append(args, "--proxy-server="+t.Proxy.String())
	}

	if t.ProxyAuth != nil || t.ClickTitle != "" {
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
