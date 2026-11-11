package convert

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"testing"

	"github.com/roachadam/mimicry/internal/ws"
)

// A record's length is bounded before its payload is allocated: a length
// over mimicry's own WebSocket message bound (ws.MaxMessage) fails as
// CODE_INVALID with next to nothing allocated, however large; one within
// it reads, and one cut short is io.ErrUnexpectedEOF.
func TestReadRecord(t *testing.T) {
	if maxRecord != ws.MaxMessage {
		t.Errorf("maxRecord %d, ws.MaxMessage %d: want them equal", maxRecord, ws.MaxMessage)
	}

	header := func(n uint32) []byte {
		return []byte{
			RecordBinary,
			byte(n >> 24),
			byte(n >> 16),
			byte(n >> 8),
			byte(n),
		}
	}

	for _, tc := range []struct {
		name    string
		in      []byte
		payload []byte
		invalid bool
		err     error
	}{
		{
			name:    "a message",
			in:      append(header(2), "hi"...),
			payload: []byte("hi"),
		},
		{
			name:    "one byte past the bound",
			in:      header(maxRecord + 1),
			invalid: true,
		},
		{
			name:    "the largest length",
			in:      header(1<<32 - 1),
			invalid: true,
		},
		{
			name: "cut short",
			in:   append(header(3), "hi"...),
			err:  io.ErrUnexpectedEOF,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before, after runtime.MemStats

			runtime.ReadMemStats(&before)

			typ, payload, err := ReadRecord(bytes.NewReader(tc.in))

			runtime.ReadMemStats(&after)

			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
				t.Errorf("allocated %d bytes", allocated)
			}

			_, invalid := errors.AsType[*InvalidError](err)

			switch {
			case tc.invalid:
				if !invalid {
					t.Errorf("error %v, want an *InvalidError", err)
				}

			case !errors.Is(err, tc.err):
				t.Errorf("error %v, want %v", err, tc.err)

			case err == nil && (typ != RecordBinary || !bytes.Equal(payload, tc.payload)):
				t.Errorf("record %d %q, want %d %q", typ, payload, RecordBinary, tc.payload)
			}
		})
	}
}
