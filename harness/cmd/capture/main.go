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
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/roachadam/mimicry/harness/internal/browser"
	"github.com/roachadam/mimicry/harness/internal/capture"
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
	headless bool
	samples  int
	timeout  time.Duration
	out      string
	cert     tls.Certificate
}

func run() error {
	out := flag.String("out", "testdata/captures", "directory to write captures to")
	only := flag.String("browsers", "", "comma-separated browsers to capture (default: every installed one)")
	headless := flag.Bool("headless", false, "hide browser windows; for checking the tool only: headless Chrome reports HeadlessChrome in its user agent")
	samples := flag.Int("samples", 3, "connections to sample per browser (at least 2, to tell whether it shuffles extensions)")
	caDir := flag.String("ca-dir", "", "capture CA directory (default: <user config dir>/mimicry/capture-ca)")
	timeout := flag.Duration("timeout", 45*time.Second, "how long to wait for each sample")
	flag.Parse()
	if *samples < 2 {
		return errors.New("-samples must be at least 2")
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
	ca, err := server.LoadOrCreateCA(*caDir, server.CAHosts, time.Now())
	if err != nil {
		return err
	}
	cert, err := ca.Issue(server.CAHosts, time.Now())
	if err != nil {
		return err
	}
	opts := options{
		headless: *headless,
		samples:  *samples,
		timeout:  *timeout,
		out:      *out,
		cert:     cert,
	}

	failed := 0
	for _, b := range browsers {
		if b.Shared && !server.TrustedBySystem(cert, "localhost") {
			log.Printf("%s: the capture CA is not trusted, so %s would stop at a certificate warning. Trust it once (macOS asks for your password):\n"+
				"  security add-trusted-cert -r trustRoot -p ssl -k ~/Library/Keychains/login.keychain-db %q\n"+
				"It can only vouch for %s.", b.Name, b.Name, ca.CertPath, strings.Join(server.CAHosts, " and "))
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

func captureBrowser(ctx context.Context, b *browser.Browser, opts options) error {
	samples := make([]*capture.Capture, 0, opts.samples)
	for i := range opts.samples {
		c, err := sample(ctx, b, opts)
		if err != nil {
			return fmt.Errorf("sample %d: %w", i+1, err)
		}
		samples = append(samples, c)
	}
	c, err := capture.Combine(samples)
	if err != nil {
		return err
	}
	c.Identify()
	if !c.HasExtension(profile.ExtServerName) {
		log.Printf("%s: warning: no server_name in the ClientHello", c.Profile.Name)
	}
	f := capture.Compute(&c.Profile)
	log.Printf("%s: extension order %s across %d samples", c.Profile.Name, c.Profile.TLS.ExtensionOrder, len(samples))
	log.Printf("%s: JA4 %s", c.Profile.Name, f.JA4)
	log.Printf("%s: Akamai %s", c.Profile.Name, f.Akamai)
	path, err := capture.Write(opts.out, c)
	if errors.Is(err, capture.ErrExists) {
		log.Printf("%s: %v; delete it to capture again", c.Profile.Name, err)
		return nil
	}
	if err != nil {
		return err
	}
	log.Printf("%s: wrote %s", c.Profile.Name, path)
	return nil
}

// sample runs a fresh capture server, sends the browser to it, and returns the
// first complete capture from that browser.
func sample(ctx context.Context, b *browser.Browser, opts options) (*capture.Capture, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	captures := make(chan *capture.Capture, 64)
	srv := &server.Server{
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				opts.cert,
			},
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
	log.Printf("%s: opening %s", b.Name, b.URL(t))
	session, err := b.Open(ctx, t, opts.headless)
	if err != nil {
		return nil, err
	}
	c, err := await(ctx, b.Name, captures, opts.timeout)
	if closeErr := session.Close(); closeErr != nil {
		log.Printf("%s: cleaning up: %v", b.Name, closeErr)
	}
	return c, err
}

// await returns the first complete capture from the named browser. Captures
// from other browsers, or with only a ClientHello, are skipped.
func await(ctx context.Context, name string, captures <-chan *capture.Capture, timeout time.Duration) (*capture.Capture, error) {
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
			if c.Trace != nil && !c.Finished {
				// The connection ended before the page asked for /done, so
				// its trace stops partway; the page carries on elsewhere.
				paths := make([]string, 0, len(c.Trace.Requests))
				for _, r := range c.Trace.Requests {
					paths = append(paths, r.Path)
				}
				log.Printf("%s: an HTTP/2 connection ended before /done after %v (last frame at %d ms); waiting for the rest",
					name, paths, c.Trace.Frames[len(c.Trace.Frames)-1].Millis)
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
