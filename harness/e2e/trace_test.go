package e2e

import (
	"bytes"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/profiles"
)

// frameSlack is the least a WINDOW_UPDATE may differ from the browser's:
// credit is returned once a threshold is crossed, so an increment depends on
// where the server's DATA frames (up to 16384 bytes) fell and, for Safari, on
// how many frames arrived between its checks (up to five, measured).
const frameSlack = 16384

// On one connection, mimicry encodes its first request, cuts its upload into
// DATA frames and returns download credit as the browser did on the capture
// page. Only what does not depend on the kind of request is compared: the
// browser's subresource requests carry headers a navigation profile does not.
func TestConnectionMatchesBrowserTrace(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			want := loadTrace(t, name)
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
			got := (<-captures).Trace

			// The first request's header block, instruction by instruction.
			// Values are left out: :authority differs (localhost here).
			if w, g := shapes(want.Requests[0]), shapes(got.Requests[0]); !slices.Equal(w, g) {
				t.Errorf("first request's HPACK instructions\n got %v\nwant %v", g, w)
			}

			// The largest DATA frame of the upload.
			if w, g := maxData(want, "/upload"), maxData(got, "/upload"); w != g {
				t.Errorf("largest upload DATA frame %d bytes, want %d", g, w)
			}

			// Typical WINDOW_UPDATE increments while downloading. Firefox's
			// stream updates follow how fast the page read the body (16 KiB to
			// 3 MiB in the trace) and are not modelled: a known gap.
			if name != "firefox-158-macos" {
				if w, g := median(windowUpdates(want, "/big")), median(windowUpdates(got, "/big")); !near(w, g) {
					t.Errorf("stream WINDOW_UPDATE %d, want %d±%d", g, w, slack(w))
				}
			}
			if w, g := median(windowUpdates(want, "")), median(windowUpdates(got, "")); !near(w, g) {
				t.Errorf("connection WINDOW_UPDATE %d, want %d±%d", g, w, slack(w))
			}
		})
	}
}

// shape is an instruction without its value.
type shape struct {
	Rep          h2trace.Representation
	Index        uint64
	Name         string
	HuffmanName  bool
	HuffmanValue bool
}

func shapes(r h2trace.Request) []shape {
	out := make([]shape, len(r.Instructions))
	for i, ins := range r.Instructions {
		out[i] = shape{
			Rep:          ins.Rep,
			Index:        ins.Index,
			Name:         ins.Name,
			HuffmanName:  ins.HuffmanName,
			HuffmanValue: ins.HuffmanValue,
		}
	}
	return out
}

func streamOf(tr *h2trace.Trace, path string) uint32 {
	for _, r := range tr.Requests {
		if r.Path == path {
			return r.Stream
		}
	}
	return 0
}

func maxData(tr *h2trace.Trace, path string) uint32 {
	id := streamOf(tr, path)
	var m uint32
	for _, f := range tr.Frames {
		if f.Type == "DATA" && f.Stream == id {
			m = max(m, f.Length)
		}
	}
	return m
}

// windowUpdates lists the WINDOW_UPDATE increments on path's stream, or on
// the connection when path is empty, after the first request: the preface's
// update and per-stream top-ups sent with HEADERS are not credit returns.
func windowUpdates(tr *h2trace.Trace, path string) []uint32 {
	id := streamOf(tr, path)
	var out []uint32
	afterHeaders := false
	started := false
	for _, f := range tr.Frames {
		if f.Type == "HEADERS" {
			started = true
			afterHeaders = true
			continue
		}
		if f.Type == "WINDOW_UPDATE" && started && !afterHeaders && f.Stream == id {
			out = append(out, f.Increment)
		}
		afterHeaders = false
	}
	return out
}

func median(v []uint32) uint32 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	return s[len(s)/2]
}

// slack is how far an increment may stray from want: 2% (five frames of a
// 5 MiB increment) and at least one frame.
func slack(want uint32) uint32 {
	return max(frameSlack, want/50)
}

func near(want, got uint32) bool {
	return got+slack(want) >= want && got <= want+slack(want)
}
