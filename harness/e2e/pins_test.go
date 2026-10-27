package e2e

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/roachadam/mimicry"
)

// TestHTTP3Pins checks Config.Pins holds QUIC handshakes as it holds TCP
// ones: an origin pinned to the TCP server's key only never moves to HTTP/3;
// one pinned to both does.
func TestHTTP3Pins(t *testing.T) {
	pki := newInteropPKI(t)
	tcpPin := spkiPin(pki.leaf.Leaf)
	quicPin := spkiPin(pki.bigChain.Leaf)

	for _, name := range testProfiles(t) {
		p, err := loadProfile(name)
		if err != nil {
			t.Fatal(err)
		}

		if p.QUIC == nil || p.HTTP3 == nil {
			continue
		}

		for _, row := range []struct {
			name   string
			pins   []string
			wantH3 bool
		}{
			{
				name: "both servers' keys pinned",
				pins: []string{
					tcpPin,
					quicPin,
				},
				wantH3: true,
			},
			{
				name: "only the TCP server's key pinned",
				pins: []string{
					tcpPin,
				},
			},
		} {
			t.Run(name+"/"+row.name, func(t *testing.T) {
				t.Parallel()

				p, err := loadProfile(name)
				if err != nil {
					t.Fatal(err)
				}

				e := newInteropEnv(t, p, pki, interopCase{
					name:  row.name,
					limit: 30 * time.Second,
					server: backendOpts{
						bigChain: true,
					},
					config: func(cfg *mimicry.Config) {
						cfg.Pins = []mimicry.Pin{
							{
								Host: interopHost,
								SPKI: row.pins,
							},
						}
					},
				})
				if row.wantH3 {
					e.warm()

					resp, _, err := e.do(http.MethodGet, "/info", nil)
					if err != nil {
						t.Fatal(err)
					}

					e.h3(resp, "GET /info")

					if resp.TLS == nil || resp.TLS.NegotiatedProtocol != "h3" || !resp.TLS.HandshakeComplete ||
						len(resp.TLS.VerifiedChains) == 0 || !resp.TLS.VerifiedChains[0][0].Equal(pki.bigChain.Leaf) {
						t.Errorf("the HTTP/3 response's TLS state is %+v, want the QUIC connection's", resp.TLS)
					}

					return
				}

				// Every request answered over TCP, through at least one
				// refused QUIC attempt and a few requests after it.
				after := 0
				for after < 3 {
					if e.relay.Stats().Sessions > 0 {
						after++
					}

					resp, _, err := e.do(http.MethodGet, "/info", nil)
					if err != nil {
						t.Fatal(err)
					}

					if resp.ProtoMajor == 3 {
						t.Fatal("a response came over HTTP/3, from a server whose key no pin names")
					}

					select {
					case <-e.ctx.Done():
						t.Fatal("no QUIC attempt")
					case <-time.After(50 * time.Millisecond):
					}
				}
			})
		}
	}
}

// spkiPin is c's key as a "sha256/<base64>" pin.
func spkiPin(c *x509.Certificate) string {
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return "sha256/" + base64.StdEncoding.EncodeToString(sum[:])
}
