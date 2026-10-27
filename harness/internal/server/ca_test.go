package server

import (
	"crypto/x509"
	"errors"
	"slices"
	"testing"
	"time"
)

// The capture CA is trusted on developer machines, so a certificate it
// issues for any name outside CADomains must fail verification.
func TestCANameConstraints(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir(), CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	verify := func(host string) error {
		cert, err := ca.Issue([]string{
			host,
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		_, err = cert.Leaf.Verify(x509.VerifyOptions{
			DNSName: host,
			Roots:   roots,
		})
		return err
	}
	for _, host := range append(slices.Clone(CAHosts), "www.kinds-a.test", "static.kinds-a.test", "www.nike-com.test", "a.localhost") {
		if err := verify(host); err != nil {
			t.Errorf("certificate for %s rejected: %v", host, err)
		}
	}
	for _, host := range []string{"example.com", "www.nike.com", "test.example.com", "latest.com", "localhost.example.com"} {
		err := verify(host)
		var invalid x509.CertificateInvalidError
		if !errors.As(err, &invalid) || invalid.Reason != x509.CANotAuthorizedForThisName {
			t.Errorf("a certificate for %s was not refused by the name constraints: %v", host, err)
		}
	}
}
