package h2trace

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/http2/hpack"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The examples of RFC 7541, appendix C, decoded on one connection where the
// RFC uses one.
func TestDecodeRFC7541Examples(t *testing.T) {
	tests := []struct {
		name   string
		blocks []string
		want   [][]Instruction
	}{
		{
			name: "C.2.2 literal without indexing",
			blocks: []string{
				"040c 2f73 616d 706c 652f 7061 7468",
			},
			want: [][]Instruction{
				{
					{
						Rep:   Without,
						Index: 4,
						Name:  ":path",
						Value: "/sample/path",
					},
				},
			},
		},
		{
			name: "C.2.3 literal never indexed",
			blocks: []string{
				"1008 7061 7373 776f 7264 0673 6563 7265 74",
			},
			want: [][]Instruction{
				{
					{
						Rep:   Never,
						Name:  "password",
						Value: "secret",
					},
				},
			},
		},
		{
			name: "C.4 requests with Huffman, dynamic table reuse",
			blocks: []string{
				"8286 8441 8cf1 e3c2 e5f2 3a6b a0ab 90f4 ff",
				"8286 84be 5886 a8eb 1064 9cbf",
			},
			want: [][]Instruction{
				{
					{
						Rep:   Indexed,
						Index: 2,
						Name:  ":method",
						Value: "GET",
					},
					{
						Rep:   Indexed,
						Index: 6,
						Name:  ":scheme",
						Value: "http",
					},
					{
						Rep:   Indexed,
						Index: 4,
						Name:  ":path",
						Value: "/",
					},
					{
						Rep:          Incremental,
						Index:        1,
						Name:         ":authority",
						Value:        "www.example.com",
						HuffmanValue: true,
					},
				},
				{
					{
						Rep:   Indexed,
						Index: 2,
						Name:  ":method",
						Value: "GET",
					},
					{
						Rep:   Indexed,
						Index: 6,
						Name:  ":scheme",
						Value: "http",
					},
					{
						Rep:   Indexed,
						Index: 4,
						Name:  ":path",
						Value: "/",
					},
					{
						Rep:   Indexed,
						Index: 62,
						Name:  ":authority",
						Value: "www.example.com",
					},
					{
						Rep:          Incremental,
						Index:        24,
						Name:         "cache-control",
						Value:        "no-cache",
						HuffmanValue: true,
					},
				},
			},
		},
		{
			name: "size update (multi-byte integer) then a field",
			blocks: []string{
				"3fe11f 82",
			},
			want: [][]Instruction{
				{
					{
						Rep:  SizeUpdate,
						Size: 4096,
					},
					{
						Rep:   Indexed,
						Index: 2,
						Name:  ":method",
						Value: "GET",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec := hpack.NewDecoder(4096, nil)
			for i, block := range tt.blocks {
				got, err := Decode(dec, unhex(t, block))
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, tt.want[i]) {
					t.Errorf("block %d\n got %+v\nwant %+v", i+1, got, tt.want[i])
				}
			}
		})
	}
}

func TestDecodeRejectsTruncated(t *testing.T) {
	for _, block := range []string{
		"040c 2f73", // string shorter than its length
		"3fe1",      // integer continues past the end
		"10",        // never-indexed literal with no name string
	} {
		if _, err := Decode(hpack.NewDecoder(4096, nil), unhex(t, block)); err == nil {
			t.Errorf("%s: decoded a truncated block", block)
		}
	}
}
