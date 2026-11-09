package native

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

// keyLog is an io.Writer safe for concurrent use, holding NSS key log
// lines.
type keyLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (k *keyLog) Write(p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.buf.Write(p)
}

// lines returns the log's lines, sorted.
func (k *keyLog) lines() []string {
	k.mu.Lock()
	defer k.mu.Unlock()

	lines := strings.Split(strings.TrimSpace(k.buf.String()), "\n")
	slices.Sort(lines)

	return lines
}

// Config.KeyLogWriter logs the secrets that decrypt the connection: the
// client's lines are the server's, the four TLS 1.3 traffic secrets of one
// handshake, over TCP and over QUIC.
func TestKeyLogMatchesServer(t *testing.T) {
	for _, tc := range []struct {
		name string
		h3   bool
	}{
		{name: "http2"},
		{name: "http3", h3: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var client, server keyLog

			var addr string
			if !tc.h3 {
				addr = tcpServer(t, func(ctx context.Context, conn net.Conn) {
					tc := tls.Server(conn, &tls.Config{
						Certificates: []tls.Certificate{
							*serverCert.Load(),
						},
						NextProtos: []string{
							"h2",
						},
						KeyLogWriter: &server,
					})
					if tc.HandshakeContext(ctx) != nil {
						return
					}

					serveH2On(ctx, tc, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						_, _ = io.WriteString(w, "ok")
					}))
				})
			}

			e := newTestEngine(t, options{
				dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					if addr == "" {
						return nil, errNoTCP
					}

					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
				keyLog: &client,
			})

			if tc.h3 {
				port, conns := quicServerConns(t, &server)
				if _, a := dialQUIC(t, e, "https://localhost/", port); a.conn == nil {
					t.Fatal("the QUIC attempt failed")
				}

				go serveH3Requests(next(t, conns, "QUIC connection at the server"))
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost/", nil)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := e.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}

			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			if want := map[bool]int{false: 2, true: 3}[tc.h3]; resp.ProtoMajor != want {
				t.Fatalf("response over %s, want HTTP/%d", resp.Proto, want)
			}

			got, want := client.lines(), server.lines()

			labels := make([]string, 0, len(got))
			for _, l := range got {
				labels = append(labels, strings.Fields(l)[0])
			}

			if !slices.Equal(labels, []string{
				"CLIENT_HANDSHAKE_TRAFFIC_SECRET",
				"CLIENT_TRAFFIC_SECRET_0",
				"SERVER_HANDSHAKE_TRAFFIC_SECRET",
				"SERVER_TRAFFIC_SECRET_0",
			}) {
				t.Errorf("client key log labels %q, want one handshake's four TLS 1.3 secrets", labels)
			}

			if !slices.Equal(got, want) {
				t.Errorf("client key log:\n%s\nserver's:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
