// Command capture launches the installed browsers against the capture server
// and writes each browser's capture, with its JA3, JA4 and Akamai fingerprints,
// to the output directory.
//
// Each browser is sampled several times, each time on a fresh server and port
// so that no connection or session carries over. If the samples differ only in
// extension order, the browser shuffles its extensions; any other difference
// is an error, because the profile could not describe it.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/roachadam/mimicry/harness/internal/browser"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/dnsstub"
	"github.com/roachadam/mimicry/harness/internal/pageshape"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "capture:", err)
		os.Exit(1)
	}
}

type options struct {
	headless  bool
	samples   int
	timeout   time.Duration
	out       string
	ca        *server.CA
	cert      tls.Certificate
	scenarios []*server.Scenario
}

func run() error {
	out := flag.String("out", "testdata/captures", "directory to write captures to")
	only := flag.String("browsers", "", "comma-separated browsers to capture (default: every installed one)")
	headless := flag.Bool("headless", false, "hide browser windows; for checking the tool only: headless Chrome reports HeadlessChrome in its user agent")
	samples := flag.Int("samples", 3, "connections to sample per browser (at least 2, to tell whether it shuffles extensions)")
	caDir := flag.String("ca-dir", "", "capture CA directory (default: <user config dir>/mimicry/capture-ca)")
	timeout := flag.Duration("timeout", 45*time.Second, "how long to wait for each sample")
	scenarioNames := flag.String("scenarios", "", "comma-separated scenarios to capture (default: all but replays; see internal/server/scenario.go); "+
		"replay-<site> replays -pages/<site>.json, replay all of them")
	pages := flag.String("pages", "testdata/pages", "directory of recorded page shapes (cmd/pageshape) for replay scenarios")
	flag.Parse()
	if *samples < 2 {
		return errors.New("-samples must be at least 2")
	}
	scenarios := server.Scenarios
	if *scenarioNames != "" {
		scenarios = nil
		for name := range strings.SplitSeq(*scenarioNames, ",") {
			found, err := scenariosNamed(name, *pages)
			if err != nil {
				return err
			}
			scenarios = append(scenarios, found...)
		}
	}

	browsers := browser.Find()
	if *only != "" {
		names := strings.Split(*only, ",")
		browsers = slices.DeleteFunc(browsers, func(b *browser.Browser) bool {
			return !slices.Contains(names, b.Name)
		})
	}
	if len(browsers) == 0 {
		return errors.New("no supported browser found")
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *caDir == "" {
		dir, err := server.DefaultCADir()
		if err != nil {
			return err
		}
		*caDir = dir
	}
	ca, err := server.LoadOrCreateCA(*caDir, server.CADomains, time.Now())
	if err != nil {
		return err
	}
	cert, err := ca.Issue(server.CAHosts, time.Now())
	if err != nil {
		return err
	}
	opts := options{
		headless:  *headless,
		samples:   *samples,
		timeout:   *timeout,
		out:       *out,
		ca:        ca,
		cert:      cert,
		scenarios: scenarios,
	}
	// Safari resolves through the system: the stub answers its *.test
	// lookups (Chrome and Firefox map the names themselves).
	dns, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", dnsstub.Addr)
	if err != nil {
		log.Printf("DNS stub for *.test on %s: %v; Safari will not reach the capture hosts", dnsstub.Addr, err)
	} else {
		go func() {
			if err := dnsstub.Serve(ctx, dns); err != nil {
				log.Printf("DNS stub: %v", err)
			}
		}()
	}

	failed := 0
	for _, b := range browsers {
		if b.Shared && !dnsstub.ResolverInstalled() {
			log.Printf("%s: %s does not send *.test lookups to the capture tool, so %s cannot reach the capture hosts. Create it once:\n  %s",
				b.Name, dnsstub.ResolverFile, b.Name, dnsstub.InstallCommand)
			failed++
			continue
		}
		if b.Shared && !server.TrustedBySystem(cert, server.CaptureHost) {
			log.Printf("%s: the capture CA is not trusted, so %s would stop at a certificate warning. Trust it once (macOS asks for your password):\n"+
				"  security add-trusted-cert -r trustRoot -p ssl -k ~/Library/Keychains/login.keychain-db %q\n"+
				"It can only vouch for names under %s.", b.Name, b.Name, ca.CertPath, strings.Join(server.CADomains, " and "))
			failed++
			continue
		}
		if err := captureBrowser(ctx, b, opts); err != nil {
			log.Printf("%s: %v", b.Name, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d browsers failed", failed, len(browsers))
	}
	return nil
}

// captureBrowser captures each scenario. Profiles come from the default
// scenario; every other scenario's first request is compared with it, since
// a browser that sends it differently under other server behaviour is a
// finding.
func captureBrowser(ctx context.Context, b *browser.Browser, opts options) error {
	var base *capture.Capture
	for _, sc := range opts.scenarios {
		c, err := captureScenario(ctx, b, sc, opts)
		if err != nil {
			return fmt.Errorf("scenario %s: %w", sc.Name, err)
		}
		if sc == server.DefaultScenario {
			base = c
			if !c.HasExtension(profile.ExtServerName) {
				log.Printf("%s: warning: no server_name in the ClientHello", c.Profile.Name)
			}
			f := capture.Compute(&c.Profile)
			log.Printf("%s: extension order %s across %d samples", c.Profile.Name, c.Profile.TLS.ExtensionOrder, opts.samples)
			log.Printf("%s: JA4 %s", c.Profile.Name, f.JA4)
			log.Printf("%s: Akamai %s", c.Profile.Name, f.Akamai)
		} else if _, _, multi := sc.MultiHost(); base != nil && !sc.HTTP1 && !multi {
			for _, d := range diff.Compare(&base.Profile, &c.Profile) {
				log.Printf("%s: scenario %s changes the first request: %s", c.Profile.Name, sc.Name, d)
			}
		}
		path, err := capture.Write(opts.out, c)
		if errors.Is(err, capture.ErrExists) {
			log.Printf("%s: %v; delete it to capture again", c.Profile.Name, err)
			continue
		}
		if err != nil {
			return err
		}
		log.Printf("%s: wrote %s", c.Profile.Name, path)
	}
	return nil
}

func captureScenario(ctx context.Context, b *browser.Browser, sc *server.Scenario, opts options) (*capture.Capture, error) {
	samples := make([]*capture.Capture, 0, opts.samples)
	for i := range opts.samples {
		c, err := sample(ctx, b, sc, opts)
		if err != nil {
			return nil, fmt.Errorf("sample %d: %w", i+1, err)
		}
		samples = append(samples, c)
	}
	c, err := capture.Combine(samples)
	if err != nil {
		return nil, err
	}
	c.Identify()
	return c, nil
}

// sample runs a fresh capture server answering as sc, sends the browser to
// it, and returns the first complete capture from that browser.
func sample(ctx context.Context, b *browser.Browser, sc *server.Scenario, opts options) (*capture.Capture, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	// A replayed page opens a connection per host, each handing over a
	// capture; the buffer holds them all so the one asking for /done is
	// never dropped.
	captures := make(chan *capture.Capture, 1024)
	srv := &server.Server{
		Scenario: sc,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				opts.cert,
			},
		},
		IssueCert: func(hosts []string) (tls.Certificate, error) {
			return opts.ca.Issue(hosts, time.Now())
		},
		OnCapture: func(c *capture.Capture) {
			select {
			case captures <- c:
			default:
			}
		},
		// A connection ended by a server error yields a truncated trace;
		// say so rather than let it pass as browser behaviour.
		Logf: func(format string, args ...any) {
			log.Printf("%s: server: "+format, append([]any{b.Name}, args...)...)
		},
	}
	serveCtx, stopServer := context.WithCancel(ctx)
	served := make(chan struct{})
	go func() {
		_ = srv.Serve(serveCtx, ln)
		close(served)
	}()
	defer func() {
		stopServer()
		<-served
	}()

	t := browser.Target{
		Host:       server.CaptureHost,
		Port:       ln.Addr().(*net.TCPAddr).Port,
		CertSHA256: sha256.Sum256(opts.cert.Certificate[0]),
	}
	timeout := opts.timeout
	hosts, length, multi := sc.MultiHost()
	if multi {
		t.Host = hosts[0]
		t.Hosts = hosts[1:]
		timeout += length
	}
	log.Printf("%s: opening %s", b.Name, b.URL(t))
	session, err := b.Open(ctx, t, opts.headless)
	if err != nil {
		return nil, err
	}
	c, err := await(ctx, b.Name, captures, timeout, multi)
	if closeErr := session.Close(); closeErr != nil {
		log.Printf("%s: cleaning up: %v", b.Name, closeErr)
	}
	return c, err
}

// await returns the first complete capture from the named browser. Captures
// from other browsers, or with only a ClientHello, are skipped. A page
// across several hosts spans many connections; only the one asking for
// /done carries it.
func await(ctx context.Context, name string, captures <-chan *capture.Capture, timeout time.Duration, page bool) (*capture.Capture, error) {
	deadline := time.After(timeout)
	for {
		select {
		case c := <-captures:
			if !c.Complete() {
				// Usually a rejected certificate: the browser sent a ClientHello
				// and gave up. Logged because it is the first thing to check
				// when a browser times out.
				log.Printf("%s: a connection ended without a request (ALPN %q)", name, c.Protocol)
				continue
			}
			if browserName, _, _ := capture.ParseUserAgent(c.UserAgent()); browserName != name {
				continue
			}
			if page && !c.Finished {
				continue
			}
			if c.Trace != nil && !c.Finished {
				// The connection ended before the page asked for /done, so
				// its trace stops partway; the page carries on elsewhere.
				paths := make([]string, 0, len(c.Trace.Requests))
				for _, r := range c.Trace.Requests {
					paths = append(paths, r.Path)
				}
				frames := c.Trace.Frames[max(0, len(c.Trace.Frames)-6):]
				last := make([]string, len(frames))
				for i, f := range frames {
					last[i] = fmt.Sprintf("%s@%d+%dms", f.Type, f.Stream, f.Millis)
				}
				log.Printf("%s: an HTTP/2 connection ended before /done after %v; its last frames: %s; waiting for the rest",
					name, paths, strings.Join(last, " "))
				continue
			}
			if len(c.H1) > 0 && !c.Finished {
				// One of the page's HTTP/1.1 connections; the one that asks
				// for /done carries the whole page load.
				continue
			}
			return c, nil
		case <-deadline:
			return nil, fmt.Errorf("no request within %s", timeout)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// scenariosNamed resolves a -scenarios entry: a built-in scenario,
// replay-<site> for pages/<site>.json, or replay for every page there.
func scenariosNamed(name, pages string) ([]*server.Scenario, error) {
	var files []string
	switch {
	case name == "replay":
		var err error
		if files, err = filepath.Glob(filepath.Join(pages, "*.json")); err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("no page shapes in %s", pages)
		}
	case strings.HasPrefix(name, "replay-"):
		files = []string{
			filepath.Join(pages, strings.TrimPrefix(name, "replay-")+".json"),
		}
	default:
		sc, err := server.ScenarioByName(name)
		if err != nil {
			return nil, err
		}
		return []*server.Scenario{
			sc,
		}, nil
	}
	out := make([]*server.Scenario, 0, len(files))
	for _, f := range files {
		p, err := pageshape.Load(f)
		if err != nil {
			return nil, err
		}
		sc, err := server.Replay(p)
		if err != nil {
			return nil, err
		}
		log.Print(sc.Doc)
		out = append(out, sc)
	}
	return out, nil
}
