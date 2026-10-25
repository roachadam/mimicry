package quictrace

import (
	"errors"
	"reflect"
	"testing"
)

// TestQPACKRFC9204Examples replays RFC 9204 appendix B's exchanges through
// one table, in order: each encoder stream chunk's instructions, each
// field section's lines (and a section read before the inserts it needs,
// which is blocked), and the decoder stream's instructions.
func TestQPACKRFC9204Examples(t *testing.T) {
	table := NewTable(220)
	type step struct {
		name string
		// encoder is encoder stream bytes, section a field section's,
		// decoder decoder stream bytes; one per step.
		encoder, section, decoder string
		wantEncoder               []EncoderInstruction
		wantFields                []FieldLine
		wantDecoder               []DecoderInstruction
		wantErr                   error
		wantInserted              uint64
	}
	for _, s := range []step{
		{
			name:       "B.1 literal with static name reference",
			section:    "0000510b2f696e6465782e68746d6c",
			wantFields: []FieldLine{{Rep: RepLiteralRef, Static: true, Index: 1, Name: ":path", Value: "/index.html"}},
		},
		{
			name:    "B.2 blocked before its inserts",
			section: "03811011",
			wantErr: ErrBlocked,
		},
		{
			name:    "B.2 encoder",
			encoder: "3fbd01c00f7777772e6578616d706c652e636f6dc10c2f73616d706c652f70617468",
			wantEncoder: []EncoderInstruction{
				{Op: OpSetCapacity, Capacity: 220},
				{Op: OpInsertNameRef, Static: true, Index: 0, Name: ":authority", Value: "www.example.com"},
				{Op: OpInsertNameRef, Static: true, Index: 1, Name: ":path", Value: "/sample/path"},
			},
			wantInserted: 2,
		},
		{
			name:    "B.2 post-base indexed",
			section: "03811011",
			wantFields: []FieldLine{
				{Rep: RepIndexedPostBase, Index: 0, Name: ":authority", Value: "www.example.com"},
				{Rep: RepIndexedPostBase, Index: 1, Name: ":path", Value: "/sample/path"},
			},
		},
		{
			name:        "B.2 decoder",
			decoder:     "84",
			wantDecoder: []DecoderInstruction{{Op: OpSectionAck, Stream: 4}},
		},
		{
			name:         "B.3 literal name insert",
			encoder:      "4a637573746f6d2d6b65790c637573746f6d2d76616c7565",
			wantEncoder:  []EncoderInstruction{{Op: OpInsertLiteral, Name: "custom-key", Value: "custom-value"}},
			wantInserted: 3,
		},
		{
			name:        "B.3 decoder",
			decoder:     "01",
			wantDecoder: []DecoderInstruction{{Op: OpInsertCountIncrement, Increment: 1}},
		},
		{
			name:         "B.4 duplicate",
			encoder:      "02",
			wantEncoder:  []EncoderInstruction{{Op: OpDuplicate, Index: 2, Name: ":authority", Value: "www.example.com"}},
			wantInserted: 4,
		},
		{
			name:    "B.4 dynamic and static indexed",
			section: "050080c181",
			wantFields: []FieldLine{
				{Rep: RepIndexed, Index: 0, Name: ":authority", Value: "www.example.com"},
				{Rep: RepIndexed, Static: true, Index: 1, Name: ":path", Value: "/"},
				{Rep: RepIndexed, Index: 1, Name: "custom-key", Value: "custom-value"},
			},
		},
		{
			name:        "B.4 decoder",
			decoder:     "48",
			wantDecoder: []DecoderInstruction{{Op: OpStreamCancel, Stream: 8}},
		},
		{
			name:         "B.5 insert with dynamic name, evicting",
			encoder:      "810d637573746f6d2d76616c756532",
			wantEncoder:  []EncoderInstruction{{Op: OpInsertNameRef, Index: 1, Name: "custom-key", Value: "custom-value2"}},
			wantInserted: 5,
		},
		{
			name:    "B.5 evicted entry",
			section: "020080",
			wantErr: errors.New("dynamic entry 0 not in the table"),
		},
	} {
		switch {
		case s.encoder != "":
			b := mustHex(t, s.encoder)
			got, n, err := table.ReadEncoderStream(b)
			if err != nil || n != len(b) {
				t.Fatalf("%s: read %d of %d bytes: %v", s.name, n, len(b), err)
			}
			if !reflect.DeepEqual(got, s.wantEncoder) {
				t.Errorf("%s: encoder instructions\n%+v\nwant\n%+v", s.name, got, s.wantEncoder)
			}
			if table.Inserted() != s.wantInserted {
				t.Errorf("%s: %d inserted; want %d", s.name, table.Inserted(), s.wantInserted)
			}
		case s.section != "":
			got, err := table.DecodeSection(mustHex(t, s.section))
			if s.wantErr != nil {
				if err == nil || (!errors.Is(err, s.wantErr) && err.Error() != s.wantErr.Error()) {
					t.Errorf("%s: error %v; want %v", s.name, err, s.wantErr)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			if !reflect.DeepEqual(got.Fields, s.wantFields) {
				t.Errorf("%s: fields\n%+v\nwant\n%+v", s.name, got.Fields, s.wantFields)
			}
		default:
			b := mustHex(t, s.decoder)
			got, n, err := ReadDecoderStream(b)
			if err != nil || n != len(b) {
				t.Fatalf("%s: read %d of %d bytes: %v", s.name, n, len(b), err)
			}
			if !reflect.DeepEqual(got, s.wantDecoder) {
				t.Errorf("%s: decoder instructions %+v; want %+v", s.name, got, s.wantDecoder)
			}
		}
	}
}
