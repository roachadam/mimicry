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
	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// captureRequests sends n requests, each on a new connection, with a mimicry
// client for p, and returns what the capture server recorded.
func captureRequests(t *testing.T, p *profile.Profile, n int) []*capture.Capture {
	t.Helper()
	cert, pool := testutil.Certificate(t, "localhost")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	captures := make(chan *capture.Capture, n)
	ctx, cancel := context.WithCancel(t.Context())
	srv := &server.Server{
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
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: tr}
	got := make([]*capture.Capture, 0, n)
	for range n {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost:"+port+"/", nil)
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
		// script to ask for /done), and the next request gets a fresh
		// connection and ClientHello.
		tr.CloseIdleConnections()
		got = append(got, <-captures)
	}
	return got
}

func loadCapture(t *testing.T, name string) *profile.Profile {
	t.Helper()
	data, err := os.ReadFile("../testdata/captures/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := profile.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Every built-in profile reproduces its browser's capture exactly, judged as
// the browsers were: three connections, extension order inferred from how
// they compare. A shuffled profile whose three orders happened to agree would
// be read as fixed and fail; for Chrome that chance is negligible.
func TestProfilesMatchCaptures(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
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
			for _, d := range diff.Compare(loadCapture(t, name), &got.Profile) {
				t.Error(d)
			}
		})
	}
}
