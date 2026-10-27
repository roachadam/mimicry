// Package e2e checks mimicry against real browser captures: each built-in
// profile, sent through mimicry to the capture server, must be
// indistinguishable from the browser it came from.
package e2e

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
)

// captureClient runs a capture server answering as sc and returns its base
// URL, a mimicry transport for p trusting it, and the channel its captures
// arrive on.
func captureClient(t *testing.T, p *profile.Profile, sc *server.Scenario) (string, *mimicry.Transport, <-chan *capture.Capture) {
	t.Helper()
	return captureClientWith(t, p, sc, nil)
}

// captureClientWith is captureClient with jar as the transport's cookie
// jar (nil: none).
func captureClientWith(t *testing.T, p *profile.Profile, sc *server.Scenario, jar *cookies.Jar) (string, *mimicry.Transport, <-chan *capture.Capture) {
	t.Helper()
	cert, pool := testutil.Certificate(t, "localhost")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	captures := make(chan *capture.Capture, 8)
	ctx, cancel := context.WithCancel(t.Context())
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
		Logf: t.Logf,
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	tr, err := mimicry.NewTransport(mimicry.Config{
		Profile: p,
		RootCAs: pool,
		Cookies: jar,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	return "https://localhost:" + port, tr, captures
}

// newJar returns an empty cookie jar with p's cookie rules.
func newJar(t *testing.T, p *profile.Profile) *cookies.Jar {
	t.Helper()
	j, err := cookies.New(cookies.Config{
		Rules: p.Cookies,
	})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// captureRequests sends n requests, each from a fresh mimicry client for p
// to a fresh server, as the browser samples were fresh launches: every one
// a full handshake, none resuming another's session. It returns what the
// capture server recorded.
func captureRequests(t *testing.T, p *profile.Profile, n int) []*capture.Capture {
	t.Helper()
	got := make([]*capture.Capture, 0, n)
	for range n {
		base, tr, captures := captureClient(t, p, server.DefaultScenario)
		client := &http.Client{Transport: tr}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		// Closing the connection completes the capture (mimicry runs no page
		// script to ask for /done).
		tr.CloseIdleConnections()
		got = append(got, <-captures)
	}
	return got
}

// loadCapture reads the default page's capture p is checked against.
func loadCapture(t *testing.T, p *profile.Profile) *profile.Profile {
	t.Helper()
	path, _, _ := capturePath(t, p, capture.DefaultScenario, ".json")
	data, err := os.ReadFile(path) //nolint:gosec // a capture file
	if err != nil {
		t.Fatal(err)
	}
	c, err := profile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Every built-in profile reproduces its browser's capture exactly, judged as
// the browsers were: three connections, extension order inferred from how
// they compare. A shuffled profile whose three orders happened to agree would
// be read as fixed and fail; for Chrome that chance is negligible.
func TestProfilesMatchCaptures(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			samples := captureRequests(t, p, 3)
			for i, c := range samples {
				if c.Protocol != "h2" {
					t.Errorf("connection %d negotiated %q, want h2", i+1, c.Protocol)
				}
			}
			got, err := capture.Combine(samples)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range diff.Compare(loadCapture(t, p), &got.Profile) {
				t.Error(d)
			}
		})
	}
}
