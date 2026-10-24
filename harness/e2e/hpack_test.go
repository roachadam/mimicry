package e2e

import (
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	xhpack "golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/harness/internal/server"
	"github.com/roachadam/mimicry/internal/hpack"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

const captureDir = "../testdata/captures/"

// loadTraces returns every sample's trace for the named capture's default
// scenario.
func loadTraces(t *testing.T, name string) []*h2trace.Trace {
	t.Helper()
	traces, err := h2trace.Load(captureDir + name + ".h2trace.json")
	if err != nil {
		t.Fatal(err)
	}
	return traces
}

// scenarioTraces returns every captured scenario's traces for the named
// capture, by scenario.
func scenarioTraces(t *testing.T, name string) map[*server.Scenario][]*h2trace.Trace {
	t.Helper()
	files, err := filepath.Glob(captureDir + name + ".*h2trace.json")
	if err != nil {
		t.Fatal(err)
	}
	out := map[*server.Scenario][]*h2trace.Trace{}
	for _, f := range files {
		// <name>.h2trace.json or <name>.<scenario>.h2trace.json
		scenario := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), name+"."), "h2trace.json")
		sc := server.DefaultScenario
		if scenario = strings.TrimSuffix(scenario, "."); scenario != "" {
			if sc, err = server.ScenarioByName(scenario); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
		}
		traces, err := h2trace.Load(f)
		if err != nil {
			t.Fatal(err)
		}
		out[sc] = traces
	}
	return out
}

// Replaying a browser's requests through mimicry's encoder with the built-in
// profile's HPACK policy reproduces every header block the browser sent, byte
// for byte, on every sampled connection of every scenario, and on every
// connection of every replayed real page (hundreds of requests, kilobytes of
// cookies, a full table). The server's header table size reaches the encoder
// where the browser acknowledged the server's SETTINGS.
func TestHPACKReproducesBrowserBlocks(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			for sc, traces := range scenarioTraces(t, name) {
				for i, tr := range traces {
					t.Run(sc.Name+" sample "+strconv.Itoa(i+1), func(t *testing.T) {
						replay(t, p.HTTP2.HPACK, sc, tr)
					})
				}
			}
			for scenario, samples := range pageTraces(t, name) {
				for i, page := range samples {
					for k, tr := range page.H2 {
						t.Run(scenario+" sample "+strconv.Itoa(i+1)+" "+tr.Host+" "+strconv.Itoa(k), func(t *testing.T) {
							// Replayed pages are served without SETTINGS.
							replay(t, p.HTTP2.HPACK, &server.Scenario{}, tr)
						})
					}
				}
			}
		})
	}
}

// pageTraces returns every replayed page captured for the named capture, by
// scenario.
func pageTraces(t *testing.T, name string) map[string][]*capture.Page {
	t.Helper()
	files, err := filepath.Glob(captureDir + name + ".*.pagetrace.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]*capture.Page{}
	for _, f := range files {
		pages, err := capture.LoadPages(f)
		if err != nil {
			t.Fatal(err)
		}
		scenario := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), name+"."), ".pagetrace.json.gz")
		out[scenario] = pages
	}
	return out
}

// replay re-encodes tr's header blocks in order. The table size a scenario's
// server advertised reaches the encoder where the browser acknowledged the
// server's SETTINGS, and only if the server sent one, as in the client. A
// block that differs is reported by its first differing instruction.
func replay(t *testing.T, policy *profile.HPACK, sc *server.Scenario, tr *h2trace.Trace) {
	t.Helper()
	enc := hpack.NewEncoder(policy)
	tableSize, advertised := sc.Setting(http2.SettingHeaderTableSize)
	// Decodes our own blocks, to say how one differs.
	dec := xhpack.NewDecoder(4096, nil)
	dec.SetAllowedMaxDynamicTableSize(max(4096, tableSize))
	next := 0
	for _, f := range tr.Frames {
		switch {
		case f.Type == "SETTINGS" && f.Flags&0x1 != 0 && advertised:
			enc.SetPeerTableSize(tableSize)
		case f.Type == "HEADERS":
			r := tr.Requests[next]
			next++
			var fields []hpack.Field
			for _, ins := range r.Instructions {
				if ins.Rep != h2trace.SizeUpdate {
					fields = append(fields, hpack.Field{
						Name:  ins.Name,
						Value: ins.Value,
					})
				}
			}
			block := enc.AppendBlock(nil, fields)
			got, err := h2trace.Decode(dec, block)
			if err != nil {
				t.Fatalf("%s (stream %d): decoding our block: %v", r.Path, r.Stream, err)
			}
			if hex.EncodeToString(block) == r.Block {
				continue
			}
			for i := range max(len(got), len(r.Instructions)) {
				var g, w h2trace.Instruction
				if i < len(got) {
					g = got[i]
				}
				if i < len(r.Instructions) {
					w = r.Instructions[i]
				}
				if g != w {
					t.Fatalf("%s (stream %d), instruction %d of %d:\n got %+v\nwant %+v", r.Path, r.Stream, i, len(r.Instructions), g, w)
				}
			}
			t.Fatalf("%s (stream %d): same instructions, different bytes:\n got %x\nwant %s", r.Path, r.Stream, block, r.Block)
		}
	}
}
