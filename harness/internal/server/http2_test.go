package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/testutil"
)

// A client that walks the page's requests the way its script does gets every
// response in full (the 8 MiB download only finishes if the server honours
// the client's flow control), and the capture handed over at /done holds a
// trace of every request with its decoded header block.
func TestPageFlowTrace(t *testing.T) {
	cert, pool := testutil.Certificate(t, "localhost")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	captures := make(chan *capture.Capture, 4)
	ctx, cancel := context.WithCancel(t.Context())
	srv := &Server{
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
	client := &http.Client{Transport: tr}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	base := "https://localhost:" + port

	fetch := func(method, path string, body []byte, wantLen int) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %s %s", path, resp.Proto, resp.Status)
		}
		if wantLen >= 0 && len(b) != wantLen {
			t.Fatalf("%s: %d bytes, want %d", path, len(b), wantLen)
		}
		if path == "/upload" && string(b) != strconv.Itoa(len(body))+"\n" {
			t.Fatalf("server counted %q of a %d-byte upload", b, len(body))
		}
	}
	fetch(http.MethodGet, "/", nil, -1)
	fetch(http.MethodGet, "/style.css", nil, -1)
	fetch(http.MethodGet, "/big", nil, bigSize)
	fetch(http.MethodPost, "/upload", make([]byte, 200_000), -1)
	fetch(http.MethodGet, "/done", nil, -1)

	c := <-captures
	if c.Trace == nil {
		t.Fatal("no trace")
	}
	paths := make([]string, 0, len(c.Trace.Requests))
	for _, r := range c.Trace.Requests {
		paths = append(paths, r.Path)
		if len(r.Instructions) == 0 || r.Block == "" {
			t.Errorf("%s: no header block recorded", r.Path)
		}
	}
	if want := []string{"/", "/style.css", "/big", "/upload", "/done"}; !slices.Equal(paths, want) {
		t.Errorf("traced requests %v, want %v", paths, want)
	}
	// The 200 KB upload goes out as several DATA frames, all traced.
	var uploaded uint32
	for _, f := range c.Trace.Frames {
		if f.Type == "DATA" {
			uploaded += f.Length
		}
	}
	if uploaded != 200_000 {
		t.Errorf("traced %d bytes of DATA, want 200000", uploaded)
	}
	// The first request's headers became the profile's, as before.
	if c.Profile.HTTP2 == nil || len(c.Profile.Headers) == 0 || len(c.Profile.HTTP2.PseudoHeaderOrder) != 4 {
		t.Errorf("first request not recorded into the profile: %+v", c.Profile.HTTP2)
	}
}
