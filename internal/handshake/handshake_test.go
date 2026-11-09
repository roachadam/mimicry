package handshake

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	utls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/testutil"
	"github.com/roachadam/mimicry/profiles"
)

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

	plan := fingerprint.Compile(profiles.ChromeMacOS().Spec())

	_, _, err = Client(t.Context(), client, plan.Hello(), Config{
		ServerName: "localhost",
	})
	if err == nil {
		t.Fatal("handshake succeeded with a certificate nobody trusts")
	}
}

// A CertificateRequest that does not allow the scheme the profile's
// browser signs the key with is ErrNotMeasured, before the callback's
// certificate goes out: no capture shows what a browser does then. A Go
// server always allows every measured scheme, so the request is built
// here.
func TestClientCertificateSchemeNotAllowed(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		ClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &tls.Certificate{
				Certificate: [][]byte{
					der,
				},
				PrivateKey: key,
			}, nil
		},
		ClientAuth: profiles.ChromeMacOS().Spec().TLS.ClientAuth,
	}

	for _, tc := range []struct {
		name    string
		schemes []utls.SignatureScheme
		allowed bool
	}{
		{
			name: "the browser's scheme allowed",
			schemes: []utls.SignatureScheme{
				utls.PSSWithSHA256,
				utls.ECDSAWithP256AndSHA256,
			},
			allowed: true,
		},
		{
			name: "other schemes only",
			schemes: []utls.SignatureScheme{
				utls.PSSWithSHA256,
				utls.ECDSAWithSHA1,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := clientCertificate(cfg, nil, &utls.CertificateRequestInfo{
				SignatureSchemes: tc.schemes,
				Version:          utls.VersionTLS12,
			})

			switch {
			case tc.allowed && (err != nil || len(c.Certificate) != 1):
				t.Fatalf("got error %v; want the certificate", err)
			case !tc.allowed && !errors.Is(err, headerorder.ErrNotMeasured):
				t.Fatalf("got error %v (a certificate: %v); want ErrNotMeasured", err, c != nil)
			}
		})
	}
}
