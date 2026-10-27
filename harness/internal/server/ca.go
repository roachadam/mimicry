package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// CaptureHost is the hostname browsers are pointed at; it resolves to
// 127.0.0.1. A hostname, not an IP, because browsers omit server_name for
// IP addresses.
const CaptureHost = "capture.mimicry.test"

// CADomains are the only domains the capture CA may issue for, with every
// name under them. localhost is kept for cmd/clientcapture; browser
// captures use *.test (see docs/capturing.md).
var CADomains = []string{
	"test",
	"localhost",
}

// CAHosts are the names the default capture certificate covers.
var CAHosts = []string{
	CaptureHost,
	"localhost",
}

// CA is the capture tools' certificate authority. It persists across runs so
// that it can be trusted once (Safari cannot be told to ignore certificate
// errors). Name constraints limit it to CADomains and forbid IP addresses, so
// even a trusted copy cannot vouch for any real site.
type CA struct {
	Cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// CertPath is the PEM certificate, the file to trust.
	CertPath string
}

// DefaultCADir is where the capture CA lives unless told otherwise:
// <user config dir>/mimicry/capture-ca, outside any repository.
func DefaultCADir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mimicry", "capture-ca"), nil
}

// LoadOrCreateCA loads the CA in dir, or creates one limited to domains. An
// existing CA must permit exactly domains; otherwise delete dir to replace it.
func LoadOrCreateCA(dir string, domains []string, now time.Time) (*CA, error) {
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		ca, err := parseCA(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		ca.CertPath = certPath
		if !sameHosts(ca.Cert.PermittedDNSDomains, domains) {
			return nil, fmt.Errorf("%s: the CA permits %v, not %v; delete the directory (and untrust the old CA) to replace it",
				dir, ca.Cert.PermittedDNSDomains, domains)
		}
		if now.After(ca.Cert.NotAfter) {
			return nil, fmt.Errorf("%s: the CA expired on %s; delete the directory (and untrust the old CA) to replace it", dir, ca.Cert.NotAfter)
		}
		return ca, nil
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
		return createCA(dir, certPath, keyPath, domains, now)
	default:
		return nil, fmt.Errorf("%s: incomplete CA, delete the directory to replace it: %w", dir, errors.Join(certErr, keyErr))
	}
}

func createCA(dir, certPath, keyPath string, domains []string, now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	_, allIPv4, _ := net.ParseCIDR("0.0.0.0/0")
	_, allIPv6, _ := net.ParseCIDR("::/0")
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "mimicry capture CA (local, .test)",
		},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.AddDate(5, 0, 0),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         slices.Clone(domains),
		ExcludedIPRanges: []*net.IPNet{
			allIPv4,
			allIPv6,
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{
		Cert:     cert,
		key:      key,
		CertPath: certPath,
	}, nil
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, errors.New("CA files are not PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("CA key does not match the certificate")
	}
	return &CA{
		Cert: cert,
		key:  key,
	}, nil
}

func sameHosts(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// Issue returns a server certificate for hosts, valid for a day from now and
// chained to the CA.
func (ca *CA) Issue(hosts []string, now time.Time) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := randomSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: hosts[0],
		},
		DNSNames:    slices.Clone(hosts),
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{
			der,
			ca.Cert.Raw,
		},
		PrivateKey: key,
		Leaf:       leaf,
	}, nil
}

// TrustedBySystem reports whether the operating system's verifier accepts
// cert for host, which is what Safari relies on.
func TrustedBySystem(cert tls.Certificate, host string) bool {
	_, err := cert.Leaf.Verify(x509.VerifyOptions{
		DNSName: host,
	})
	return err == nil
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}
