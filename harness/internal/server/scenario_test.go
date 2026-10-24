package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/testutil"
)

// scenarioClient serves sc and returns a Go HTTP/2 client for it, its base
// URL, and the capture channel.
func scenarioClient(t *testing.T, sc *Scenario) (*http.Client, string, <-chan *capture.Capture) {
	t.Helper()
	cert, pool := testutil.Certificate(t, "localhost")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	captures := make(chan *capture.Capture, 4)
	ctx, cancel := context.WithCancel(t.Context())
	srv := &Server{
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
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: pool,
		},
		ForceAttemptHTTP2: true,
	}
	t.Cleanup(tr.CloseIdleConnections)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return &http.Client{Transport: tr}, "https://localhost:" + port, captures
}

func do(t *testing.T, client *http.Client, method, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

// The set-cookie scenario sets its real-world-shaped jar on the first
// navigation and deletes all of it at /done, so a browser that cannot be
// isolated keeps nothing.
func TestSetCookieScenario(t *testing.T) {
	sc, err := ScenarioByName("set-cookie")
	if err != nil {
		t.Fatal(err)
	}
	client, base, captures := scenarioClient(t, sc)
	set := do(t, client, http.MethodGet, base+"/", nil).Header.Values("Set-Cookie")
	if len(set) != len(serverCookies) {
		t.Fatalf("%d Set-Cookie headers, want %d", len(set), len(serverCookies))
	}
	if !strings.HasPrefix(set[5], "_abck=") || len(strings.SplitN(strings.TrimPrefix(set[5], "_abck="), ";", 2)[0]) != 480 {
		t.Errorf("no 480-character _abck cookie: %q", set[5][:min(len(set[5]), 40)])
	}
	for _, c := range do(t, client, http.MethodGet, base+"/done", nil).Header.Values("Set-Cookie") {
		if !strings.Contains(c, "Max-Age=0") {
			t.Errorf("/done does not delete %q", c)
		}
	}
	if c := <-captures; c.Scenario != "set-cookie" || !c.Finished {
		t.Errorf("capture scenario %q finished %v", c.Scenario, c.Finished)
	}
}

// A scenario's SETTINGS reach the client: under large-settings, Go's client
// uploads in frames above the default 16 KiB.
func TestScenarioSettingsAreAdvertised(t *testing.T) {
	sc, err := ScenarioByName("large-settings")
	if err != nil {
		t.Fatal(err)
	}
	client, base, captures := scenarioClient(t, sc)
	// A first request lets the server's SETTINGS arrive before the upload.
	do(t, client, http.MethodGet, base+"/", nil)
	do(t, client, http.MethodPost, base+"/upload", make([]byte, 200_000))
	do(t, client, http.MethodGet, base+"/done", nil)
	c := <-captures
	var largest uint32
	for _, f := range c.Trace.Frames {
		if f.Type == "DATA" {
			largest = max(largest, f.Length)
		}
	}
	if largest <= 16384 {
		t.Fatalf("largest DATA frame %d: the client did not see the advertised 64 KiB frame size", largest)
	}
}
