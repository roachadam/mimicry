package mimicry

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// A client certificate goes out only as the profile describes
// (tls.clientAuth), against a server that asks every connection for one:
// a profile without tls.clientAuth, or a key type it never measured
// (P-384), fails with ErrNotMeasured; a nil certificate sends an empty
// Certificate; and with tls.clientAuth.resume false the session it went
// in is never resumed (a full handshake with the certificate each time),
// with true it is, sending none. Two requests, each on its own connection.
func TestClientCertificate(t *testing.T) {
	p256 := clientIdentity(t, elliptic.P256())
	p384 := clientIdentity(t, elliptic.P384())

	resume := func(r bool) profile.Profile {
		return profiles.ChromeMacOS().With(func(s *profile.Spec) {
			s.TLS.ClientAuth.Resume = r
		})
	}

	for _, tc := range []struct {
		name    string
		profile profile.Profile
		cert    *tls.Certificate
		// want is what the server saw of each request's connection;
		// none: the first fails with ErrNotMeasured.
		want []string
	}{
		{
			name:    "no tls.clientAuth",
			profile: profiles.SafariMacOS(),
			cert:    p256,
		},
		{
			name:    "unmeasured key type",
			profile: profiles.ChromeMacOS(),
			cert:    p384,
		},
		{
			name:    "nil certificate",
			profile: profiles.ChromeMacOS(),
			want: []string{
				"full handshake, 0 certificates",
				"resumed",
			},
		},
		{
			name:    "never resumed",
			profile: resume(false),
			cert:    p256,
			want: []string{
				"full handshake, 1 certificates",
				"full handshake, 1 certificates",
			},
		},
		{
			name:    "resumed",
			profile: resume(true),
			cert:    p256,
			want: []string{
				"full handshake, 1 certificates",
				"resumed",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu   sync.Mutex
				seen []string
			)

			cert, pool := testutil.Certificate(t, "localhost")
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()

				if r.TLS.DidResume {
					seen = append(seen, "resumed")
				} else {
					seen = append(seen, fmt.Sprintf("full handshake, %d certificates", len(r.TLS.PeerCertificates)))
				}

				// Each request on a connection of its own.
				w.Header().Set("Connection", "close")
			}))

			srv.TLS = &tls.Config{
				Certificates: []tls.Certificate{
					cert,
				},
				ClientAuth: tls.RequestClientCert,
			}
			// Dropped and refused handshakes are what the test makes.
			srv.Config.ErrorLog = log.New(io.Discard, "", 0)
			srv.StartTLS()
			t.Cleanup(srv.Close)

			_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

			tr, err := NewTransport(Config{
				Profile: tc.profile,
				RootCAs: pool,
				GetClientCertificate: func(string, *tls.CertificateRequestInfo) (*tls.Certificate, error) {
					return tc.cert, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(tr.CloseIdleConnections)

			client := &http.Client{
				Transport: tr,
			}

			for range max(len(tc.want), 1) {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost:"+port+"/", nil)
				if err != nil {
					t.Fatal(err)
				}

				resp, err := client.Do(req)
				if tc.want == nil {
					if !errors.Is(err, ErrNotMeasured) {
						t.Fatalf("got %v, want ErrNotMeasured", err)
					}

					return
				}

				if err != nil {
					t.Fatal(err)
				}

				_ = resp.Body.Close()
			}

			mu.Lock()
			defer mu.Unlock()

			if !slices.Equal(seen, tc.want) {
				t.Errorf("the server saw %q, want %q", seen, tc.want)
			}
		})
	}
}

// clientIdentity is a self-signed client certificate with a new ECDSA key
// on curve.
func clientIdentity(t *testing.T, curve elliptic.Curve) *tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "client",
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
		},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{
			der,
		},
		PrivateKey: key,
	}
}
