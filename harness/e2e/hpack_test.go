package e2e

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/h2trace"
	"github.com/roachadam/mimicry/internal/hpack"
	"github.com/roachadam/mimicry/profiles"
)

func loadTrace(t *testing.T, name string) *h2trace.Trace {
	t.Helper()
	data, err := os.ReadFile("../testdata/captures/" + name + ".h2trace.json")
	if err != nil {
		t.Fatal(err)
	}
	var tr h2trace.Trace
	if err := json.Unmarshal(data, &tr); err != nil {
		t.Fatal(err)
	}
	return &tr
}

// Replaying a browser's requests through mimicry's encoder with the built-in
// profile's HPACK policy reproduces every header block the browser sent, byte
// for byte.
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
			tr := loadTrace(t, name)
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
}
