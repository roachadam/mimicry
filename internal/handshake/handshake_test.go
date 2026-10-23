package handshake

import (
	"crypto/tls"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

func normalize(values []uint16) []profile.Code {
	out := make([]profile.Code, len(values))
	for i, v := range values {
		if profile.IsGREASE(v) {
			out[i] = profile.GREASE
		} else {
			out[i] = profile.Code(v)
		}
	}
	return out
}

// Each built-in profile must complete a real handshake, verifying the server
// certificate, and the ClientHello crypto/tls receives must carry exactly the
// cipher suites and extension order the plan chose for that connection.
func TestClientSendsThePlannedHello(t *testing.T) {
	cert, pool := testutil.Certificate(t, "localhost")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	infos := make(chan *tls.ClientHelloInfo, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() {
					_ = conn.Close()
				}()
				srv := tls.Server(conn, &tls.Config{
					Certificates: []tls.Certificate{
						cert,
					},
					NextProtos: []string{
						"h2",
						"http/1.1",
					},
					GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
						infos <- info
						return nil, nil //nolint:nilnil // crypto/tls: a nil config keeps the current one
					},
				})
				_ = srv.SetDeadline(time.Now().Add(10 * time.Second))
				_ = srv.HandshakeContext(t.Context())
			}()
		}
	}()

	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := fingerprint.Compile(p)
			if err != nil {
				t.Fatal(err)
			}
			hello := plan.Hello()
			raw, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			conn, proto, err := Client(t.Context(), raw, hello, Config{
				ServerName: "localhost",
				RootCAs:    pool,
			})
			if err != nil {
				t.Fatalf("handshake: %v", err)
			}
			defer func() {
				_ = conn.Close()
			}()
			if proto != "h2" {
				t.Errorf("negotiated %q, want h2", proto)
			}
			info := <-infos
			wantExts := make([]profile.Code, len(hello.Extensions))
			for i, e := range hello.Extensions {
				wantExts[i] = e.Type
			}
			if got := normalize(info.Extensions); !slices.Equal(got, wantExts) {
				t.Errorf("extension order\n got %v\nwant %v", got, wantExts)
			}
			if got := normalize(info.CipherSuites); !slices.Equal(got, hello.CipherSuites) {
				t.Errorf("cipher suites\n got %v\nwant %v", got, hello.CipherSuites)
			}
		})
	}
}

func TestHandshakeFailsOnUntrustedCertificate(t *testing.T) {
	cert, _ := testutil.Certificate(t, "localhost")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})
	go func() {
		server, err := ln.Accept()
		if err != nil {
			return
		}
		_ = server.SetDeadline(time.Now().Add(10 * time.Second))
		_ = tls.Server(server, &tls.Config{
			Certificates: []tls.Certificate{
				cert,
			},
		}).HandshakeContext(t.Context())
		_ = server.Close()
	}()
	client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fingerprint.Compile(profiles.Chrome155MacOS())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Client(t.Context(), client, plan.Hello(), Config{
		ServerName: "localhost",
	})
	if err == nil {
		t.Fatal("handshake succeeded with a certificate nobody trusts")
	}
}

// Raw-data extensions that uTLS keeps state for must carry the body uTLS
// sends; a profile asking for another body is refused rather than silently
// sent differently.
func TestSpecRefusesBodiesUTLSWouldChange(t *testing.T) {
	p := profiles.Chrome155MacOS()
	for i := range p.TLS.Extensions {
		if p.TLS.Extensions[i].Type == 0xff01 {
			p.TLS.Extensions[i].Data = profile.Bytes{
				1,
			}
		}
	}
	plan, err := fingerprint.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Spec(plan.Hello())
	if err == nil || !strings.Contains(err.Error(), "0xff01") {
		t.Fatalf("Spec error = %v, want a refusal for 0xff01", err)
	}
}
