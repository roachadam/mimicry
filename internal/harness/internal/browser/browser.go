// Package browser launches real release builds of Chrome, Firefox and Safari
// against the capture server.
package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Target is the capture server browsers connect to.
type Target struct {
	// Host is the capture hostname, under .test, so browsers send
	// server_name. Chrome and Firefox map it to 127.0.0.1 themselves; Safari
	// resolves it through the capture tool's DNS stub.
	Host string
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
	// Proxy, when set, is the proxy the browser goes through
	// ("http://127.0.0.1:port" or "socks5://127.0.0.1:port").
	Proxy *url.URL
	// Plain sends the browser to http:// instead of https://.
	Plain bool
	// Path is the start URL's path; "" for "/".
	Path string
	// ProxyAuth, when set, are the credentials the browser answers proxy
	// challenges with, as a user typing them into the login dialog.
	ProxyAuth *url.Userinfo
	// H3LocalRoots lets Chrome and Firefox use QUIC with a certificate
	// from a root not built in, as the capture CA's. It is a capture
	// condition; see decision 0026.
	H3LocalRoots bool
	// ClickTitle, when set, has the tool click the middle of every page
	// with this title, once per URL, so the page gets user activation.
	// Safari needs Accessibility access for it.
	ClickTitle string
	// debugPort is Firefox's remote debugging port, for ProxyAuth or
	// ClickTitle.
	debugPort int
}

// h3UnusedPort is the port of the origins Chrome forces QUIC on: one the
// page never uses, so nothing is forced but the hosts may use a local root.
const h3UnusedPort = 1

// Conditions describes, with reasons, what b changes from its defaults for
// t beyond host mapping and certificate acceptance. A capture records them.
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
}

// chromeWarmup is how long a fresh Chrome profile settles before the capture
// page. In its first seconds it rebuilds its certificate verifiers and marks
// every open connection as going away (net log: CERT_VERIFY_PROC_CREATED),
// which a long-running Chrome never does.
const chromeWarmup = 5 * time.Second

// ChromeApp is the Chrome application Find looks for on macOS; cmd/capture's
// -chrome-app points it at another build (Chrome for Testing).
var ChromeApp = "/Applications/Google Chrome.app"

// DataDir is where the browsers' throwaway profile directories are made;
// "" is the system's temporary directory. On Windows the browser runs in the
// console user's session and the capture tool reads the profile from an
// ssh session, so cmd/capture puts it under the run's directory.
var DataDir string

// URL is where b goes for t.
func (b *Browser) URL(t Target) string {
	return t.url(t.Host)
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
}

// Open launches b on t. Chrome and Firefox get a new profile directory and
// their own instance; headless hides their windows. Safari opens a tab in the
// user's own window, ignores headless, and must trust the server's
// certificate.
func (b *Browser) Open(ctx context.Context, t Target, headless bool) (*Session, error) {
	// Safari takes its proxy from the system PAC script and asks for proxy
	// credentials in a dialog, which someone at the screen fills in.
	if t.ProxyAuth != nil && t.ClickTitle != "" {
		return nil, errors.New("a capture both answering proxy challenges and clicking is not supported")
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

	if t.ClickTitle != "" {
		switch b.Name {
		case "chrome":
			s.clicker, err = chromeClicker(ctx, dir, t.Host, t.ClickTitle)
		case "firefox":
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

	return b.handTo(ctx, dir, url)
}

// Close stops the browser, killing it if it has not exited within five
// seconds, and removes its profile. For Safari it closes the capture tab.
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

// quit closes Safari's capture tab, or stops the browser and removes its
// profile.
func (s *Session) quit() error {
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

	var err error
	if s.release != nil {
		err = s.release()
	}

	return errors.Join(err, s.removeProfile())
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
