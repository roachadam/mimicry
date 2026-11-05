package e2e

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// On one connection, mimicry's first header block, upload DATA cuts and
// download credit match every sample, and its opening and first request
// are cut into TLS records as in every captured connection whose request
// was waiting as it opened (http2.openingRecords). Only kind-independent
// parts are compared.
func TestConnectionMatchesBrowserTrace(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}

			base, tr, captures := captureClient(t, p, server.DefaultScenario)
			client := &http.Client{Transport: tr}

			page, err := url.Parse(base + "/")
			if err != nil {
				t.Fatal(err)
			}

			// As the capture page: a navigation to /, then its script's
			// fetches of /big and /upload.
			send := func(method, path string, body []byte, r mimicry.Request) {
				t.Helper()

				req, err := http.NewRequestWithContext(t.Context(), method, base+path, bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}

				if body == nil {
					req.Body = http.NoBody
				}

				resp, err := client.Do(mimicry.With(req, r))
				if err != nil {
					t.Fatal(err)
				}

				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			fetch := mimicry.Request{
				Kind: mimicry.Fetch,
				From: page,
			}

			send(http.MethodGet, "/", nil, mimicry.Request{})
			send(http.MethodGet, "/big", nil, fetch)
			send(http.MethodPost, "/upload", make([]byte, 200_000), fetch)
			tr.CloseIdleConnections()

			c := <-captures
			got := h2trace.Extract(c.Trace)

			wantOpening, err := capture.OpeningRecords(captureDir, p)
			if err != nil {
				t.Fatal(err)
			}

			gotOpening, _, err := h2trace.Opening(c.Trace)
			if err != nil {
				t.Fatal(err)
			}

			if wantOpening != nil && !slices.EqualFunc(gotOpening, wantOpening, slices.Equal) {
				t.Errorf("opening TLS records\n got %v\nwant %v", gotOpening, wantOpening)
			}

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

			wantTraces, err := loadH2Traces(t, p, capture.DefaultScenario)
			if err != nil {
				t.Fatal(err)
			}

			for i, want := range wantTraces {
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
				gap := creditGaps[capture.Source(p, capture.DefaultScenario)+" "+where]
				outside := false

				for _, inc := range got.Credit[where] {
					if inc+frame >= low[where] && inc <= high[where]+frame {
						continue
					}

					outside = true

					if gap == "" {
						t.Errorf("%s credit increment %d, outside the browser's %d..%d (± one frame)", where, inc, low[where], high[where])
					} else {
						t.Logf("known gap: %s credit increment %d, outside the browser's %d..%d: %s", where, inc, low[where], high[where], gap)
					}
				}

				if gap != "" && !outside {
					t.Errorf("known gap %q no longer appears", gap)
				}
			}
		})
	}
}

// creditGaps are credit increments mimicry sends outside the browser's
// range, by "<profile> <where>" (the profile that took the capture), with
// why: logged, not failed. A listed gap that no longer appears fails.
var creditGaps = map[string]string{
	"firefox-158-windows /big": "Firefox returns /big's stream credit as the page reads it: its largest increment was 3.56 MB in 3 Windows samples (with 16–557 KB steps between), while 2 of 3 macOS samples of the same Firefox sent 4 MiB, mimicry's step; how fast the page reads is the machine's, not a rule",
	"firefox-159-macos /big":   "as firefox-158-windows's: Firefox returns /big's stream credit as the page reads it; its largest increment was 2.36 MB in 3 macOS samples of Firefox 159 (Firefox 158 on macOS: 4 MiB in 2 of 3), mimicry's step 4 MiB; how fast the page reads is the machine's, not a rule",
	"firefox-159-windows /big": "as firefox-158-windows's: its largest increment was 2.67 MB in 3 Windows samples of Firefox 159, mimicry's step 4 MiB",
	"firefox-159-linux /big":   "as firefox-158-windows's: its largest increment was 3.16 MB in 3 Linux samples of Firefox 159 (2.90 and 0.80 MB in the other two, each followed by 16–492 KB steps), mimicry's step 4 MiB",
}
