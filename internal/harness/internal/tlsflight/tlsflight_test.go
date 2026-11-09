package tlsflight_test

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/roachadam/mimicry/internal/harness/internal/tlsflight"
)

// Read decrypts a client's certificate flight, as crypto/tls's client sends
// it to crypto/tls's server, in TLS 1.3 and 1.2, with an EC and an RSA key,
// and reads a resumed handshake that sends none.
func TestReadClientFlight(t *testing.T) {
	ca, caKey := certificate(t, nil, nil, "test client CA", ecKey(t))

	for _, tc := range []struct {
		name    string
		version uint16
		key     crypto.Signer
		scheme  uint16
		flight  []string
	}{
		{"TLS 1.3 EC", tls.VersionTLS13, ecKey(t), 0x0403, []string{"change_cipher_spec", "certificate", "certificate_verify", "finished"}},
		{"TLS 1.3 RSA", tls.VersionTLS13, rsaKey(t), 0x0804, []string{"change_cipher_spec", "certificate", "certificate_verify", "finished"}},
		{"TLS 1.2 EC", tls.VersionTLS12, ecKey(t), 0x0403, []string{"certificate", "client_key_exchange", "certificate_verify", "change_cipher_spec", "finished"}},
		{"TLS 1.2 RSA", tls.VersionTLS12, rsaKey(t), 0x0804, []string{"certificate", "client_key_exchange", "certificate_verify", "change_cipher_spec", "finished"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf, _ := certificate(t, ca, caKey, "test client", tc.key)
			server, serverKey := certificate(t, nil, nil, "server.test", ecKey(t))

			pool := x509.NewCertPool()
			pool.AddCert(ca)

			keys := &tlsflight.KeyLog{}
			serverCfg := &tls.Config{
				Certificates: []tls.Certificate{
					{
						Certificate: [][]byte{
							server.Raw,
						},
						PrivateKey: serverKey,
					},
				},
				ClientAuth:   tls.RequestClientCert,
				ClientCAs:    pool,
				KeyLogWriter: keys,
				MaxVersion:   tc.version,
			}
			clientCfg := &tls.Config{
				Certificates: []tls.Certificate{
					{
						Certificate: [][]byte{
							leaf.Raw,
						},
						PrivateKey: tc.key,
					},
				},
				InsecureSkipVerify: true, //nolint:gosec // a test server
				ServerName:         "server.test",
				ClientSessionCache: tls.NewLRUClientSessionCache(4),
				MaxVersion:         tc.version,
			}

			for i, resumed := range []bool{false, true} {
				client, srv, state := handshake(t, clientCfg, serverCfg)
				if state.DidResume != resumed {
					t.Fatalf("connection %d: resumed %v", i+1, state.DidResume)
				}

				h, err := tlsflight.Read(client, srv, keys)
				if err != nil {
					t.Fatalf("connection %d: %v", i+1, err)
				}

				if h.Version != state.Version || h.Suite != state.CipherSuite {
					t.Errorf("connection %d: version %#04x suite %#04x, want %#04x %#04x", i+1, h.Version, h.Suite, state.Version, state.CipherSuite)
				}

				if h.ClientHello.Type != tlsflight.TypeClientHello {
					t.Errorf("connection %d: ClientHello is %s", i+1, h.ClientHello.Name())
				}

				flight := make([]string, 0, len(h.Client))
				for _, m := range h.Client {
					flight = append(flight, m.Name())
				}

				_, requested := tlsflight.Find(h.Server, tlsflight.TypeCertificateRequest)

				if resumed {
					if requested {
						t.Errorf("connection %d: a resumed handshake carried a CertificateRequest", i+1)
					}

					if want := []string{"change_cipher_spec", "finished"}; !slices.Equal(flight, want) {
						t.Errorf("connection %d: client flight %v, want %v", i+1, flight, want)
					}

					continue
				}

				if !slices.Equal(flight, tc.flight) {
					t.Fatalf("connection %d: client flight %v, want %v", i+1, flight, tc.flight)
				}

				if tc.version == tls.VersionTLS13 && h.ClientEncrypted < 1 {
					t.Errorf("connection %d: %d encrypted client records", i+1, h.ClientEncrypted)
				}

				cr, _ := tlsflight.Find(h.Server, tlsflight.TypeCertificateRequest)

				req, err := tlsflight.ParseCertificateRequest(h.Version, cr.Body)
				if err != nil {
					t.Fatal(err)
				}

				if len(req.SignatureAlgorithms) == 0 || !slices.ContainsFunc(req.Authorities, func(n []byte) bool {
					return bytes.Equal(n, ca.RawSubject)
				}) {
					t.Errorf("connection %d: CertificateRequest %+v lacks algorithms or the CA", i+1, req)
				}

				cm, _ := tlsflight.Find(h.Client, tlsflight.TypeCertificate)

				certs, err := tlsflight.ParseCertificate(h.Version, cm.Body)
				if err != nil {
					t.Fatal(err)
				}

				if len(certs) != 1 || !bytes.Equal(certs[0].DER, leaf.Raw) {
					t.Errorf("connection %d: %d certificates, want the leaf alone", i+1, len(certs))
				}

				cv, _ := tlsflight.Find(h.Client, tlsflight.TypeCertificateVerify)

				scheme, err := tlsflight.ParseCertificateVerify(cv.Body)
				if err != nil {
					t.Fatal(err)
				}

				if scheme != tc.scheme {
					t.Errorf("connection %d: CertificateVerify scheme %#04x, want %#04x", i+1, scheme, tc.scheme)
				}
			}
		})
	}
}

// handshake runs one connection between the configurations over loopback
// TCP, exchanging a byte each way (so a TLS 1.3 client takes its tickets),
// and returns every byte each side sent and the server's state.
func handshake(t *testing.T, clientCfg, serverCfg *tls.Config) (client, server []byte, state tls.ConnectionState) {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = ln.Close()
	}()

	tap := make(chan *tapConn, 1)
	errs := make(chan error, 1)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			errs <- err

			return
		}

		tc := &tapConn{
			Conn: c,
		}
		sc := tls.Server(tc, serverCfg)

		defer func() {
			_ = sc.Close()
		}()

		b := make([]byte, 1)
		if _, err := io.ReadFull(sc, b); err != nil {
			errs <- err

			return
		}

		if _, err := sc.Write(b); err != nil {
			errs <- err

			return
		}

		state = sc.ConnectionState()

		tap <- tc

		errs <- nil
	}()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	cc := tls.Client(c, clientCfg)

	defer func() {
		_ = cc.Close()
	}()

	if err := cc.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, err := cc.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}

	if _, err := io.ReadFull(cc, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}

	if err := <-errs; err != nil {
		t.Fatal(err)
	}

	tc := <-tap

	tc.mu.Lock()
	defer tc.mu.Unlock()

	return tc.in.Bytes(), tc.out.Bytes(), state
}

// tapConn keeps what a connection read and wrote.
type tapConn struct {
	net.Conn

	mu      sync.Mutex
	in, out bytes.Buffer
}

func (c *tapConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)

	c.mu.Lock()
	c.in.Write(p[:n])
	c.mu.Unlock()

	return n, err
}

func (c *tapConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.out.Write(p)
	c.mu.Unlock()

	return c.Conn.Write(p)
}

func ecKey(t *testing.T) crypto.Signer {
	t.Helper()

	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	return k
}

func rsaKey(t *testing.T) crypto.Signer {
	t.Helper()

	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	return k
}

// certificate makes a certificate for key named cn: a CA when issuer is
// nil (self-signed), else a client certificate issuer signs.
func certificate(t *testing.T, issuer *x509.Certificate, issuerKey crypto.Signer, cn string, key crypto.Signer) (*x509.Certificate, crypto.Signer) {
	t.Helper()

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: cn,
		},
		DNSNames: []string{
			cn,
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}

	parent, signer := tmpl, key
	if issuer == nil {
		tmpl.IsCA = true
		tmpl.BasicConstraintsValid = true
	} else {
		parent, signer = issuer, issuerKey
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}

	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return c, key
}
