package e2e

import (
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/hpack"
	"github.com/roachadam/mimicry/profiles"
)

// loadTraces returns every sample's trace for the named capture.
func loadTraces(t *testing.T, name string) []*h2trace.Trace {
	t.Helper()
	traces, err := h2trace.Load("../testdata/captures/" + name + ".h2trace.json")
	if err != nil {
		t.Fatal(err)
	}
	return traces
}

// Replaying a browser's requests through mimicry's encoder with the built-in
// profile's HPACK policy reproduces every header block the browser sent, byte
// for byte, on every sampled connection.
func TestHPACKReproducesBrowserBlocks(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			if p.HTTP2.HPACK == nil {
				t.Fatal("built-in profile has no HPACK policy")
			}
			for i, tr := range loadTraces(t, name) {
				t.Run("sample "+strconv.Itoa(i+1), func(t *testing.T) {
					enc := hpack.NewEncoder(p.HTTP2.HPACK)
					for _, r := range tr.Requests {
						var fields []hpack.Field
						for _, ins := range r.Instructions {
							if ins.Rep != h2trace.SizeUpdate {
								fields = append(fields, hpack.Field{
									Name:  ins.Name,
									Value: ins.Value,
								})
							}
						}
						got := hex.EncodeToString(enc.AppendBlock(nil, fields))
						if got != r.Block {
							t.Fatalf("%s (stream %d):\n got %s\nwant %s", r.Path, r.Stream, got, r.Block)
						}
					}
				})
			}
		})
	}
}
