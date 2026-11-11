package wire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// The frame reader takes any input apart into frames that encode back to
// the same bytes, and ends with io.EOF or a named error, never a panic.
// Seeds: every vector's bytes.
func FuzzReadFrame(f *testing.F) {
	m, files := readManifest(f)
	for _, v := range m.Vectors {
		f.Add(files[v.File])
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)

		var hb [headerSize]byte

		for {
			start := len(data) - r.Len()

			h, payload, err := readFrame(r, &hb)
			if err != nil {
				if errors.Is(err, io.EOF) && start == len(data) {
					return
				}

				if name, _ := errName(err); name == "" {
					t.Fatalf("unnamed error %v at byte %d", err, start)
				}

				return
			}

			consumed := data[start : len(data)-r.Len()]
			if got := appendFrame(nil, h.stream, h.typ, h.flags, payload); !bytes.Equal(got, consumed) {
				t.Fatalf("read %x, encodes back to %x", consumed, got)
			}
		}
	})
}

// A Conn of either role fed any input, with a server accepting, reading and
// closing what the peer opens, ends with a named error once the input
// does, never a panic or a hang. Seeds: every vector's input.
func FuzzConn(f *testing.F) {
	m, files := readManifest(f)
	for _, v := range m.Vectors {
		var input []byte

		off := 0

		for _, fr := range v.Frames {
			if fr.Dir == "in" {
				input = append(input, files[v.File][off:off+fr.Size]...)
			}

			off += fr.Size
		}

		var role byte
		if v.Role == "core" {
			role = 1
		}

		f.Add(role, input)
	}

	f.Fuzz(func(t *testing.T, role byte, input []byte) {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()

		go func() {
			_, _ = io.Copy(io.Discard, outR)
		}()

		cfg := Config{
			Role:       RoleSDK,
			Window:     vectorWindow,
			MaxStreams: vectorMaxStreams,
			OnEvent:    func([]byte) {},
		}
		if role%2 == 1 {
			cfg.Role = RoleCore
		}

		c, err := NewConn(&pipeRWC{
			r: inR,
			w: outW,
		}, cfg)
		if err != nil {
			t.Fatal(err)
		}

		go func() {
			_, _ = inW.Write(input)
			_ = inW.Close()
		}()

		go func() {
			for {
				s, err := c.Accept(context.Background())
				if err != nil {
					return
				}

				go func() {
					_, _ = io.Copy(io.Discard, s)
					_ = s.WriteReply([]byte("ok"), true)
					_ = s.Close()
				}()
			}
		}()

		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()

		select {
		case <-c.Done():
		case <-timer.C:
			_ = c.Close()

			t.Fatal("the connection did not end 10 s after its input did")
		}

		if name, _ := errName(c.Err()); name == "" {
			t.Fatalf("unnamed error %v", c.Err())
		}
	})
}
