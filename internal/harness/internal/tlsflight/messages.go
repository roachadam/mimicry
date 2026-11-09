package tlsflight

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
)

// Extensions this package reads (RFC 8446, section 4.2).
const (
	extSignatureAlgorithms     uint16 = 13
	extSupportedVersions       uint16 = 43
	extCertificateAuthorities  uint16 = 47
	extSignatureAlgorithmsCert uint16 = 50
)

// CertificateRequest is what a server's CertificateRequest asks for.
type CertificateRequest struct {
	// CertificateTypes are TLS 1.2's certificate_types.
	CertificateTypes []uint8
	// SignatureAlgorithms are the schemes it accepts in CertificateVerify
	// (TLS 1.3's signature_algorithms extension, TLS 1.2's
	// supported_signature_algorithms); SignatureAlgorithmsCert TLS 1.3's
	// signature_algorithms_cert, when sent.
	SignatureAlgorithms     []uint16
	SignatureAlgorithmsCert []uint16
	// Authorities are the distinguished names (DER) of the CAs it
	// names.
	Authorities [][]byte
}

// ParseCertificateRequest parses a CertificateRequest's body sent in
// version (RFC 8446, section 4.3.2; RFC 5246, section 7.4.4).
func ParseCertificateRequest(version uint16, body []byte) (*CertificateRequest, error) {
	p := parser(body)
	r := &CertificateRequest{}

	if version == VersionTLS12 {
		types, ok1 := p.vector8()
		algs, ok2 := p.vector16()
		cas, ok3 := p.vector16()

		if !ok1 || !ok2 || !ok3 {
			return nil, errors.New("malformed TLS 1.2 CertificateRequest")
		}

		r.CertificateTypes = bytes.Clone(types)
		r.SignatureAlgorithms = uint16s(algs)

		var err error
		if r.Authorities, err = names16(cas); err != nil {
			return nil, err
		}

		return r, nil
	}

	_, ok1 := p.vector8() // certificate_request_context
	exts, ok2 := p.vector16()

	if !ok1 || !ok2 {
		return nil, errors.New("malformed TLS 1.3 CertificateRequest")
	}

	for len(exts) > 0 {
		typ, ok1 := exts.uint16()
		data, ok2 := exts.vector16()

		if !ok1 || !ok2 {
			return nil, errors.New("malformed CertificateRequest extension")
		}

		switch typ {
		case extSignatureAlgorithms, extSignatureAlgorithmsCert:
			algs, ok := data.vector16()
			if !ok {
				return nil, errors.New("malformed CertificateRequest signature algorithms")
			}

			if typ == extSignatureAlgorithms {
				r.SignatureAlgorithms = uint16s(algs)
			} else {
				r.SignatureAlgorithmsCert = uint16s(algs)
			}

		case extCertificateAuthorities:
			cas, ok := data.vector16()
			if !ok {
				return nil, errors.New("malformed CertificateRequest certificate_authorities")
			}

			var err error
			if r.Authorities, err = names16(cas); err != nil {
				return nil, err
			}
		}
	}

	return r, nil
}

// CertificateEntry is one certificate of a Certificate message.
type CertificateEntry struct {
	// DER is the certificate.
	DER []byte
	// Extensions are its TLS 1.3 extensions, as sent (empty: none).
	Extensions []byte
}

// ParseCertificate parses a Certificate message's body sent in version
// (RFC 8446, section 4.4.2; RFC 5246, section 7.4.2): its certificates in
// order, none for an empty one.
func ParseCertificate(version uint16, body []byte) ([]CertificateEntry, error) {
	p := parser(body)

	if version == VersionTLS13 {
		if _, ok := p.vector8(); !ok {
			return nil, errors.New("malformed Certificate request context")
		}
	}

	list, ok := p.vector24()
	if !ok {
		return nil, errors.New("malformed Certificate list")
	}

	var out []CertificateEntry

	for len(list) > 0 {
		der, ok := list.vector24()
		if !ok {
			return nil, errors.New("malformed certificate entry")
		}

		e := CertificateEntry{
			DER: bytes.Clone(der),
		}
		if version == VersionTLS13 {
			ext, ok := list.vector16()
			if !ok {
				return nil, errors.New("malformed certificate entry extensions")
			}

			e.Extensions = bytes.Clone(ext)
		}

		out = append(out, e)
	}

	return out, nil
}

// ParseCertificateVerify returns a CertificateVerify's signature scheme
// (RFC 8446, section 4.4.3; TLS 1.2's SignatureAndHashAlgorithm, the same
// code points).
func ParseCertificateVerify(body []byte) (uint16, error) {
	p := parser(body)

	scheme, ok1 := p.uint16()
	_, ok2 := p.vector16()

	if !ok1 || !ok2 {
		return 0, errors.New("malformed CertificateVerify")
	}

	return scheme, nil
}

// KeyLog collects the secrets a tls.Config's KeyLogWriter writes, in the
// NSS key log format, by client random. It is safe for concurrent use.
type KeyLog struct {
	mu      sync.Mutex
	secrets map[string][]byte
}

// Write records each line of p: label, client random and secret, in hex.
func (k *KeyLog) Write(p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.secrets == nil {
		k.secrets = map[string][]byte{}
	}

	for line := range strings.SplitSeq(string(p), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || strings.HasPrefix(f[0], "#") {
			continue
		}

		secret, err := hex.DecodeString(f[2])
		if err != nil {
			return 0, err
		}

		k.secrets[f[0]+" "+strings.ToLower(f[1])] = secret
	}

	return len(p), nil
}

// Secret returns the secret logged under label for the connection whose
// ClientHello carried random, or nil.
func (k *KeyLog) Secret(label string, random []byte) []byte {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.secrets[label+" "+hex.EncodeToString(random)]
}

// parser reads TLS's length-prefixed fields.
type parser []byte

func (p *parser) skip(n int) bool {
	if len(*p) < n {
		return false
	}

	*p = (*p)[n:]

	return true
}

func (p *parser) uint16() (uint16, bool) {
	if len(*p) < 2 {
		return 0, false
	}

	v := binary.BigEndian.Uint16(*p)

	*p = (*p)[2:]

	return v, true
}

func (p *parser) vector(lenBytes int) (parser, bool) {
	if len(*p) < lenBytes {
		return nil, false
	}

	n := 0
	for _, b := range (*p)[:lenBytes] {
		n = n<<8 | int(b)
	}

	if len(*p) < lenBytes+n {
		return nil, false
	}

	v := (*p)[lenBytes : lenBytes+n]

	*p = (*p)[lenBytes+n:]

	return v, true
}

func (p *parser) vector8() (parser, bool) {
	return p.vector(1)
}

func (p *parser) vector16() (parser, bool) {
	return p.vector(2)
}

func (p *parser) vector24() (parser, bool) {
	return p.vector(3)
}

// uint16s reads a list of 16-bit values.
func uint16s(p parser) []uint16 {
	out := make([]uint16, 0, len(p)/2)
	for {
		v, ok := p.uint16()
		if !ok {
			return out
		}

		out = append(out, v)
	}
}

// names16 reads a list of 16-bit length-prefixed distinguished names.
func names16(p parser) ([][]byte, error) {
	var out [][]byte

	for len(p) > 0 {
		n, ok := p.vector16()
		if !ok {
			return nil, errors.New("malformed distinguished name list")
		}

		out = append(out, bytes.Clone(n))
	}

	return out, nil
}
