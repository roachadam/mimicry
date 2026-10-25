package qpack

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// A hostile server's encoder stream or field section is an error, never a
// panic or an entry it did not insert: indexes out of range, entries
// evicted or not yet inserted, a capacity above what was advertised,
// truncated and oversized literals, bad prefixes.
func TestDecoderRejectsHostileInput(t *testing.T) {
	insertFoo := appendString(appendString(nil, 0x40, 5, "foo", false), 0x00, 7, "bar", false)
	tests := []struct {
		name    string
		encoder []byte
		section []byte
		want    error
	}{
		{
			name:    "static index past the table",
			section: []byte{0x00, 0x00, 0xff, 0x40},
			want:    ErrDecompression,
		},
		{
			name:    "dynamic index without inserts",
			section: []byte{0x02, 0x00, 0x80},
			want:    ErrBlocked,
		},
		{
			name:    "relative index past the base",
			encoder: slices.Concat([]byte{0x3f, 0xe1, 0x1f}, insertFoo),
			section: []byte{0x02, 0x00, 0x81},
			want:    ErrDecompression,
		},
		{
			name:    "post-base index past the required insert count",
			encoder: slices.Concat([]byte{0x3f, 0xe1, 0x1f}, insertFoo),
			section: []byte{0x02, 0x80, 0x11},
			want:    ErrDecompression,
		},
		{
			name:    "encoded insert count past the full range",
			section: []byte{0xff, 0x7f, 0x00},
			want:    ErrDecompression,
		},
		{
			name:    "delta base below zero",
			encoder: slices.Concat([]byte{0x3f, 0xe1, 0x1f}, insertFoo),
			section: []byte{0x02, 0xff, 0x05},
			want:    ErrDecompression,
		},
		{
			name:    "truncated literal",
			section: []byte{0x00, 0x00, 0x27, 0x05, 'a'},
			want:    ErrDecompression,
		},
		{
			name:    "truncated prefix",
			section: []byte{0x00},
			want:    ErrDecompression,
		},
		{
			name:    "capacity above the maximum",
			encoder: []byte{0x3f, 0xe2, 0x1f},
			want:    ErrEncoderStream,
		},
		{
			name:    "insert with no capacity",
			encoder: insertFoo,
			want:    ErrEncoderStream,
		},
		{
			name:    "insert naming an entry never inserted",
			encoder: []byte{0x3f, 0xe1, 0x1f, 0x80, 0x01, 'x'},
			want:    ErrEncoderStream,
		},
		{
			name:    "duplicate of an evicted entry",
			encoder: slices.Concat([]byte{0x3f, 0x09}, appendString(appendString(nil, 0x40, 5, "a", false), 0x00, 7, "b", false), appendString(appendString(nil, 0x40, 5, "c", false), 0x00, 7, "d", false), []byte{0x01}),
			want:    ErrEncoderStream,
		},
		{
			name:    "literal longer than the limit",
			encoder: slices.Concat([]byte{0x3f, 0xe1, 0x1f, 0x5f}, appendInt(nil, 0, 0, 1<<21)[1:]),
			want:    ErrEncoderStream,
		},
		{
			name:    "static name index past the table",
			encoder: []byte{0x3f, 0xe1, 0x1f, 0xff, 0x40, 0x00},
			want:    ErrEncoderStream,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(4096)
			err := d.OnEncoderStream(tc.encoder)
			if err == nil && tc.section != nil {
				_, _, err = d.Decode(tc.section)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// Under any table pressure (a table far smaller than the fields, every
// section unacknowledged or acknowledged as it goes, few or no blocked
// streams allowed) every section the encoder writes decodes, with the
// encoder stream it wrote, to the fields given: it never evicts an entry
// a section or the peer may still need, and never refers to one it may
// not.
func TestEncoderOutputAlwaysDecodes(t *testing.T) {
	requests := make([][]Field, 0, 40)
	for i := range 40 {
		requests = append(requests, []Field{
			{Name: fmt.Sprintf("x-name-%d", i), Value: "v"},
			{Name: ":method", Value: "GET"},
			{Name: ":authority", Value: "www.example.test"},
			{Name: ":path", Value: fmt.Sprintf("/r/%d", i%7)},
			{Name: "user-agent", Value: strings.Repeat("u", 60)},
			{Name: "x-request", Value: fmt.Sprintf("%d", i)},
			{Name: "x-big", Value: strings.Repeat("b", 10*(i%5))},
			{Name: "cookie", Value: fmt.Sprintf("c=%d", i%3)},
		})
	}
	for _, strategy := range []Strategy{StrategyIndexAll, StrategyNewNames, StrategyStatic} {
		for _, capacity := range []uint64{0, 64, 220, 500, 4096} {
			for _, blocked := range []uint64{0, 1, 100} {
				for _, ack := range []bool{false, true} {
					name := fmt.Sprintf("strategy %d capacity %d blocked %d ack %v", strategy, capacity, blocked, ack)
					t.Run(name, func(t *testing.T) {
						e := NewEncoder(Config{
							Strategy:    strategy,
							MaxCapacity: 1 << 16,
						})
						e.SetPeerSettings(capacity, blocked)
						d := NewDecoder(capacity)
						// Unacknowledged sections are decoded only at the end,
						// after every later insert: what they refer to must
						// still be there.
						var late [][]byte
						for i, fields := range requests {
							stream := uint64(4 * i)
							ins, block := e.Encode(stream, fields)
							if err := d.OnEncoderStream(ins); err != nil {
								t.Fatalf("request %d: encoder stream: %v", i, err)
							}
							if !ack {
								late = append(late, block)
								continue
							}
							if decodes(t, d, i, block, fields) > 0 {
								if err := e.OnDecoderStream(AppendSectionAck(nil, stream)); err != nil {
									t.Fatal(err)
								}
							}
						}
						for i, block := range late {
							decodes(t, d, i, block, requests[i])
						}
					})
				}
			}
		}
	}
}

// decodes decodes request i's section and returns its Required Insert
// Count.
func decodes(t *testing.T, d *Decoder, i int, block []byte, want []Field) uint64 {
	t.Helper()
	got, ric, err := d.Decode(block)
	if err != nil {
		t.Fatalf("request %d: %v", i, err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("request %d: decoded %v, want %v", i, got, want)
	}
	return ric
}
