package server

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/internal/harness/internal/capture"
)

// The WebSocket recorder is the oracle for every websocket capture, so it is
// checked against the examples of RFC 6455 section 5.7 and RFC 7692 section
// 7.2.3; malformed input must record an error, never a frame.
func TestWebSocketRecordsRFCExamples(t *testing.T) {
	hello := []byte("Hello")
	bin256 := bytes.Repeat([]byte{0xab}, 256)
	bin64k := bytes.Repeat([]byte{0xcd}, 65536)
	join := func(parts ...[]byte) []byte {
		return bytes.Join(parts, nil)
	}

	for _, tc := range []struct {
		name    string
		deflate bool
		in      []byte
		want    []capture.WSFrame
		// wantErr is a substring of the recorded error ("" for none);
		// stopped is input the server stops reading at, with the transport
		// open. Otherwise the client ends it when its input ends.
		wantErr string
		stopped bool
	}{
		{
			name: "unmasked text",
			in:   []byte{0x81, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f},
			want: []capture.WSFrame{
				{
					Fin:        true,
					Opcode:     capture.WSText,
					LengthBits: 7,
					Length:     5,
					Payload:    hello,
				},
			},
		},
		{
			name: "masked text",
			in:   []byte{0x81, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58},
			want: []capture.WSFrame{
				{
					Fin:        true,
					Opcode:     capture.WSText,
					Masked:     true,
					Mask:       "37fa213d",
					LengthBits: 7,
					Length:     5,
					Payload:    hello,
				},
			},
		},
		{
			name: "fragmented text, ping and masked pong",
			in: []byte{
				0x01, 0x03, 0x48, 0x65, 0x6c,
				0x89, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f,
				0x80, 0x02, 0x6c, 0x6f,
				0x8a, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58,
			},
			want: []capture.WSFrame{
				{
					Opcode:     capture.WSText,
					LengthBits: 7,
					Length:     3,
					Payload:    []byte("Hel"),
				},
				{
					Fin:        true,
					Opcode:     capture.WSPing,
					LengthBits: 7,
					Length:     5,
					Payload:    hello,
				},
				{
					Fin:        true,
					Opcode:     capture.WSContinuation,
					LengthBits: 7,
					Length:     2,
					Payload:    []byte("lo"),
				},
				{
					Fin:        true,
					Opcode:     capture.WSPong,
					Masked:     true,
					Mask:       "37fa213d",
					LengthBits: 7,
					Length:     5,
					Payload:    hello,
				},
			},
		},
		{
			name: "16-bit and 64-bit lengths",
			in: join(
				[]byte{0x82, 0x7e, 0x01, 0x00}, bin256,
				[]byte{0x82, 0x7f, 0, 0, 0, 0, 0, 0x01, 0x00, 0x00}, bin64k,
			),
			want: []capture.WSFrame{
				{
					Fin:        true,
					Opcode:     capture.WSBinary,
					LengthBits: 16,
					Length:     256,
					Payload:    bin256,
				},
				{
					Fin:        true,
					Opcode:     capture.WSBinary,
					LengthBits: 64,
					Length:     65536,
					Payload:    bin64k,
				},
			},
		},
		{
			name: "close with code and reason",
			in:   []byte{0x88, 0x05, 0x03, 0xe8, 0x62, 0x79, 0x65},
			want: []capture.WSFrame{
				{
					Fin:         true,
					Opcode:      capture.WSClose,
					LengthBits:  7,
					Length:      5,
					Payload:     []byte{0x03, 0xe8, 'b', 'y', 'e'},
					CloseCode:   1000,
					CloseReason: "bye",
				},
			},
		},
		{
			name:    "compressed, fragmented, then context takeover",
			deflate: true,
			in: []byte{
				0x41, 0x03, 0xf2, 0x48, 0xcd,
				0x80, 0x04, 0xc9, 0xc9, 0x07, 0x00,
				0xc1, 0x05, 0xf2, 0x00, 0x11, 0x00, 0x00,
			},
			want: []capture.WSFrame{
				{
					RSV:        4,
					Opcode:     capture.WSText,
					LengthBits: 7,
					Length:     3,
					Payload:    []byte{0xf2, 0x48, 0xcd},
					Compressed: true,
				},
				{
					Fin:        true,
					Opcode:     capture.WSContinuation,
					LengthBits: 7,
					Length:     4,
					Payload:    []byte{0xc9, 0xc9, 0x07, 0x00},
					Compressed: true,
					Inflated:   hello,
				},
				{
					Fin:        true,
					RSV:        4,
					Opcode:     capture.WSText,
					LengthBits: 7,
					Length:     5,
					Payload:    []byte{0xf2, 0x00, 0x11, 0x00, 0x00},
					Compressed: true,
					Inflated:   hello,
				},
			},
		},
		{
			name: "RSV1 without permessage-deflate is not inflated",
			in:   []byte{0xc1, 0x07, 0xf2, 0x48, 0xcd, 0xc9, 0xc9, 0x07, 0x00},
			want: []capture.WSFrame{
				{
					Fin:        true,
					RSV:        4,
					Opcode:     capture.WSText,
					LengthBits: 7,
					Length:     7,
					Payload:    []byte{0xf2, 0x48, 0xcd, 0xc9, 0xc9, 0x07, 0x00},
				},
			},
		},
		{
			name:    "truncated payload",
			in:      []byte{0x81, 0x05, 0x48, 0x65},
			wantErr: "inside a frame",
		},
		{
			name:    "a length beyond the limit",
			in:      binary.BigEndian.AppendUint64([]byte{0x82, 0x7f}, 1<<40),
			wantErr: "more than",
			stopped: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &pageLog{}
			rec := log.openWebSocket(capture.WebSocket{})
			a := &wsAccept{}

			if tc.deflate {
				a.extensions = "permessage-deflate"
			}

			var sent bytes.Buffer

			s := &wsSession{
				rec:    rec,
				accept: a,
				write: func(p []byte) error {
					_, err := sent.Write(p)
					return err
				},
				end: func() error {
					return nil
				},
			}
			err := s.run(bytes.NewReader(tc.in))
			ws := log.page().WebSockets[0]

			var got []capture.WSFrame

			for _, f := range ws.Frames {
				if !f.Server {
					f.Millis = 0
					got = append(got, f)
				}
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("frames:\n got %+v\nwant %+v", got, tc.want)
			}

			closedBy := "client"
			if tc.stopped {
				closedBy = ""

				if err == nil {
					t.Errorf("run = nil, want the error it stopped reading on")
				}
			}

			if tc.wantErr == "" && ws.Error != "" || !strings.Contains(ws.Error, tc.wantErr) {
				t.Errorf("recorded error %q, want %q", ws.Error, tc.wantErr)
			}

			if ws.ClosedBy != closedBy {
				t.Errorf("closed by %q, want %q", ws.ClosedBy, closedBy)
			}
		})
	}
}
