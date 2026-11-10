// Package clientcert makes a capture run's test client identities: a client
// CA, made for the run and forgotten with it, and an identity per key type
// (EC P-256, RSA-2048) it issued, each also as a PKCS#12 file for the
// browsers' certificate stores. The CA is constrained to client
// authentication and to no name at all, so it cannot vouch for a server
// even where something trusted it; nothing does: only the identities are
// installed, never as a trust anchor.
package clientcert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// CACommonName is the subject common name of every run's client CA, by
// which a browser's automatic selection (Chrome's and Edge's
// AutoSelectCertificateForUrls filter) and the clean-up find its
// identities.
const CACommonName = "mimicry capture client CA (test)"

// Password protects the PKCS#12 files. They hold throwaway test keys; the
// stores they are imported into need some password.
const Password = "mimicry-test"

// Values of Identity.Key: server.ClientKeyEC and server.ClientKeyRSA,
// the key types server.Scenario.ClientCert names.
const (
	keyEC  = "ec"
	keyRSA = "rsa"
)

// Identity is a client certificate and its key.
type Identity struct {
	// Key is its key type, "ec" or "rsa".
	Key  string
	Cert *x509.Certificate
	// CA is the certificate that issued it.
	CA *x509.Certificate
	// P12 is a PKCS#12 file (legacy 3DES and SHA-1, which every store
	// imports) with the key, the certificate and the CA, untrusted: the
	// keychain and NSS keep it beside the identity, and Windows puts it
	// among the user's intermediate CAs.
	P12 []byte
	// Name is its subject common name.
	Name string
	// private is its key (TLS).
	private crypto.Signer
}

// Set is a run's client CA and its identities.
type Set struct {
	CA         *x509.Certificate
	identities map[string]*Identity
}

// lifetime is how long a run's certificates are valid: a run takes hours
// at most.
const lifetime = 48 * time.Hour

// New makes a client CA and an identity per key type, valid from an hour
// before now.
func New(now time.Time) (*Set, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	_, everyIPv4, _ := net.ParseCIDR("0.0.0.0/0")
	_, everyIPv6, _ := net.ParseCIDR("::/0")

	caTmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject: pkix.Name{
			CommonName: CACommonName,
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(lifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		// No name and no address under it is valid for a server.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains: []string{
			"invalid",
		},
		ExcludedIPRanges: []*net.IPNet{
			everyIPv4,
			everyIPv6,
		},
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}

	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}

	s := &Set{
		CA:         ca,
		identities: map[string]*Identity{},
	}

	for _, kind := range []string{keyEC, keyRSA} {
		id, err := issue(kind, ca, caKey, now)
		if err != nil {
			return nil, fmt.Errorf("%s identity: %w", kind, err)
		}

		s.identities[kind] = id
	}

	return s, nil
}

// Identity returns the identity with key type key ("ec", "rsa"), nil
// for another.
func (s *Set) Identity(key string) *Identity {
	return s.identities[key]
}

// AutoSelectEntries are Chrome's and Edge's AutoSelectCertificateForUrls
// policy entries, one JSON string per host: on https:// of the host, any
// port, the browser answers a CertificateRequest with an identity a run's
// client CA issued (found by CACommonName), without asking. Without the
// policy Chromium asks which identity to send, even when only one fits.
func AutoSelectEntries(hosts []string) []string {
	out := make([]string, len(hosts))
	for i, h := range hosts {
		out[i] = string(mustJSON(autoSelect{
			Pattern: "https://" + h,
			Filter: autoSelectFilter{
				Issuer: autoSelectName{
					CN: CACommonName,
				},
			},
		}))
	}

	return out
}

// LinuxPolicyFiles are where Chrome, Edge and Brave on Linux read
// LinuxPolicy: managed policy, writable by root only.
var LinuxPolicyFiles = map[string]string{
	"chrome": "/etc/opt/chrome/policies/managed/mimicry-client-cert.json",
	"edge":   "/etc/opt/edge/policies/managed/mimicry-client-cert.json",
	"brave":  "/etc/brave/policies/managed/mimicry-client-cert.json",
}

// LinuxPolicy is a managed policy file holding AutoSelectEntries for hosts.
func LinuxPolicy(hosts []string) []byte {
	return append(mustJSON(map[string][]string{
		"AutoSelectCertificateForUrls": AutoSelectEntries(hosts),
	}), '\n')
}

type autoSelect struct {
	Pattern string           `json:"pattern"`
	Filter  autoSelectFilter `json:"filter"`
}

// autoSelectFilter and autoSelectName are named as Chromium's policy
// format names them.
type autoSelectFilter struct {
	Issuer autoSelectName `json:"ISSUER"` //nolint:tagliatelle // Chromium's name
}

type autoSelectName struct {
	CN string `json:"CN"` //nolint:tagliatelle // Chromium's name
}

// mustJSON is v as JSON; the values are this package's own structs of
// strings, which cannot fail to marshal.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("clientcert: " + err.Error())
	}

	return b
}

// Pool holds the client CA alone: what the server names in its
// CertificateRequest.
func (s *Set) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(s.CA)

	return p
}

// issue makes the identity of key type kind, issued by ca.
func issue(kind string, ca *x509.Certificate, caKey crypto.Signer, now time.Time) (*Identity, error) {
	var (
		key crypto.Signer
		err error
	)

	switch kind {
	case keyEC:
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case keyRSA:
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	default:
		return nil, fmt.Errorf("unknown key type %q", kind)
	}

	if err != nil {
		return nil, err
	}

	name := "mimicry capture client " + kind + " (test)"
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject: pkix.Name{
			CommonName: name,
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(lifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, key.Public(), caKey)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	id := &Identity{
		Key:     kind,
		Cert:    cert,
		CA:      ca,
		Name:    name,
		private: key,
	}

	if id.P12, err = pkcs12.LegacyDES.Encode(key, cert, []*x509.Certificate{ca}, Password); err != nil {
		return nil, err
	}

	return id, nil
}

// TLS is the identity as crypto/tls holds it: its certificate alone,
// without the CA, as every captured browser sent it, and its key.
func (id *Identity) TLS() *tls.Certificate {
	return &tls.Certificate{
		Certificate: [][]byte{
			id.Cert.Raw,
		},
		PrivateKey: id.private,
		Leaf:       id.Cert,
	}
}

// serial is a random 128-bit serial number.
func serial() *big.Int {
	b := make([]byte, 16)

	// crypto/rand.Read never fails: it crashes the program instead.
	_, _ = rand.Read(b)

	return new(big.Int).SetBytes(b)
}
