package wire

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the test vectors in proto/testdata/wire from the table in vectors_test.go")

const vectorDir = "../../../proto/testdata/wire"

// The vectors' window and stream limit: small, so a few bytes show credit
// running out.
const (
	vectorWindow     = 8
	vectorMaxStreams = 2
)

// manifest is proto/testdata/wire/manifest.json; its README describes it.
type manifest struct {
	Version    int      `json:"version"`
	Window     int      `json:"window"`
	MaxStreams int      `json:"maxStreams"`
	Vectors    []vector `json:"vectors"`
}

type vector struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Role        string        `json:"role"`
	File        string        `json:"file"`
	Frames      []vectorFrame `json:"frames"`
}

type vectorFrame struct {
	Dir     string  `json:"dir"`
	Size    int     `json:"size"`
	Stream  *uint32 `json:"stream,omitempty"`
	Type    string  `json:"type,omitempty"`
	End     bool    `json:"end,omitempty"`
	Payload string  `json:"payload,omitempty"`
	Auto    bool    `json:"auto,omitempty"`
	Expect  string  `json:"expect,omitempty"`
	Error   string  `json:"error,omitempty"`
	Remote  bool    `json:"remote,omitempty"`
	Note    string  `json:"note,omitempty"`
}

// step is a frame of a vector being built: its manifest entry and bytes.
type step struct {
	f   vectorFrame
	raw []byte
}

func frameStep(dir string, stream uint32, typ frameType, end bool, payload string) step {
	var flags uint8
	if end {
		flags = flagEnd
	}

	raw := appendFrame(nil, stream, typ, flags, []byte(payload))

	return step{
		f: vectorFrame{
			Dir:     dir,
			Size:    len(raw),
			Stream:  &stream,
			Type:    typ.String(),
			End:     end,
			Payload: hex.EncodeToString([]byte(payload)),
		},
		raw: raw,
	}
}

func in(stream uint32, typ frameType, end bool, payload string) step {
	return frameStep("in", stream, typ, end, payload)
}

func out(stream uint32, typ frameType, end bool, payload string) step {
	return frameStep("out", stream, typ, end, payload)
}

// auto is an out frame the endpoint sends on its own.
func auto(stream uint32, typ frameType, payload string) step {
	s := frameStep("out", stream, typ, false, payload)

	s.f.Auto = true

	return s
}

// malformed is input no frame field describes; note says what it is.
func malformed(raw []byte, note string) step {
	return step{
		f: vectorFrame{
			Dir:  "in",
			Size: len(raw),
			Note: note,
		},
		raw: raw,
	}
}

func (s step) ignored() step {
	s.f.Expect = "ignore"

	return s
}

func (s step) streamError(name string) step {
	s.f.Expect = "stream-error"
	s.f.Error = name

	return s
}

func (s step) connError(name string) step {
	s.f.Expect = "conn-error"
	s.f.Error = name

	return s
}

func (s step) remote() step {
	s.f.Remote = true

	return s
}

func u32(v uint32) string {
	return string(binary.BigEndian.AppendUint32(nil, v))
}

func rawHeader(stream uint32, typ, flags uint8, length uint32) []byte {
	b := binary.BigEndian.AppendUint32(nil, stream)

	b = append(b, typ, flags)

	return binary.BigEndian.AppendUint32(b, length)
}

// abort is the RESET on stream 0 an endpoint sends after a protocol error.
func abort(c Code) step {
	return auto(0, typeReset, u32(uint32(c)))
}

type tableVector struct {
	name        string
	description string
	role        string
	steps       []step
}

// vectorTable is the source of proto/testdata/wire (go test -run
// TestVectorFiles -update rewrites it).
func vectorTable() []tableVector {
	return []tableVector{
		{
			name:        "call-reply",
			description: "Two calls, each a CALL and a REPLY with END, on streams 1 and 3.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, true, "ping"),
				in(1, typeReply, true, "pong"),
				out(3, typeCall, true, "ping"),
				in(3, typeReply, true, "pong"),
			},
		},
		{
			name:        "call-reply-core",
			description: "The core's side of a call.",
			role:        "core",
			steps: []step{
				in(1, typeCall, true, "ping"),
				out(1, typeReply, true, "pong"),
			},
		},
		{
			name:        "response-body",
			description: "A 10-byte body after the REPLY, more than the 8-byte window: the reader grants each 4 bytes read back with WINDOW, and none after END.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, true, "get"),
				in(1, typeReply, false, "head"),
				in(1, typeData, false, "abcd"),
				auto(1, typeWindow, u32(4)),
				in(1, typeData, false, "efgh"),
				auto(1, typeWindow, u32(4)),
				in(1, typeData, true, "ij"),
			},
		},
		{
			name:        "request-body",
			description: "A body after the CALL; the core grants the 6 bytes read back, then replies.",
			role:        "core",
			steps: []step{
				in(1, typeCall, false, "post"),
				in(1, typeData, false, "abcdef"),
				auto(1, typeWindow, u32(6)),
				in(1, typeData, true, "gh"),
				out(1, typeReply, true, "ok"),
			},
		},
		{
			name:        "bidirectional",
			description: "DATA both ways after the REPLY; the SDK ends its direction first with an empty DATA, the core keeps sending, then ends.",
			role:        "core",
			steps: []step{
				in(1, typeCall, false, "socket"),
				out(1, typeReply, false, "open"),
				in(1, typeData, false, "hi"),
				out(1, typeData, false, "yo"),
				in(1, typeData, true, ""),
				out(1, typeData, false, "bye"),
				out(1, typeData, true, ""),
			},
		},
		{
			name:        "bidirectional-sdk",
			description: "DATA both ways after the REPLY, from the SDK's side; the SDK ends first, the core's last DATA carries END.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "socket"),
				in(1, typeReply, false, "open"),
				out(1, typeData, false, "hi"),
				in(1, typeData, false, "yo"),
				out(1, typeData, true, ""),
				in(1, typeData, true, "bye"),
			},
		},
		{
			name:        "send-credit",
			description: "The SDK spends its 8 bytes of credit; a WINDOW of 4 lets it send 4 more.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "put"),
				out(1, typeData, false, "abcdefgh"),
				in(1, typeWindow, false, u32(4)),
				out(1, typeData, false, "ijkl"),
				out(1, typeData, true, ""),
				in(1, typeReply, true, "ok"),
			},
		},
		{
			name:        "reverse-call",
			description: "The core opens stream 2 with an EVENT (as VerifyConnection does); the SDK answers with a REPLY.",
			role:        "sdk",
			steps: []step{
				in(2, typeEvent, true, "verify"),
				out(2, typeReply, true, "accept"),
			},
		},
		{
			name:        "reverse-call-core",
			description: "The core's side of a reverse call.",
			role:        "core",
			steps: []step{
				out(2, typeEvent, true, "verify"),
				in(2, typeReply, true, "accept"),
			},
		},
		{
			name:        "notice",
			description: "An EVENT on stream 0: a notice nothing answers.",
			role:        "sdk",
			steps: []step{
				in(0, typeEvent, false, "cookie"),
			},
		},
		{
			name:        "notice-core",
			description: "The core sends a notice.",
			role:        "core",
			steps: []step{
				out(0, typeEvent, false, "cookie"),
			},
		},
		{
			name:        "reset-by-peer",
			description: "The core resets a call the SDK has not ended: the stream fails with the core's CANCEL, and the SDK answers with a RESET of its own.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "get"),
				in(1, typeReset, false, u32(uint32(CodeCancel))),
				auto(1, typeReset, u32(uint32(CodeCancel))),
			},
		},
		{
			name:        "reset-after-end",
			description: "The SDK resets a call the core has ended (REPLY with END): no answer, the END said the core stopped. A WINDOW in flight afterwards is ignored.",
			role:        "core",
			steps: []step{
				in(1, typeCall, false, "upload"),
				out(1, typeReply, true, "ok"),
				in(1, typeReset, false, u32(uint32(CodeCancel))),
				in(1, typeWindow, false, u32(4)).ignored(),
			},
		},
		{
			name:        "reset-local",
			description: "The SDK resets its call; the core's frames in flight are ignored until its answering RESET, then stream 3 works.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "get"),
				out(1, typeReset, false, u32(uint32(CodeCancel))),
				in(1, typeReply, false, "head").ignored(),
				in(1, typeData, false, "late").ignored(),
				in(1, typeReset, false, u32(uint32(CodeCancel))).ignored(),
				out(3, typeCall, true, "next"),
				in(3, typeReply, true, "ok"),
			},
		},
		{
			name:        "reset-local-ended",
			description: "The core resets a call the SDK had ended (CALL with END): the stream is gone at once, and a WINDOW in flight is ignored.",
			role:        "core",
			steps: []step{
				in(1, typeCall, true, "get"),
				out(1, typeReply, false, "head"),
				out(1, typeReset, false, u32(uint32(CodeCancel))),
				in(1, typeWindow, false, u32(4)).ignored(),
			},
		},
		{
			name:        "late-window-reset",
			description: "A WINDOW and a RESET on a stream both sides ended are in flight, not errors.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, true, "get"),
				in(1, typeReply, true, "ok"),
				in(1, typeWindow, false, u32(4)).ignored(),
				in(1, typeReset, false, u32(uint32(CodeCancel))).ignored(),
			},
		},
		{
			name:        "credit-overrun",
			description: "The SDK sends 3 bytes, then 6 with 5 of credit left: the core resets the stream (CREDIT_OVERRUN), ignores frames until the SDK's answering RESET, and the connection goes on.",
			role:        "core",
			steps: []step{
				in(1, typeCall, false, "post"),
				in(1, typeData, false, "abc"),
				in(1, typeData, false, "defghi").streamError("CREDIT_OVERRUN"),
				auto(1, typeReset, u32(uint32(CodeCreditOverrun))),
				in(1, typeData, false, "j").ignored(),
				in(1, typeReset, false, u32(uint32(CodeCancel))).ignored(),
				in(3, typeCall, true, "next"),
				out(3, typeReply, true, "ok"),
			},
		},
		{
			name:        "frame-too-large",
			description: "A header announcing 65,537 bytes fails before any payload is read.",
			role:        "core",
			steps: []step{
				malformed(rawHeader(1, uint8(typeCall), 0, MaxPayload+1), "CALL 1 header with length 65537, no payload").connError("FRAME_TOO_LARGE"),
				abort(CodeFrameTooLarge),
			},
		},
		{
			name:        "unknown-type",
			description: "Frame type 7.",
			role:        "core",
			steps: []step{
				malformed(rawHeader(1, 7, 0, 0), "type 7 on stream 1, empty").connError("UNKNOWN_TYPE"),
				abort(CodeUnknownType),
			},
		},
		{
			name:        "unknown-type-zero",
			description: "Frame type 0.",
			role:        "sdk",
			steps: []step{
				malformed(rawHeader(0, 0, 0, 0), "ten zero bytes").connError("UNKNOWN_TYPE"),
				abort(CodeUnknownType),
			},
		},
		{
			name:        "unknown-flags",
			description: "Flag bit 0x02.",
			role:        "core",
			steps: []step{
				malformed(append(rawHeader(1, uint8(typeCall), 0x02, 2), "hi"...), "CALL 1 with flags 0x02").connError("UNKNOWN_FLAGS"),
				abort(CodeUnknownFlags),
			},
		},
		{
			name:        "closed-stream",
			description: "DATA on a stream both sides ended.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, true, "get"),
				in(1, typeReply, true, "ok"),
				in(1, typeData, false, "x").connError("STREAM_CLOSED"),
				abort(CodeStreamClosed),
			},
		},
		{
			name:        "ended-direction",
			description: "DATA after the SDK's END, on a stream still open.",
			role:        "core",
			steps: []step{
				in(1, typeCall, true, "get"),
				in(1, typeData, false, "x").connError("STREAM_CLOSED"),
				abort(CodeStreamClosed),
			},
		},
		{
			name:        "reused-id",
			description: "A CALL on an id below one already opened.",
			role:        "core",
			steps: []step{
				in(3, typeCall, true, "a"),
				in(1, typeCall, true, "b").connError("STREAM_CLOSED"),
				abort(CodeStreamClosed),
			},
		},
		{
			name:        "never-opened",
			description: "A REPLY on a stream the SDK never opened.",
			role:        "sdk",
			steps: []step{
				in(1, typeReply, false, "x").connError("STREAM_NOT_OPEN"),
				abort(CodeStreamNotOpen),
			},
		},
		{
			name:        "never-opened-peer",
			description: "DATA on an SDK stream id no CALL opened.",
			role:        "core",
			steps: []step{
				in(3, typeData, false, "x").connError("STREAM_NOT_OPEN"),
				abort(CodeStreamNotOpen),
			},
		},
		{
			name:        "never-opened-window",
			description: "A WINDOW on a stream never opened is an error, unlike one on a closed stream.",
			role:        "sdk",
			steps: []step{
				in(3, typeWindow, false, u32(4)).connError("STREAM_NOT_OPEN"),
				abort(CodeStreamNotOpen),
			},
		},
		{
			name:        "wrong-parity",
			description: "A CALL on an even id.",
			role:        "core",
			steps: []step{
				in(2, typeCall, false, "x").connError("WRONG_PARITY"),
				abort(CodeWrongParity),
			},
		},
		{
			name:        "wrong-parity-sdk",
			description: "An EVENT opening an odd id.",
			role:        "sdk",
			steps: []step{
				in(1, typeEvent, false, "x").connError("WRONG_PARITY"),
				abort(CodeWrongParity),
			},
		},
		{
			name:        "call-from-core",
			description: "The core never sends CALL; it opens streams with EVENT.",
			role:        "sdk",
			steps: []step{
				in(2, typeCall, false, "x").connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "event-from-sdk",
			description: "The SDK never sends EVENT.",
			role:        "core",
			steps: []step{
				in(0, typeEvent, false, "x").connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "data-on-stream-zero",
			description: "Stream 0 carries only EVENT and RESET.",
			role:        "core",
			steps: []step{
				in(0, typeData, false, "x").connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "stream-limit",
			description: "A third stream open at once, with a limit of 2.",
			role:        "core",
			steps: []step{
				in(1, typeCall, false, "a"),
				in(3, typeCall, false, "b"),
				in(5, typeCall, false, "c").connError("STREAM_LIMIT"),
				abort(CodeStreamLimit),
			},
		},
		{
			name:        "reply-twice",
			description: "A second REPLY.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, true, "get"),
				in(1, typeReply, false, "a"),
				in(1, typeReply, false, "b").connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "data-before-reply",
			description: "DATA before the REPLY.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, true, "get"),
				in(1, typeData, false, "x").connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "reply-from-opener",
			description: "A REPLY from the side that opened the stream.",
			role:        "core",
			steps: []step{
				in(1, typeCall, false, "a"),
				in(1, typeReply, false, "b").connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "window-zero",
			description: "A WINDOW of 0.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "a"),
				in(1, typeWindow, false, u32(0)).connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "window-overflow",
			description: "A WINDOW taking the credit (8) past 2^31-1.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "a"),
				in(1, typeWindow, false, u32(1<<31-8)).connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "window-short",
			description: "A WINDOW with a 2-byte payload.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "a"),
				in(1, typeWindow, false, "\x00\x04").connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "window-end",
			description: "END on a WINDOW.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "a"),
				in(1, typeWindow, true, u32(4)).connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "reset-short",
			description: "A RESET with a 1-byte payload.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, false, "a"),
				in(1, typeReset, false, "\x00").connError("BAD_FRAME"),
				abort(CodeBadFrame),
			},
		},
		{
			name:        "truncated-header",
			description: "The connection ends 5 bytes into a header; nothing is sent back.",
			role:        "core",
			steps: []step{
				malformed(rawHeader(1, uint8(typeCall), 0, 4)[:5], "first 5 bytes of a CALL 1 header").connError("TRUNCATED"),
			},
		},
		{
			name:        "truncated-payload",
			description: "The connection ends 2 bytes into a 4-byte payload; nothing is sent back.",
			role:        "core",
			steps: []step{
				malformed(append(rawHeader(1, uint8(typeCall), 0, 4), "ab"...), "CALL 1 announcing 4 bytes, 2 sent").connError("TRUNCATED"),
			},
		},
		{
			name:        "peer-abort",
			description: "The core resets the connection (RESET on stream 0, FRAME_TOO_LARGE): the SDK fails with the core's code and sends nothing.",
			role:        "sdk",
			steps: []step{
				out(1, typeCall, true, "get"),
				in(0, typeReset, false, u32(uint32(CodeFrameTooLarge))).connError("FRAME_TOO_LARGE").remote(),
			},
		},
	}
}

// buildVectors is the manifest and the byte files from vectorTable.
func buildVectors(t *testing.T) ([]byte, map[string][]byte) {
	t.Helper()

	m := manifest{
		Version:    1,
		Window:     vectorWindow,
		MaxStreams: vectorMaxStreams,
	}
	files := make(map[string][]byte)

	for _, tv := range vectorTable() {
		v := vector{
			Name:        tv.name,
			Description: tv.description,
			Role:        tv.role,
			File:        tv.name + ".bin",
		}

		var data []byte

		for _, s := range tv.steps {
			if s.f.Dir == "out" && !s.f.Auto && s.f.Type == "DATA" && s.f.End && s.f.Payload != "" {
				t.Fatalf("%s: an out DATA with END carries no payload (Write, then CloseWrite)", tv.name)
			}

			v.Frames = append(v.Frames, s.f)
			data = append(data, s.raw...)
		}

		m.Vectors = append(m.Vectors, v)
		files[v.File] = data
	}

	return encodeManifest(t, m), files
}

// encodeManifest writes one frame per line.
func encodeManifest(t *testing.T, m manifest) []byte {
	t.Helper()

	js := func(v any) string {
		var b bytes.Buffer

		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)

		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}

		return string(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "{\n  \"version\": %d,\n  \"window\": %d,\n  \"maxStreams\": %d,\n  \"vectors\": [\n", m.Version, m.Window, m.MaxStreams)

	for i, v := range m.Vectors {
		fmt.Fprintf(&b, "    {\n      \"name\": %s,\n      \"description\": %s,\n      \"role\": %s,\n      \"file\": %s,\n      \"frames\": [\n",
			js(v.Name), js(v.Description), js(v.Role), js(v.File))

		for j, f := range v.Frames {
			b.WriteString("        ")
			b.WriteString(js(f))

			if j < len(v.Frames)-1 {
				b.WriteString(",")
			}

			b.WriteString("\n")
		}

		b.WriteString("      ]\n    }")

		if i < len(m.Vectors)-1 {
			b.WriteString(",")
		}

		b.WriteString("\n")
	}

	b.WriteString("  ]\n}\n")

	return b.Bytes()
}

// The files in proto/testdata/wire are what vectorTable builds.
func TestVectorFiles(t *testing.T) {
	man, files := buildVectors(t)

	files["manifest.json"] = man

	if *update {
		old, err := filepath.Glob(filepath.Join(vectorDir, "*.bin"))
		if err != nil {
			t.Fatal(err)
		}

		for _, p := range old {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		}

		for name, data := range files {
			if err := os.WriteFile(filepath.Join(vectorDir, name), data, 0o644); err != nil { //nolint:gosec // test data, read by anyone
				t.Fatal(err)
			}
		}
	}

	onDisk, err := filepath.Glob(filepath.Join(vectorDir, "*.bin"))
	if err != nil {
		t.Fatal(err)
	}

	if len(onDisk) != len(files)-1 {
		t.Errorf("%d .bin files in %s, the table makes %d; run go test -run TestVectorFiles -update", len(onDisk), vectorDir, len(files)-1)
	}

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(vectorDir, name))
		if err != nil {
			t.Fatalf("%v; run go test -run TestVectorFiles -update", err)
		}

		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from vectorTable; run go test -run TestVectorFiles -update", name)
		}
	}
}

func readManifest(t testing.TB) (manifest, map[string][]byte) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(vectorDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}

	files := make(map[string][]byte)

	for _, v := range m.Vectors {
		data, err := os.ReadFile(filepath.Join(vectorDir, v.File))
		if err != nil {
			t.Fatal(err)
		}

		files[v.File] = data
	}

	return m, files
}

// Every vector replays as the README says, from the files: the endpoint
// sends exactly the out frames, delivers or ignores each in frame, and
// fails with the named error, never a panic or a hang.
func TestVectors(t *testing.T) {
	m, files := readManifest(t)

	for _, v := range m.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			if err := replay(ctx, m, v, files[v.File]); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// pipeRWC is a connection of two io.Pipes.
type pipeRWC struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipeRWC) Read(b []byte) (int, error) {
	return p.r.Read(b)
}

func (p *pipeRWC) Write(b []byte) (int, error) {
	return p.w.Write(b)
}

func (p *pipeRWC) Close() error {
	_ = p.r.Close()

	return p.w.Close()
}

// outBuf collects what the endpoint writes.
type outBuf struct {
	mu   sync.Mutex
	b    []byte
	eof  bool
	more chan struct{}
	off  int
}

func (o *outBuf) collect(r io.Reader) {
	buf := make([]byte, 4096)

	for {
		n, err := r.Read(buf)

		o.mu.Lock()

		o.b = append(o.b, buf[:n]...)
		o.eof = err != nil
		o.mu.Unlock()
		signal(o.more)

		if err != nil {
			return
		}
	}
}

// next is the next n bytes written.
func (o *outBuf) next(ctx context.Context, n int) ([]byte, error) {
	for {
		o.mu.Lock()

		if len(o.b) >= o.off+n {
			b := o.b[o.off : o.off+n]

			o.off += n
			o.mu.Unlock()

			return b, nil
		}

		eof := o.eof
		o.mu.Unlock()

		if eof {
			return nil, fmt.Errorf("the endpoint closed the connection before sending %d more bytes", n)
		}

		select {
		case <-o.more:
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for %d bytes from the endpoint: %w", n, ctx.Err())
		}
	}
}

// rest waits for the end and returns what came after the last next.
func (o *outBuf) rest(ctx context.Context) ([]byte, error) {
	for {
		o.mu.Lock()

		if o.eof {
			b := o.b[o.off:]
			o.mu.Unlock()

			return b, nil
		}

		o.mu.Unlock()

		select {
		case <-o.more:
		case <-ctx.Done():
			return nil, errors.New("the endpoint never closed the connection")
		}
	}
}

// errName is the manifest's name for err, and whether the peer reported it.
func errName(err error) (string, bool) {
	if re, ok := errors.AsType[*RemoteError](err); ok {
		return re.Code.String(), true
	}

	if code, ok := codeOf(err); ok {
		return code.String(), false
	}

	switch {
	case errors.Is(err, ErrTruncated):
		return "TRUNCATED", false
	case errors.Is(err, ErrConnClosed):
		return "CONN_CLOSED", false
	}

	return "", false
}

type replayer struct {
	c       *Conn
	streams map[uint32]*Stream
	events  chan []byte
}

func replay(ctx context.Context, m manifest, v vector, data []byte) error {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	role := RoleSDK
	if v.Role == "core" {
		role = RoleCore
	}

	r := &replayer{
		streams: make(map[uint32]*Stream),
		events:  make(chan []byte, len(v.Frames)),
	}

	c, err := NewConn(&pipeRWC{
		r: inR,
		w: outW,
	}, Config{
		Role:       role,
		Window:     m.Window,
		MaxStreams: m.MaxStreams,
		OnEvent: func(p []byte) {
			r.events <- p
		},
	})
	if err != nil {
		return err
	}

	r.c = c

	o := &outBuf{
		more: make(chan struct{}, 1),
	}
	go o.collect(outR)

	// A step that hangs is unblocked by closing everything.
	stop := context.AfterFunc(ctx, func() {
		_ = c.Close()
		_ = inW.Close()
	})
	defer stop()

	wantErr, wantRemote := "CONN_CLOSED", false
	off := 0

	for i, f := range v.Frames {
		b := data[off : off+f.Size]

		off += f.Size

		if f.Dir == "out" {
			if !f.Auto {
				if err := r.act(ctx, f); err != nil {
					return fmt.Errorf("frame %d (%s %s): %w", i, f.Dir, f.Type, err)
				}
			}

			got, err := o.next(ctx, len(b))
			if err != nil {
				return fmt.Errorf("frame %d (%s %s): %w", i, f.Dir, f.Type, err)
			}

			if !bytes.Equal(got, b) {
				return fmt.Errorf("frame %d: the endpoint sent %x, want %x", i, got, b)
			}

			continue
		}

		// The endpoint may stop reading partway through a frame it fails
		// the connection on.
		if _, err := inW.Write(b); err != nil && f.Expect != "conn-error" {
			return fmt.Errorf("frame %d: writing it: %w (the endpoint failed: %w)", i, err, c.Err())
		}

		if f.Expect == "conn-error" {
			wantErr, wantRemote = f.Error, f.Remote
			_ = inW.Close()

			continue
		}

		if err := r.observe(ctx, f); err != nil {
			return fmt.Errorf("frame %d (%s %s): %w", i, f.Dir, f.Type, err)
		}
	}

	_ = inW.Close()

	select {
	case <-c.Done():
	case <-ctx.Done():
		return errors.New("the connection did not end after the input did")
	}

	if name, remote := errName(c.Err()); name != wantErr || remote != wantRemote {
		return fmt.Errorf("the connection failed with %w (%s, remote %t), want %s (remote %t)", c.Err(), name, remote, wantErr, wantRemote)
	}

	rest, err := o.rest(ctx)
	if err != nil {
		return err
	}

	if len(rest) > 0 {
		return fmt.Errorf("the endpoint sent %x after the last frame", rest)
	}

	return nil
}

func (r *replayer) stream(f vectorFrame) (*Stream, error) {
	s := r.streams[*f.Stream]
	if s == nil {
		return nil, fmt.Errorf("no stream %d", *f.Stream)
	}

	return s, nil
}

// act makes the endpoint send out frame f.
func (r *replayer) act(ctx context.Context, f vectorFrame) error {
	payload, err := hex.DecodeString(f.Payload)
	if err != nil {
		return err
	}

	if (f.Type == "CALL" || f.Type == "EVENT") && *f.Stream != 0 {
		s, err := r.c.Open(ctx, payload, f.End)
		if err != nil {
			return err
		}

		if s.ID() != *f.Stream {
			return fmt.Errorf("opened stream %d, want %d", s.ID(), *f.Stream)
		}

		r.streams[s.ID()] = s

		return nil
	}

	if f.Type == "EVENT" {
		return r.c.Notify(ctx, payload)
	}

	s, err := r.stream(f)
	if err != nil {
		return err
	}

	switch f.Type {
	case "REPLY":
		return s.WriteReply(payload, f.End)
	case "DATA":
		if len(payload) > 0 {
			if _, err := s.Write(payload); err != nil {
				return err
			}
		}

		if f.End {
			return s.CloseWrite()
		}

		return nil

	case "RESET":
		return s.Close()
	}

	return fmt.Errorf("no action sends %s", f.Type)
}

// observe checks what in frame f did.
func (r *replayer) observe(ctx context.Context, f vectorFrame) error {
	switch f.Expect {
	case "ignore":
		return nil
	case "stream-error":
		s, err := r.stream(f)
		if err != nil {
			return err
		}

		_, err = s.Read(make([]byte, 1))
		if name, remote := errName(err); name != f.Error || remote {
			return fmt.Errorf("the stream failed with %w, want %s", err, f.Error)
		}

		return nil

	case "":
	default:
		return fmt.Errorf("unknown expectation %q", f.Expect)
	}

	payload, err := hex.DecodeString(f.Payload)
	if err != nil {
		return err
	}

	switch f.Type {
	case "CALL", "EVENT":
		if *f.Stream == 0 {
			select {
			case got := <-r.events:
				if !bytes.Equal(got, payload) {
					return fmt.Errorf("notice %x, want %x", got, payload)
				}

				return nil

			case <-ctx.Done():
				return errors.New("no notice")
			}
		}

		s, err := r.c.Accept(ctx)
		if err != nil {
			return err
		}

		if s.ID() != *f.Stream || !bytes.Equal(s.Payload(), payload) {
			return fmt.Errorf("accepted stream %d with %x, want %d with %x", s.ID(), s.Payload(), *f.Stream, payload)
		}

		r.streams[s.ID()] = s

	case "REPLY":
		s, err := r.stream(f)
		if err != nil {
			return err
		}

		got, err := s.Reply()
		if err != nil {
			return err
		}

		if !bytes.Equal(got, payload) {
			return fmt.Errorf("REPLY %x, want %x", got, payload)
		}

	case "DATA":
		s, err := r.stream(f)
		if err != nil {
			return err
		}

		got := make([]byte, len(payload))
		if _, err := io.ReadFull(s, got); err != nil {
			return err
		}

		if !bytes.Equal(got, payload) {
			return fmt.Errorf("read %x, want %x", got, payload)
		}

	case "RESET":
		s, err := r.stream(f)
		if err != nil {
			return err
		}

		_, err = s.Read(make([]byte, 1))

		var re *RemoteError
		if !errors.As(err, &re) || re.Code != Code(binary.BigEndian.Uint32(payload)) {
			return fmt.Errorf("the stream failed with %w, want the peer's RESET", err)
		}

		return nil

	case "WINDOW":
		return nil
	}

	if f.End {
		s, err := r.stream(f)
		if err != nil {
			return err
		}

		if n, err := s.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
			return fmt.Errorf("after END, Read returned %d, %w; want io.EOF", n, err)
		}
	}

	return nil
}
