package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"testing"

	"golang.org/x/net/http2"
)

// h1Response is what serveH1Requests answers every request with.
var h1Response = []byte("HTTP/1.1 200 OK\r\nServer: nginx\r\nContent-Type: application/json\r\n" +
	"Content-Length: 2\r\nAlt-Svc: h3=\":443\"\r\nVary: Accept-Encoding\r\nX-Proto: h1\r\n\r\n{}")

var headEnd = []byte("\r\n\r\n")

// serveH1Requests answers each request head on conn with h1Response,
// allocating nothing per request, until conn closes.
func serveH1Requests(conn net.Conn) {
	var buf [8 << 10]byte

	n := 0

	for {
		m, err := conn.Read(buf[n:])
		if err != nil {
			return
		}

		n += m
		for i := bytes.Index(buf[:n], headEnd); i >= 0; i = bytes.Index(buf[:n], headEnd) {
			if _, err := conn.Write(h1Response); err != nil {
				return
			}

			n = copy(buf[:], buf[i+len(headEnd):n])
		}
	}
}

// literal is an HPACK field without indexing, its name the static table's
// entry idx (at most 15+127) or, with idx 0, name; no Huffman coding.
func literal(idx int, name, value string) []byte {
	var b []byte
	if idx < 15 {
		b = append(b, byte(idx))
	} else {
		b = append(b, 0x0f, byte(idx-15))
	}

	if idx == 0 {
		b = append(b, byte(len(name)))
		b = append(b, name...)
	}

	b = append(b, byte(len(value)))

	return append(b, value...)
}

// serveH2Requests answers each request on conn, a TLS connection that
// negotiated h2, with the same response, allocating nothing per request:
// frames are read by hand and the header block, literals only, is encoded
// once.
func serveH2Requests(ctx context.Context, conn net.Conn) {
	tc, err := tlsServer(ctx, conn)
	if err != nil {
		return
	}

	block := []byte{0x88} // :status 200
	for _, f := range [][]byte{
		literal(54, "", "nginx"),
		literal(31, "", "application/json"),
		literal(28, "", "2"),
		literal(0, "alt-svc", `h3=":443"`),
		literal(59, "", "Accept-Encoding"),
		literal(0, "x-proto", "h2"),
		literal(0, "access-control-allow-origin", "*"),
	} {
		block = append(block, f...)
	}

	body := []byte("{}")
	fr := http2.NewFramer(tc, nil)

	var (
		preface [len(http2.ClientPreface)]byte
		hdr     [9]byte
		payload [1 << 14]byte
	)

	if _, err := io.ReadFull(tc, preface[:]); err != nil || fr.WriteSettings() != nil {
		return
	}

	for {
		if _, err := io.ReadFull(tc, hdr[:]); err != nil {
			return
		}

		length := int(hdr[0])<<16 | int(hdr[1])<<8 | int(hdr[2])
		typ, flags := http2.FrameType(hdr[3]), http2.Flags(hdr[4])
		stream := binary.BigEndian.Uint32(hdr[5:]) & (1<<31 - 1)

		if length > len(payload) {
			return
		}

		if _, err := io.ReadFull(tc, payload[:length]); err != nil {
			return
		}

		switch {
		case typ == http2.FrameSettings && !flags.Has(http2.FlagSettingsAck):
			err = fr.WriteSettingsAck()
		case typ == http2.FramePing && !flags.Has(http2.FlagPingAck):
			err = fr.WritePing(true, [8]byte(payload[:8]))
		case typ == http2.FrameHeaders && flags.Has(http2.FlagHeadersEndHeaders):
			err = fr.WriteHeaders(http2.HeadersFrameParam{
				StreamID:      stream,
				BlockFragment: block,
				EndHeaders:    true,
			})
			if err == nil {
				err = fr.WriteData(stream, true, body)
			}
		}

		if err != nil {
			return
		}
	}
}

// A warm GET's allocations, request path and response included, stay
// near what they are, on each TCP protocol: a guard for the per-request
// costs taken out (route keys, the pool's idle bookkeeping, HTTP/2 stream
// setup and response headers). The servers allocate nothing per request.
func TestWarmRequestAllocations(t *testing.T) {
	race := raceBuild()

	for _, tc := range []struct {
		name  string
		url   string
		serve func(ctx context.Context, conn net.Conn)
		// most is the bound, a little above what it measured; mostRace
		// with -race, whose sync.Pool drops a quarter of what is put back.
		most, mostRace float64
	}{
		{
			name: "http1",
			url:  "http://localhost/json",
			serve: func(_ context.Context, conn net.Conn) {
				serveH1Requests(conn)
			},
			most:     h1Most,
			mostRace: h1MostRace,
		},
		{
			name:     "http2",
			url:      "https://localhost/json",
			serve:    serveH2Requests,
			most:     h2Most,
			mostRace: h2MostRace,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				_ = ln.Close()
			})

			go func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}

					t.Cleanup(func() {
						_ = conn.Close()
					})

					go tc.serve(t.Context(), conn)
				}
			}()

			e := newTestEngine(t, options{
				dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
				},
			})

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}

			var buf [64]byte

			get := func() {
				resp, err := e.RoundTrip(req)
				if err != nil {
					t.Fatal(err)
				}

				_, _ = io.ReadFull(resp.Body, buf[:2])
				_ = resp.Body.Close()
			}

			get()

			most := tc.most
			if race {
				most = tc.mostRace
			}

			if n := testing.AllocsPerRun(200, get); n > most {
				t.Errorf("%v allocations per warm GET, want at most %v", n, most)
			}
		})
	}
}

// raceBuild reports whether the test binary was built with -race.
func raceBuild() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}

	for _, s := range info.Settings {
		if s.Key == "-race" {
			return s.Value == "true"
		}
	}

	return false
}

// The bounds, allocations per warm GET: 26 (HTTP/1.1) and 33 (HTTP/2;
// 34-35 with -race) measured, 32 and 57 before the cuts.
const (
	h1Most     = 27
	h1MostRace = 28
	h2Most     = 35
	h2MostRace = 37
)
