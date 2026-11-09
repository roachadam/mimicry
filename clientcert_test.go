package mimicry_test

import (
	"bytes"
	"context"
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

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// A client certificate goes out only as the profile describes
// (tls.clientAuth), to the host it was chosen for, against an HTTP/2
// server that asks every connection for one and whose certificate covers
// two hosts: a profile without tls.clientAuth, or a key type it never
// measured (P-384), fails with ErrNotMeasured; a nil certificate sends an
// empty Certificate; and a connection that carries a certificate never
// takes another host's request (no coalescing), which gets a connection
// and a certificate of its own, as Chrome and Firefox keep them apart.
func TestClientCertificate(t *testing.T) {
	p256 := clientIdentity(t, elliptic.P256())
	p384 := clientIdentity(t, elliptic.P384())

	for _, tc := range []struct {
		name    string
		profile profile.Profile
		cert    *tls.Certificate
		// hosts are requested in turn; "" moves to a new Transport,
		// given the tickets saved as JSON.
		hosts []string
		// want is what the server saw of each request: its host, the
		// connection it came on (numbered among those carrying requests)
		// and the client certificates of that connection; none: the
		// first request fails with ErrNotMeasured. asked are the hosts
		// GetClientCertificate was asked for.
		want  []string
		asked []string
	}{
		{
			name:    "no tls.clientAuth",
			profile: profiles.SafariMacOS(),
			cert:    p256,
			hosts: []string{
				"a.test",
			},
		},
		{
			name:    "unmeasured key type",
			profile: profiles.ChromeMacOS(),
			cert:    p384,
			hosts: []string{
				"a.test",
			},
		},
		{
			name:    "nil certificate",
			profile: profiles.ChromeMacOS(),
			hosts: []string{
				"a.test",
			},
			want: []string{
				"a.test on connection 1, 0 certificates",
			},
			asked: []string{
				"a.test",
			},
		},
		{
			name:    "no coalescing onto a connection with a certificate",
			profile: profiles.ChromeMacOS(),
			cert:    p256,
			hosts: []string{
				"a.test",
				"b.test",
			},
			want: []string{
				"a.test on connection 1, 1 certificates",
				"b.test on connection 2, 1 certificates",
			},
			asked: []string{
				"a.test",
				"b.test",
			},
		},
		{
			name:    "no coalescing onto a session resumed with a certificate",
			profile: profiles.ChromeMacOS(),
			cert:    p256,
			hosts: []string{
				"a.test",
				"",
				"a.test",
				"b.test",
			},
			want: []string{
				"a.test on connection 1, 1 certificates",
				"a.test on connection 2, 1 certificates, resumed",
				"b.test on connection 3, 1 certificates",
			},
			asked: []string{
				"a.test",
				"b.test",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu    sync.Mutex
				seen  []string
				conns = map[string]int{}
				asked []string
			)

			cert, pool := testutil.Certificate(t, "a.test", "b.test")
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()

				if conns[r.RemoteAddr] == 0 {
					conns[r.RemoteAddr] = len(conns) + 1
				}

				host, _, _ := net.SplitHostPort(r.Host)

				resumed := ""
				if r.TLS.DidResume {
					// The server restores the certificate from the ticket.
					resumed = ", resumed"
				}

				seen = append(seen, fmt.Sprintf("%s on connection %d, %d certificates%s", host, conns[r.RemoteAddr], len(r.TLS.PeerCertificates), resumed))
			}))

			srv.EnableHTTP2 = true
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

			resolver := loopbackDNS(t)
			transport := func(tickets *mimicry.TicketCache) *http.Client {
				tr, err := mimicry.NewTransport(mimicry.Config{
					Profile: tc.profile,
					RootCAs: pool,
					// Both hosts are the server, and look up to its
					// address (Chrome coalesces on lookup).
					DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
					},
					Resolver: resolver,
					Tickets:  tickets,
					GetClientCertificate: func(host string, _ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
						mu.Lock()
						defer mu.Unlock()

						asked = append(asked, host)

						return tc.cert, nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}

				t.Cleanup(tr.CloseIdleConnections)

				return &http.Client{
					Transport: tr,
				}
			}

			tickets := mimicry.NewTicketCache()
			client := transport(tickets)

			for _, host := range tc.hosts {
				if host == "" {
					// A new Transport, given the tickets saved as JSON.
					var saved bytes.Buffer
					if err := tickets.WriteJSON(&saved); err != nil {
						t.Fatal(err)
					}

					tickets = mimicry.NewTicketCache()
					if err := tickets.ReadJSON(&saved); err != nil {
						t.Fatal(err)
					}

					client = transport(tickets)

					continue
				}

				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+host+":"+port+"/", nil)
				if err != nil {
					t.Fatal(err)
				}

				resp, err := client.Do(req)
				if tc.want == nil {
					if !errors.Is(err, mimicry.ErrNotMeasured) {
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

			if !slices.Equal(asked, tc.asked) {
				t.Errorf("GetClientCertificate asked for %q, want %q", asked, tc.asked)
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
