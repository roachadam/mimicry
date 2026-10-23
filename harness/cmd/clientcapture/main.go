// Command clientcapture records what another HTTP client sends, to compare it
// with the browsers' captures:
//
//	go run ./cmd/clientcapture -scenario set-cookie -out DIR -name curl-chrome152 -- \
//	    curl_chrome152_macos -sk -c JAR -b JAR {url}/ {url}/again
//
// It serves the scenario on a fresh port, runs the command with every {url}
// replaced by the server's https://localhost:<port>, and once the command has
// exited writes every connection the client opened as DIR/<name>[-<n>].json
// (the profile-format capture), .clienthello and .h2trace.json.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/profile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "clientcapture:", err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("out", "", "directory to write captures to (required)")
	name := flag.String("name", "", "name for the captures (required)")
	scenarioName := flag.String("scenario", server.DefaultScenario.Name, "scenario to serve (see internal/server/scenario.go)")
	samples := flag.Int("samples", 3, "times to run the client, each against a fresh server (at least 2, to tell whether it shuffles extensions)")
	caDir := flag.String("ca-dir", "", "capture CA directory (default: <user config dir>/mimicry/capture-ca)")
	flag.Parse()
	if *out == "" || *name == "" || flag.NArg() == 0 || *samples < 2 {
		flag.Usage()
		os.Exit(2)
	}
	sc, err := server.ScenarioByName(*scenarioName)
	if err != nil {
		return err
	}
	if *caDir == "" {
		if *caDir, err = server.DefaultCADir(); err != nil {
			return err
		}
	}
	ca, err := server.LoadOrCreateCA(*caDir, server.CADomains, time.Now())
	if err != nil {
		return err
	}
	cert, err := ca.Issue(server.CAHosts, time.Now())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	captured := make([]*capture.Capture, 0, *samples)
	for range *samples {
		c, err := sample(sc, cert, flag.Args())
		if err != nil {
			return err
		}
		captured = append(captured, c)
	}
	c, err := capture.Combine(captured)
	if err != nil {
		// A client that varies where the browser does not is a finding
		// for the comparison, not a reason to stop: keep the first sample,
		// with the extension order its samples show.
		log.Printf("%s: %v", *name, err)
		c = captured[0]
		c.Profile.TLS.ExtensionOrder = profile.ExtensionOrderFixed
		for _, s := range captured[1:] {
			if !slices.EqualFunc(c.Profile.TLS.Extensions, s.Profile.TLS.Extensions, func(a, b profile.Extension) bool {
				return a.Type == b.Type
			}) {
				c.Profile.TLS.ExtensionOrder = profile.ExtensionOrderShuffled
			}
		}
	}
	c.Identify()
	// A client's capture is its profile and trace, whatever the server's
	// scenario.
	c.Scenario = ""
	c.Profile.Name = *name
	path, err := capture.Write(*out, c)
	if errors.Is(err, capture.ErrExists) {
		return fmt.Errorf("%w; delete it to capture again", err)
	}
	if err != nil {
		return err
	}
	log.Printf("%s: %s, extension order %s across %d samples, wrote %s", *name, c.Protocol, c.Profile.TLS.ExtensionOrder, *samples, path)
	return nil
}

// sample serves sc on a fresh port, runs the command with {url} replaced
// by the server's URL, and returns the first connection the client made a
// request on.
func sample(sc *server.Scenario, cert tls.Certificate, command []string) (*capture.Capture, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	captures := make(chan *capture.Capture, 64)
	srv := &server.Server{
		Scenario: sc,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
		},
		OnCapture: func(c *capture.Capture) {
			captures <- c
		},
		Logf: log.Printf,
	}
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- srv.Serve(ctx, ln)
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	args := make([]string, len(command))
	for i, a := range command {
		args[i] = strings.ReplaceAll(a, "{url}", "https://localhost:"+port)
	}
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...) //nolint:gosec // a dev tool running the command it was given
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmdErr := cmd.Run()
	// Stopping the server ends the client's connections and waits for
	// their handlers, so every trace is complete before it is read.
	stop()
	if err := <-served; err != nil {
		return nil, err
	}
	close(captures)
	if cmdErr != nil {
		return nil, fmt.Errorf("%s: %w", args[0], cmdErr)
	}
	var first *capture.Capture
	for c := range captures {
		switch {
		case !c.Complete():
			log.Printf("a connection sent no request (ALPN %q)", c.Protocol)
		case first == nil:
			first = c
		default:
			log.Printf("the client opened another connection; only the first is kept")
		}
	}
	if first == nil {
		return nil, errors.New("the client sent no request")
	}
	return first, nil
}
