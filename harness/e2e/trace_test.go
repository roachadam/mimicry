package e2e

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/server"
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
			base, tr, captures := captureClient(t, p, server.DefaultScenario)
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
			got := h2trace.Extract((<-captures).Trace)

			exactFeatures := []string{
				// The first request's header block, instruction by
				// instruction; values are left out.
				"request[/].hpack",
				// Firefox's per-stream top-up (absent for the others).
				"request[/].window_top_up",
			}
			const frame = 16384
			var browserMaxData uint32
			// Every credit increment mimicry sends must be one the browser
			// itself sends, within a frame: credit is returned when a
			// threshold is crossed, so increments depend on where DATA frames
			// fell and on how often the browser checks, which varies between
			// connections (Safari's connection credit overshoots by up to
			// 1.2 MB, Firefox's stream credit follows the page's reads). A
			// wrong policy (64 KB or 4 MB steps the browser never takes)
			// falls outside.
			low, high := map[string]uint32{}, map[string]uint32{}
			for i, want := range loadTraces(t, name) {
				w := h2trace.Extract(want)
				for _, f := range exactFeatures {
					if w.Exact[f] != got.Exact[f] {
						t.Errorf("sample %d: %s\n got %s\nwant %s", i+1, f, got.Exact[f], w.Exact[f])
					}
				}
				browserMaxData = max(browserMaxData, w.Measured["data_max[/upload]"])
				for where, incs := range w.Credit {
					for _, inc := range incs {
						if l, ok := low[where]; !ok || inc < l {
							low[where] = inc
						}
						high[where] = max(high[where], inc)
					}
				}
			}
			if g := got.Measured["data_max[/upload]"]; g != browserMaxData {
				t.Errorf("largest upload DATA frame %d, want %d", g, browserMaxData)
			}
			for _, where := range []string{"conn", "/big"} {
				for _, inc := range got.Credit[where] {
					if inc+frame < low[where] || inc > high[where]+frame {
						t.Errorf("%s credit increment %d, outside the browser's %d..%d (± one frame)", where, inc, low[where], high[where])
					}
				}
			}
		})
	}
}
