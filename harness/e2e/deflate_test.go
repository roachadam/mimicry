package e2e

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/zdeflate"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// wsMessage is one client message of a captured WebSocket: its frames'
// payloads joined (compressed if RSV1 was set on its first frame) and, if
// compressed, what it inflated to.
type wsMessage struct {
	compressed bool
	payload    []byte
	inflated   []byte
}

// wsMessages returns the client's text and binary messages on ws, control
// frames left out.
func wsMessages(t *testing.T, ws *capture.WebSocket) []wsMessage {
	t.Helper()
	var out []wsMessage
	var cur *wsMessage
	for i, f := range ws.Frames {
		if f.Server || f.Opcode >= capture.WSClose {
			continue
		}
		if f.Opcode != capture.WSContinuation {
			out = append(out, wsMessage{
				compressed: f.RSV&0x4 != 0,
			})
			cur = &out[len(out)-1]
		} else if cur == nil {
			t.Fatalf("frame %d: continuation without a message", i)
		}
		if f.InflateError != "" {
			t.Fatalf("frame %d: %s", i, f.InflateError)
		}
		cur.payload = append(cur.payload, f.Payload...)
		if f.Fin {
			cur.inflated = f.Inflated
			cur = nil
		}
	}
	return out
}

// Every message a browser sent on a socket that negotiated
// permessage-deflate is what the profile's websocket section says: left
// uncompressed up to uncompressed_max bytes (and out of the compression
// context), otherwise compressed by zdeflate at the profile's compression,
// byte for byte, the context kept from message to message. A profile
// without compression must have a reason: zlib level 6 does not reproduce
// its browser's messages.
func TestDeflateReproducesBrowserMessages(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			if p.WebSocket == nil {
				t.Fatal("no websocket section")
			}
			sockets := 0
			for _, scenario := range []string{"websocket", "websocket-h2"} {
				pages, err := capture.LoadPages(captureDir + name + "." + scenario + ".pagetrace.json.gz")
				if err != nil {
					t.Fatal(err)
				}
				for i, page := range pages {
					for k := range page.WebSockets {
						ws := &page.WebSockets[k]
						if !strings.Contains(ws.Extensions, "permessage-deflate") {
							continue
						}
						sockets++
						t.Run(scenario+" sample "+strconv.Itoa(i+1)+" "+ws.Path, func(t *testing.T) {
							checkDeflate(t, p.WebSocket, wsMessages(t, ws))
						})
					}
				}
			}
			if sockets == 0 {
				t.Fatal("no socket negotiated permessage-deflate")
			}
		})
	}
}

// checkDeflate replays one socket's messages through the profile's rules.
func checkDeflate(t *testing.T, ws *profile.WebSocket, msgs []wsMessage) {
	t.Helper()
	if ws.Compression == "" {
		w := zdeflate.NewWriter(6)
		for _, m := range msgs {
			if m.compressed && !bytes.Equal(w.Message(m.inflated), m.payload) {
				return
			}
		}
		t.Fatal("no compression in the profile, yet zlib level 6 reproduces every message")
	}
	if ws.Compression != profile.CompressionZlib6 {
		t.Fatalf("compression %q not checked", ws.Compression)
	}
	w := zdeflate.NewWriter(6)
	for i, m := range msgs {
		if !m.compressed {
			if len(m.payload) > ws.UncompressedMax {
				t.Errorf("message %d: %d bytes sent uncompressed, uncompressed_max is %d", i, len(m.payload), ws.UncompressedMax)
			}
			continue
		}
		if ws.UncompressedMax != 0 && (len(m.inflated) <= ws.UncompressedMax || len(m.inflated) < ws.CompressMin) {
			t.Errorf("message %d: %d bytes sent compressed, uncompressed_max is %d and compress_min %d", i, len(m.inflated), ws.UncompressedMax, ws.CompressMin)
		}
		if got := w.Message(m.inflated); !bytes.Equal(got, m.payload) {
			n := 0
			for n < len(got) && n < len(m.payload) && got[n] == m.payload[n] {
				n++
			}
			t.Fatalf("message %d (%d bytes): compressed to %d bytes, the browser's are %d; first difference at byte %d", i, len(m.inflated), len(got), len(m.payload), n)
		}
	}
}
