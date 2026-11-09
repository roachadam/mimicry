package profile

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
)

// Key types a CertificateVerify signature scheme signs with.
const (
	keyECDSAP256 = "ECDSA P-256"
	keyECDSAP384 = "ECDSA P-384"
	keyECDSAP521 = "ECDSA P-521"
	keyRSA       = "RSA"
	keyEd25519   = "Ed25519"
)

// schemeKeys are the key types of the signature schemes a client signs its
// CertificateVerify with (RFC 8446, section 4.2.3); pkcs1 marks the RSA
// PKCS#1 v1.5 ones, which TLS 1.3 forbids there.
var schemeKeys = map[Code]struct {
	key   string
	pkcs1 bool
}{
	0x0401: {keyRSA, true},
	0x0501: {keyRSA, true},
	0x0601: {keyRSA, true},
	0x0403: {keyECDSAP256, false},
	0x0503: {keyECDSAP384, false},
	0x0603: {keyECDSAP521, false},
	0x0804: {keyRSA, false},
	0x0805: {keyRSA, false},
	0x0806: {keyRSA, false},
	0x0807: {keyEd25519, false},
}

// Scheme is the signature scheme the browser signs a CertificateVerify with
// for a certificate whose public key is pub, in TLS 1.3 or 1.2; false when
// none was measured for that key type.
func (a *ClientAuth) Scheme(tls13 bool, pub crypto.PublicKey) (Code, bool) {
	schemes := a.TLS12
	if tls13 {
		schemes = a.TLS13
	}

	key := keyType(pub)
	for _, s := range schemes {
		if schemeKeys[s].key == key && key != "" {
			return s, true
		}
	}

	return 0, false
}

// keyType names pub's key type as schemeKeys does, or "".
func keyType(pub crypto.PublicKey) string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return keyRSA
	case ed25519.PublicKey:
		return keyEd25519
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256():
			return keyECDSAP256
		case elliptic.P384():
			return keyECDSAP384
		case elliptic.P521():
			return keyECDSAP521
		}
	}

	return ""
}

// checkClientAuthSchemes checks one of tls.clientAuth's scheme lists: known
// schemes, one per key type, none of PKCS#1 in TLS 1.3.
func (c *checker) checkClientAuthSchemes(path string, schemes []Code, tls13 bool) {
	seen := map[string]bool{}

	for _, s := range schemes {
		k, ok := schemeKeys[s]

		switch {
		case !ok:
			c.add(path, "signature scheme %s is not one a client signs with", s)
		case tls13 && k.pkcs1:
			c.add(path, "signature scheme %s is RSA PKCS#1, which TLS 1.3 forbids in CertificateVerify", s)
		case seen[k.key]:
			c.add(path, "two signature schemes for %s keys", k.key)
		}

		seen[k.key] = true
	}
}
