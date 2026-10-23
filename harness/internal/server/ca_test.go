package server

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"
)

// The capture CA is meant to be trusted on a developer's machine, so it must
// only ever vouch for the capture hostnames: a certificate it issues for any
// other name must fail verification.
func TestCANameConstraints(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir(), CAHosts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)

	good, err := ca.Issue(CAHosts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range CAHosts {
		if _, err := good.Leaf.Verify(x509.VerifyOptions{
			DNSName: host,
			Roots:   roots,
		}); err != nil {
			t.Errorf("certificate for %s rejected: %v", host, err)
		}
	}

	bad, err := ca.Issue([]string{
		"example.com",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = bad.Leaf.Verify(x509.VerifyOptions{
		DNSName: "example.com",
		Roots:   roots,
	})
	if err == nil {
		t.Fatal("a certificate for example.com verified under the capture CA")
	}
	var invalid x509.CertificateInvalidError
	if !errors.As(err, &invalid) || invalid.Reason != x509.CANotAuthorizedForThisName {
		t.Fatalf("example.com rejected for the wrong reason: %v", err)
	}
}

// The CA is created once and reused; trusting a new one each run would defeat
// the point. A CA for different names is refused rather than silently reused.
func TestCAPersists(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateCA(dir, CAHosts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreateCA(dir, CAHosts, time.Now())
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
