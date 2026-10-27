package e2e

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/internal/ws"
	"github.com/roachadam/mimicry/profile"
)

// wsMessage is one client message of a captured WebSocket: its frames'
// payloads joined (compressed if RSV1 was set on its first frame) and, if
// compressed, what it inflated to.
type wsMessage struct {
	compressed bool
	payload    []byte
	inflated   []byte
}

// data is what the page sent.
func (m wsMessage) data() []byte {
	if m.compressed {
		return m.inflated
	}
	return m.payload
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
// permessage-deflate is what the profile's websocket section makes of it
// (ws.Compressor, with the window and context the server's answer
// allowed): compressed by its library byte for byte, or sent
// uncompressed where its rule says. Where the compressor cannot be made
// (a window the browser's library refuses), the browser sent nothing.
// TestWebSocketsMatchBrowser compares mimicry's frames with the same
// captures; this test names the first message and byte that differ.
func TestDeflateReproducesBrowserMessages(t *testing.T) {
	for _, name := range testProfiles(t) {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			if p.WebSocket == nil {
				t.Fatal("no websocket section")
			}
			sockets := 0
			for _, scenario := range []string{"websocket", "websocket-h2"} {
				pages, err := loadPages(t, p, scenario, ".pagetrace.json.gz")
				if err != nil {
					t.Fatal(err)
				}
				for i, page := range pages {
					for k := range page.WebSockets {
						sock := &page.WebSockets[k]
						if !strings.Contains(sock.Extensions, "permessage-deflate") {
							continue
						}
						sockets++
						t.Run(scenario+" sample "+strconv.Itoa(i+1)+" "+sock.Path, func(t *testing.T) {
							checkDeflate(t, p.WebSocket, sock)
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

// checkDeflate replays one socket's messages through the profile's
// compressor.
func checkDeflate(t *testing.T, p *profile.WebSocket, sock *capture.WebSocket) {
	t.Helper()
	d, err := ws.ParseExtensions(p.Extensions, sock.Extensions)
	if err != nil {
		t.Fatal(err)
	}
	msgs := wsMessages(t, sock)
	c, err := ws.NewCompressor(p, d)
	if err != nil {
		if len(msgs) > 0 {
			t.Fatalf("the profile fails the socket (%v), the browser sent %d messages", err, len(msgs))
		}
		return
	}
	if len(msgs) == 0 {
		t.Fatal("the browser failed the socket, the profile does not")
	}
	for i, m := range msgs {
		got, compressed := c.Message(m.data())
		switch {
		case compressed != m.compressed:
			t.Fatalf("message %d (%d bytes): compressed %v, the browser's %v", i, len(m.data()), compressed, m.compressed)
		case !bytes.Equal(got, m.payload):
			n := 0
			for n < len(got) && n < len(m.payload) && got[n] == m.payload[n] {
				n++
			}
			t.Fatalf("message %d (%d bytes): compressed to %d bytes, the browser's are %d; first difference at byte %d", i, len(m.data()), len(got), len(m.payload), n)
		}
	}
}
