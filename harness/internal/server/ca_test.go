package server

import (
	"crypto/x509"
	"errors"
	"slices"
	"testing"
	"time"
)

// The capture CA is meant to be trusted on a developer's machine, so it must
// only ever vouch for capture names: anything under the reserved .test
// domain or localhost. A certificate it issues for any other name, however
// close, must fail verification.
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

// The CA is created once and reused; trusting a new one each run would defeat
// the point. A CA for different names is refused rather than silently reused.
func TestCAPersists(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateCA(dir, CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreateCA(dir, CADomains, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !again.Cert.Equal(first.Cert) {
		t.Error("second load created a new CA")
	}
	leaf, err := again.Issue(CAHosts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(first.Cert)
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{
		DNSName: "localhost",
		Roots:   roots,
	}); err != nil {
		t.Errorf("certificate from the reloaded CA does not chain to the original: %v", err)
	}
	if _, err := LoadOrCreateCA(dir, []string{"other.test"}, time.Now()); err == nil {
		t.Error("reused a CA created for different names")
	}
}
