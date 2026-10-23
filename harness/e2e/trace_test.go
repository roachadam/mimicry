package e2e

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profiles"
)

// On one connection, mimicry encodes its first request, cuts its upload into
// DATA frames and returns download credit as the browser did on the capture
// page, in every sampled connection. Only what does not depend on the kind of
// request is compared: the browser's subresource requests carry headers a
// navigation profile does not.
func TestConnectionMatchesBrowserTrace(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			base, tr, captures := captureClient(t, p)
			client := &http.Client{Transport: tr}
			send := func(method, path string, body []byte) {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), method, base+path, bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				if body == nil {
					req.Body = http.NoBody
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			send(http.MethodGet, "/", nil)
			send(http.MethodPost, "/upload", make([]byte, 200_000))
			send(http.MethodGet, "/big", nil)
			tr.CloseIdleConnections()
			gotExact, gotMeasured := h2trace.Features((<-captures).Trace)

			exactFeatures := []string{
				// The first request's header block, instruction by
				// instruction; values are left out.
				"request[/].hpack",
				// Firefox's per-stream top-up (absent for the others).
				"request[/].window_top_up",
			}
			measuredFeatures := []string{
				"data_max[/upload]",
				"window_update_median[conn]",
			}
			// Firefox's stream updates follow how fast the page read the
			// body (16 KiB to 3 MiB in its traces) and are not modelled: a
			// known gap.
			if name != "firefox-158-macos" {
				measuredFeatures = append(measuredFeatures, "window_update_median[/big]")
			}
			for i, want := range loadTraces(t, name) {
				wantExact, wantMeasured := h2trace.Features(want)
				for _, f := range exactFeatures {
					if wantExact[f] != gotExact[f] {
						t.Errorf("sample %d: %s\n got %s\nwant %s", i+1, f, gotExact[f], wantExact[f])
					}
				}
				for _, f := range measuredFeatures {
					w, g := wantMeasured[f], gotMeasured[f]
					if g+h2trace.Slack(w) < w || g > w+h2trace.Slack(w) {
						t.Errorf("sample %d: %s %d, want %d±%d", i+1, f, g, w, h2trace.Slack(w))
					}
				}
			}
		})
	}
}
