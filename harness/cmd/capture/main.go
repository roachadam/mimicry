// Command capture launches the installed browsers against an in-process
// capture server and writes each browser's capture, with its JA3, JA4 and
// Akamai fingerprints, to the output directory.
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

func run() error {
	out := flag.String("out", "testdata/captures", "directory to write captures to")
	only := flag.String("browsers", "", "comma-separated browsers to capture (default: every installed one)")
	headless := flag.Bool("headless", false, "hide browser windows; for checking the tool only: headless Chrome reports HeadlessChrome in its user agent")
	host := flag.String("host", "capture.mimicry.test", "hostname browsers connect to; resolved to 127.0.0.1 inside each browser")
	timeout := flag.Duration("timeout", 45*time.Second, "how long to wait for each browser")
	flag.Parse()

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
	cert, err := server.SelfSigned([]string{
		*host,
	}, time.Now())
	if err != nil {
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	captures := make(chan *capture.Capture, 64)
	srv := &server.Server{
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
		},
		OnCapture: func(c *capture.Capture) {
			select {
			case captures <- c:
			default:
			}
		},
	}
	serveCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	go func() {
		_ = srv.Serve(serveCtx, ln)
	}()

	target := browser.Target{
		Host:       *host,
		URL:        fmt.Sprintf("https://%s:%d/", *host, ln.Addr().(*net.TCPAddr).Port),
		CertSHA256: sha256.Sum256(cert.Certificate[0]),
	}
	failed := 0
	for _, b := range browsers {
		if err := captureOne(ctx, b, target, captures, *headless, *timeout, *out); err != nil {
			log.Printf("%s: %v", b.Name, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d browsers failed", failed, len(browsers))
	}
	return nil
}

func captureOne(ctx context.Context, b *browser.Browser, t browser.Target, captures <-chan *capture.Capture, headless bool, timeout time.Duration, out string) error {
	log.Printf("%s: opening %s", b.Name, t.URL)
	session, err := b.Open(ctx, t, headless)
	if err != nil {
		return err
	}
	c, err := await(ctx, b.Name, captures, timeout)
	if closeErr := session.Close(); closeErr != nil {
		log.Printf("%s: cleaning up: %v", b.Name, closeErr)
	}
	if err != nil {
		return err
	}
	c.Identify()
	if !hasExtension(c, profile.ExtServerName) {
		log.Printf("%s: warning: no server_name in the ClientHello", c.Profile.Name)
	}
	f := capture.Compute(&c.Profile)
	log.Printf("%s: JA4 %s", c.Profile.Name, f.JA4)
	log.Printf("%s: JA3 %s", c.Profile.Name, f.JA3)
	log.Printf("%s: Akamai %s", c.Profile.Name, f.Akamai)
	path, err := capture.Write(out, c)
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
			if browserName, _, _ := capture.ParseUserAgent(c.UserAgent()); browserName == name {
				return c, nil
			}
		case <-deadline:
			return nil, fmt.Errorf("no request within %s", timeout)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func hasExtension(c *capture.Capture, typ profile.Code) bool {
	for _, e := range c.Profile.TLS.Extensions {
		if e.Type == typ {
			return true
		}
	}
	return false
}
