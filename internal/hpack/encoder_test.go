package hpack

import (
	"encoding/hex"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	xhpack "golang.org/x/net/http2/hpack"

	"github.com/roachadam/mimicry/profile"
)

var policies = map[string]*profile.HPACK{
	"chromium default": nil,
	"firefox-like": {
		WithoutIndexing: []string{
			":path",
		},
		NeverIndexed: []string{
			"cookie",
		},
		LiteralOnExactMatch: true,
		NameMatch:           profile.NameMatchHighestIndex,
		Huffman:             profile.HuffmanAlways,
	},
	"safari-like": {
		WithoutIndexing: []string{
			":path",
			"content-length",
		},
		NeverIndexed: []string{
			"cookie",
		},
		NameMatch: profile.NameMatchStaticFirst,
		Huffman:   profile.HuffmanShorter,
	},
}

// A decoder must recover exactly what was encoded, block after block, while
// the table fills, evicts and is resized: any disagreement between the
// encoder's table and the one its representations imply shows up as a wrong
// field or a decode error. Never-indexed fields arrive marked sensitive.
func TestDecoderAgreesUnderEvictionAndResize(t *testing.T) {
	names := []string{":path", ":authority", "cookie", "accept", "user-agent", "content-length", "x-custom", "referer"}
	for name, policy := range policies {
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // seeded on purpose: deterministic test data
			enc := NewEncoder(policy)
			dec := xhpack.NewDecoder(maxTableSize, nil)
			for block := range 300 {
				// Resize now and then, including down and back up between two
				// blocks, which needs two size updates. The dip is to 0 because
				// x/net's decoder accepts a second update only after the first
				// emptied the table; TestSizeUpdates covers other dips.
				switch block % 50 {
				case 10:
					enc.SetPeerTableSize(256)
				case 20:
					enc.SetPeerTableSize(0)
					enc.SetPeerTableSize(8192)
				case 30:
					enc.SetPeerTableSize(defaultTableSize)
				}
				fields := make([]Field, 1+rng.IntN(8))
				for i := range fields {
					fields[i] = Field{
						Name:  names[rng.IntN(len(names))],
						Value: strconv.Itoa(rng.IntN(20)) + "{^}" + string("abcdefghijklmnopqrstuvwxyz"[rng.IntN(26)]),
					}
				}
				got, err := dec.DecodeFull(enc.AppendBlock(nil, fields))
				if err != nil {
					t.Fatalf("block %d: %v", block, err)
				}
				if len(got) != len(fields) {
					t.Fatalf("block %d: decoded %d fields, sent %d", block, len(got), len(fields))
				}
				for i, f := range fields {
					if got[i].Name != f.Name || got[i].Value != f.Value {
						t.Fatalf("block %d field %d: decoded %s: %s, sent %s: %s", block, i, got[i].Name, got[i].Value, f.Name, f.Value)
					}
					wantSensitive := policy != nil && f.Name == "cookie"
					if got[i].Sensitive != wantSensitive {
						t.Fatalf("block %d: %s sensitive %v, want %v", block, f.Name, got[i].Sensitive, wantSensitive)
					}
				}
			}
		})
	}
}

func TestSizeUpdates(t *testing.T) {
	firefoxAndSafari := &profile.HPACK{
		NameMatch:   profile.NameMatchStaticFirst,
		Huffman:     profile.HuffmanShorter,
		SizeUpdates: profile.SizeUpdatesAlways,
	}
	safari := &profile.HPACK{
		NameMatch:    profile.NameMatchStaticFirst,
		Huffman:      profile.HuffmanShorter,
		SizeUpdates:  profile.SizeUpdatesAlways,
		MaxTableSize: 4096,
	}
	tests := []struct {
		name   string
		policy *profile.HPACK
		sizes  []uint32
		want   string // hex prefix of the next block
	}{
		{
			name:  "unchanged default size emits nothing",
			sizes: []uint32{4096},
			want:  "82",
		},
		{
			// Captured: Firefox and Safari answer an explicit 4096.
			name:   "always: an unchanged size is signalled",
			policy: firefoxAndSafari,
			sizes:  []uint32{4096},
			want:   "3fe11f" + "82",
		},
		{
			// Captured: offered 64 KiB or 1 MiB, Safari announces 4096.
			name:   "a capped table announces its cap",
			policy: safari,
			sizes:  []uint32{1 << 20},
			want:   "3fe11f" + "82",
		},
		{
			name:  "larger size",
			sizes: []uint32{65536},
			want:  "3fe1ff03" + "82",
		},
		{
			name:  "beyond the table bound still announces the peer's value",
			sizes: []uint32{1048576},
			want:  "3fe1ff3f" + "82",
		},
		{
			name:  "down then up emits the minimum first",
			sizes: []uint32{2048, 8192},
			want:  "3fe10f" + "3fe13f" + "82",
		},
		{
			name:  "two increases emit only the final size",
			sizes: []uint32{8192, 16384},
			want:  "3fe17f" + "82",
		},
		{
			name:  "up then down emits only the final, smaller size",
			sizes: []uint32{16384, 8192},
			want:  "3fe13f" + "82",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(tt.policy)
			for _, s := range tt.sizes {
				enc.SetPeerTableSize(s)
			}
			block := enc.AppendBlock(nil, []Field{
				{
					Name:  ":method",
					Value: "GET",
				},
			})
			if got := hex.EncodeToString(block); got != tt.want {
				t.Errorf("block %s, want %s", got, tt.want)
			}
			// Size updates are emitted once.
			if got := hex.EncodeToString(enc.AppendBlock(nil, []Field{{Name: ":method", Value: "GET"}})); got != "82" {
				t.Errorf("second block %s, want 82", got)
			}
		})
	}
}

// Firefox and Safari never index cookie crumbs under 20 bytes and index
// longer ones (captured with 19-, 20- and 21-byte crumbs).
func TestNeverIndexBelow(t *testing.T) {
	enc := NewEncoder(&profile.HPACK{
		NeverIndexBelow: map[string]int{
			"cookie": 20,
		},
		NameMatch:   profile.NameMatchStaticFirst,
		Huffman:     profile.HuffmanShorter,
		SizeUpdates: profile.SizeUpdatesAlways,
	})
	for _, tt := range []struct {
		value string
		want  string // first bytes: representation and the static cookie name (32)
	}{
		{"p19=" + strings.Repeat("x", 15), "1f11"}, // never indexed
		{"p20=" + strings.Repeat("x", 16), "60"},   // incremental indexing
	} {
		got := hex.EncodeToString(enc.AppendBlock(nil, []Field{{Name: "cookie", Value: tt.value}}))
		if !strings.HasPrefix(got, tt.want) {
			t.Errorf("%d-byte crumb encoded %s, want prefix %s", len(tt.value), got, tt.want)
		}
	}
}
