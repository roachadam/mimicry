package ws

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/roachadam/mimicry/profile"
)

// fakeTransport is a socket's transport reading what a server sent from
// a fixed input, and keeping what the socket sends.
type fakeTransport struct {
	r io.Reader

	mu   sync.Mutex
	sent [][]byte
}

func (f *fakeTransport) Read(p []byte) (int, error) {
	return f.r.Read(p)
}

func (f *fakeTransport) Send(_ context.Context, parts [][]byte, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, bytes.Join(parts, nil))
	return nil
}

func (f *fakeTransport) Close() error {
	return nil
}

func (f *fakeTransport) Release() {}

// frames returns the frames the socket sent, unmasked: opcode and
// payload.
func (f *fakeTransport) frames(t *testing.T) []Message {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, 0, len(f.sent))
	for _, b := range f.sent {
		r := bytes.NewReader(b)
		h, err := ReadHeader(r)
		if err != nil || !h.Masked {
			t.Fatalf("the socket sent %x: %v, masked %v", b, err, h.Masked)
		}
		payload, _ := io.ReadAll(r)
		MaskBytes(h.Mask, 0, payload)
		out = append(out, Message{
			Opcode:  h.Opcode,
			Payload: payload,
		})
	}
	return out
}

func frame(first byte, payload []byte) []byte {
	return append(AppendHeader([]byte{}, Header{
		Fin:    first&0x80 != 0,
		RSV:    first & 0x70,
		Opcode: first & 0xf,
		Length: int64(len(payload)),
	}), payload...)
}

// What a server sends is read as RFC 6455 says (its section 5.7 examples),
// and what it may not send fails the socket before anything is buffered.
// Pings and closes are answered with their payload, code and reason.
func TestReadingServerFrames(t *testing.T) {
	hello := []byte("Hello")
	long := func(n int) []byte {
		return bytes.Repeat([]byte{0xab}, n)
	}
	lengthOnly := func(first byte, n uint64) []byte {
		return binary.BigEndian.AppendUint64([]byte{first, 127}, n)
	}
	closeFrame := append(binary.BigEndian.AppendUint16(nil, 1001), "going away"...)
	tests := []struct {
		name string
		in   []byte
		want []Message
		// sent is what the socket answers.
		sent []Message
		// end is how ReadMessage ends: "eof" (the input ended), "close"
		// (the close handshake) or "fail".
		end string
	}{
		{
			name: "text",
			in:   []byte{0x81, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f},
			want: []Message{{Opcode: OpText, Payload: hello}},
			end:  "eof",
		},
		{
			name: "fragmented with a ping",
			in:   bytes.Join([][]byte{{0x01, 0x03, 0x48, 0x65, 0x6c}, {0x89, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f}, {0x80, 0x02, 0x6c, 0x6f}}, nil),
			want: []Message{{Opcode: OpText, Payload: hello}},
			sent: []Message{{Opcode: OpPong, Payload: hello}},
			end:  "eof",
		},
		{
			name: "16-bit length",
			in:   append([]byte{0x82, 0x7e, 0x01, 0x00}, long(256)...),
			want: []Message{{Opcode: OpBinary, Payload: long(256)}},
			end:  "eof",
		},
		{
			name: "64-bit length",
			in:   append([]byte{0x82, 0x7f, 0, 0, 0, 0, 0, 1, 0, 0}, long(65536)...),
			want: []Message{{Opcode: OpBinary, Payload: long(65536)}},
			end:  "eof",
		},
		{
			name: "server close",
			in:   frame(0x88, closeFrame),
			sent: []Message{{Opcode: OpClose, Payload: closeFrame}},
			end:  "close",
		},
		{
			name: "masked",
			in:   []byte{0x81, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58},
			end:  "fail",
		},
		{
			name: "length's top bit",
			in:   lengthOnly(0x82, 1<<63),
			end:  "fail",
		},
		{
			name: "past MaxMessage",
			in:   lengthOnly(0x82, MaxMessage+1),
			end:  "fail",
		},
		{
			name: "long ping",
			in:   frame(0x89, long(126)),
			end:  "fail",
		},
		{
			name: "fragmented ping",
			in:   frame(0x09, nil),
			end:  "fail",
		},
		{
			name: "reserved opcode",
			in:   frame(0x83, nil),
			end:  "fail",
		},
		{
			name: "RSV1 without deflate",
			in:   frame(0xc1, hello),
			end:  "fail",
		},
		{
			name: "continuation first",
			in:   frame(0x80, hello),
			end:  "fail",
		},
		{
			name: "message inside a message",
			in:   append(frame(0x01, hello), frame(0x81, hello)...),
			end:  "fail",
		},
		{
			name: "1-byte close",
			in:   frame(0x88, []byte{0x03}),
			end:  "fail",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := &fakeTransport{
				r: bytes.NewReader(tc.in),
			}
			c, err := New(tr, tr, Config{
				Profile: &profile.WebSocket{
					Extensions: "permessage-deflate",
					H2Data:     profile.H2DataTogether,
					CloseH1:    profile.CloseAtOnce,
				},
				Name:  "test",
				Clock: clockwork.NewFakeClock(),
				Rand:  rand.NewChaCha8([32]byte{}),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var got []Message
			for {
				var m Message
				if m, err = c.ReadMessage(ctx); err != nil {
					break
				}
				got = append(got, m)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("messages: got %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i].Opcode != tc.want[i].Opcode || !bytes.Equal(got[i].Payload, tc.want[i].Payload) {
					t.Errorf("message %d: got opcode %#x, %d bytes", i, got[i].Opcode, len(got[i].Payload))
				}
			}
			var ce *CloseError
			switch {
			case tc.end == "eof" && !errors.Is(err, io.ErrUnexpectedEOF):
				t.Errorf("ended with %v, want the input's end", err)
			case tc.end == "close" && (!errors.As(err, &ce) || ce.Code != 1001 || ce.Reason != "going away"):
				t.Errorf("ended with %v, want the server's close", err)
			case tc.end == "fail" && (errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &ce) || errors.Is(err, context.DeadlineExceeded)):
				t.Errorf("ended with %v, want a failure", err)
			}
			<-c.ended
			sent := tr.frames(t)
			if len(sent) != len(tc.sent) {
				t.Fatalf("the socket sent %d frames, want %d", len(sent), len(tc.sent))
			}
			for i := range sent {
				if sent[i].Opcode != tc.sent[i].Opcode || !bytes.Equal(sent[i].Payload, tc.sent[i].Payload) {
					t.Errorf("sent frame %d: opcode %#x payload %q", i, sent[i].Opcode, sent[i].Payload)
				}
			}
		})
	}
}
