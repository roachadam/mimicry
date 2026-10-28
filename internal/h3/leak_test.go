package h3

import (
	"context"
	"io"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"

	utls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/internal/quic"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profiles"
)

// A long-lived connection forgets the requests it finished: the QUIC
// connection kept every request stream read to its end, with its buffers
// (about 10 KB each), as h3 never closed its read side (bench 2026-10-27:
// peak heap in GBs under load).
func TestFinishedRequestsAreReleased(t *testing.T) {
	c := rawH3(t)

	heap := func() uint64 {
		var m runtime.MemStats

		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&m)

		return m.HeapAlloc
	}

	get(t, c, 200)

	before := heap()

	const n = 2000

	get(t, c, n)

	after := heap()
	// Leaking, 2000 requests keep about 20 MB.
	if after > before && after-before > 4<<20 {
		t.Fatalf("heap grew %d KB over %d requests on one connection", (after-before)>>10, n)
	}
}

// A request on a warm connection takes its QUIC stream buffers from the pool:
// each stream's (4 KB per direction, at the client and at the server here)
// went back once the stream was done with them. Only a buffer wholly consumed
// went back before, so every request allocated four new ones (bench
// 2026-10-27: 43% of HTTP/3's allocated bytes, and 14 times net/http's GCs).
func TestRequestsReuseStreamBuffers(t *testing.T) {
	c := rawH3(t)

	totalAlloc := func() uint64 {
		var m runtime.MemStats

		runtime.ReadMemStats(&m)

		return m.TotalAlloc
	}

	get(t, c, 200)

	// No GC while measuring: it empties the pool.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	const n = 1000

	before := totalAlloc()

	get(t, c, n)

	perRequest := (totalAlloc() - before) / n
	// About 7 KB here (17 KB with -race, whose sync.Pool drops a quarter
	// of what is put back), 8 KB more for each direction whose buffers are
	// allocated anew.
	bound := uint64(12 << 10)
	if raceEnabled() {
		bound = 20 << 10
	}

	if perRequest > bound {
		t.Fatalf("%d B allocated per request on a warm connection, want at most %d", perRequest, bound)
	}
}

// raceEnabled reports whether the test binary was built with -race.
func raceEnabled() bool {
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

// get makes n GET requests on c one after another, each read to its end.
func get(t *testing.T, c *Conn, n int) {
	t.Helper()

	for range n {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost/", nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := c.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
	}
}

// rawH3 dials an HTTP/3 connection, as Chrome, to an in-process server
// answering every request 200 with a short body.
func rawH3(t *testing.T) *Conn {
	t.Helper()

	cert, roots := testutil.Certificate(t, "localhost")

	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ep, err := quic.NewEndpoint(pc, &quic.Config{
		TLSConfig: &utls.Config{
			Certificates: []utls.Certificate{{
				Certificate: cert.Certificate,
				PrivateKey:  cert.PrivateKey,
			}},
			NextProtos: []string{"h3"},
			MinVersion: utls.VersionTLS13,
		},
	})
	if err != nil {
		_ = pc.Close()

		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_ = ep.Close(ctx)
	})

	go func() {
		for {
			sc, err := ep.Accept(context.Background())
			if err != nil {
				return
			}

			go serveRaw(sc)
		}
	}()

	p := profiles.Chrome()
	plan := fingerprint.Compile(p)

	spec, err := handshake.Spec(plan.QUICHello())
	if err != nil {
		t.Fatal(err)
	}

	cpc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := ep.LocalAddr().Port()

	qc, err := quic.Dial(t.Context(), cpc, ep.LocalAddr(), quic.DialConfig{
		Profile: p.QUIC,
		Hello:   spec,
		TLS: handshake.QUICConfig(handshake.Config{
			ServerName: "localhost",
			RootCAs:    roots,
		}),
		NewPath: func(ctx context.Context) (net.PacketConn, error) {
			return (&net.ListenConfig{}).ListenPacket(ctx, "udp", "127.0.0.1:0")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	c, err := New(qc, p, Options{
		AltHost: "localhost",
		AltPort: strconv.Itoa(int(port)),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = c.Close()
	})

	return c
}

// serveRaw sends an empty SETTINGS (no QPACK dynamic table), drains the
// client's uni streams and answers each request with :status 200 and "ok".
func serveRaw(sc *quic.Conn) {
	ctx := context.Background()

	control, err := sc.NewSendOnlyStream(ctx)
	if err != nil {
		return
	}

	_, _ = control.Write([]byte{0x00, 0x04, 0x00})
	_ = control.Flush()

	for {
		s, err := sc.AcceptStream(ctx)
		if err != nil {
			return
		}

		go func() {
			_, _ = io.Copy(io.Discard, s)
			if s.IsReadOnly() {
				return
			}

			// HEADERS: QPACK prefix 0 0, static index 25 (:status 200);
			// DATA "ok".
			_, _ = s.Write([]byte{0x01, 0x03, 0x00, 0x00, 0xd9, 0x00, 0x02, 'o', 'k'})
			_ = s.Close()
		}()
	}
}
