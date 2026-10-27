// Command setup prepares the server stack's volumes: a local CA (generated
// once, kept across runs), the servers' certificate signed by it, and the
// files nginx serves (package site's payloads, JSON and page), so nginx and
// the Go server send the same bytes. The client stack only needs the CA's
// certificate (ca.pem), copied from this volume for a remote server.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/roachadam/mimicry/internal/bench/site"
	"github.com/roachadam/mimicry/internal/bench/target"
)

const caKeyFile = "ca-key.pem"

func main() {
	caDir := flag.String("ca", target.CADir(), "directory for the CA and the server certificate")
	www := flag.String("www", "/data/www", "directory for nginx's content")

	flag.Parse()

	if err := setup(*caDir, *www); err != nil {
		log.Fatal(err)
	}
}

func setup(caDir, www string) error {
	if err := os.MkdirAll(caDir, 0o750); err != nil {
		return err
	}

	ca, caKey, err := loadOrCreateCA(caDir)
	if err != nil {
		return err
	}

	if err := writeServerCert(caDir, ca, caKey); err != nil {
		return err
	}

	return writeContent(www)
}

func loadOrCreateCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, certErr := os.ReadFile(filepath.Join(dir, target.CAFile))
	keyPEM, keyErr := os.ReadFile(filepath.Join(dir, caKeyFile))

	if certErr == nil && keyErr == nil {
		log.Printf("setup: keeping the CA in %s", dir)

		return parseCA(certPEM, keyPEM)
	}

	if !errors.Is(certErr, fs.ErrNotExist) && certErr != nil {
		return nil, nil, certErr
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject: pkix.Name{
			CommonName:   "mimicry benchmark CA",
			Organization: []string{"mimicry benchmark"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}

	if err := writePEM(filepath.Join(dir, caKeyFile), "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return nil, nil, err
	}

	if err := writePEM(filepath.Join(dir, target.CAFile), "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}

	log.Printf("setup: created a CA in %s", dir)

	cert, err := x509.ParseCertificate(der)

	return cert, key, err
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)

	if cb == nil || kb == nil {
		return nil, nil, errors.New("setup: unreadable CA files")
	}

	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}

	key, err := x509.ParseECPrivateKey(kb.Bytes)

	return cert, key, err
}

// writeServerCert issues the certificate both servers and every host name
// use. One certificate covering every host is what lets browsers (and
// mimicry's Chrome profile) coalesce www, static and api onto one HTTP/2
// connection.
func writeServerCert(dir string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	names := []string{target.ProxyHost}
	for _, s := range target.Servers {
		names = append(names, "*."+s.Domain)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject: pkix.Name{
			CommonName: "bench.test",
		},
		DNSNames:    names,
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}

	if err := writePEM(filepath.Join(dir, target.KeyFile), "EC PRIVATE KEY", keyDER, 0o644); err != nil {
		return err
	}

	// The chain: leaf then CA, as servers send it.
	chain := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})

	chain = append(chain, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: ca.Raw,
	})...)

	return os.WriteFile(filepath.Join(dir, target.CertFile), chain, 0o644) //nolint:gosec // the certificate is public
}

// writeContent writes nginx's document root.
func writeContent(www string) error {
	files := map[string][]byte{
		"json": []byte(site.JSON),
	}

	for _, n := range sizes() {
		files["bytes/"+strconv.Itoa(n)] = site.Payload(n)
	}

	for _, r := range site.Resources() {
		files[r.Path[1:]] = r.Body()
	}

	for name, body := range files {
		p := filepath.Join(www, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // nginx's workers, another user, read it
			return err
		}

		if err := os.WriteFile(p, body, 0o644); err != nil { //nolint:gosec // public content
			return err
		}
	}

	log.Printf("setup: wrote %d files to %s", len(files), www)

	return nil
}

// sizes are the /bytes/{n} files nginx serves.
func sizes() []int {
	return append([]int{0}, site.Bodies...)
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type:  typ,
		Bytes: der,
	}), mode)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		panic(fmt.Sprintf("setup: serial: %v", err))
	}

	return n
}
