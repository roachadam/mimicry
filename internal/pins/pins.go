// Package pins checks connections against the root package's Config.Pins
// (decision 0032): it parses the pins, finds the one covering a host,
// matches it against a verified chain, and lists a chain's pins for
// FetchPins.
package pins

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Pin pins a host (and, with IncludeSubdomains, its subdomains) to public
// keys: the root's Pin, which converts to it.
type Pin struct {
	Host              string
	IncludeSubdomains bool
	SPKI              []string
}

// Entry is a parsed Pin: the SHA-256 hashes it accepts, of keys and of
// whole certificates.
type Entry struct {
	subdomains bool
	spki       map[[sha256.Size]byte]bool
	certs      map[[sha256.Size]byte]bool
}

// Set is the root's Config.Pins by host.
type Set map[string]*Entry

// Parse checks and parses pins.
func Parse(pins []Pin) (Set, error) {
	set := make(Set, len(pins))
	for _, p := range pins {
		host, err := pinHost(p.Host)
		if err != nil {
			return nil, err
		}

		if set[host] != nil {
			return nil, fmt.Errorf("mimicry: Config.Pins has %s twice", host)
		}

		if len(p.SPKI) == 0 {
			return nil, fmt.Errorf("mimicry: the pin for %s has no SPKI", host)
		}

		e := &Entry{
			subdomains: p.IncludeSubdomains,
			spki:       map[[sha256.Size]byte]bool{},
			certs:      map[[sha256.Size]byte]bool{},
		}
		for _, v := range p.SPKI {
			sum, hexForm, err := parsePin(v)
			if err != nil {
				return nil, fmt.Errorf("mimicry: pin %q for %s: %w", v, host, err)
			}

			e.spki[sum] = true
			if hexForm {
				e.certs[sum] = true
			}
		}

		set[host] = e
	}

	return set, nil
}

// pinHost is a Pin's Host, lowercase, or an error.
func pinHost(h string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.String(), nil
	}

	if host == "" || strings.ContainsAny(host, ":/*@ ") {
		return "", fmt.Errorf("mimicry: pin host %q is not a host name or IP address", h)
	}

	return host, nil
}

// parsePin parses one of a Pin's SPKI values into its hash, reporting
// whether it was hex (a key's hash or a certificate's fingerprint).
func parsePin(v string) (sum [sha256.Size]byte, hexForm bool, err error) {
	if b64, ok := strings.CutPrefix(v, "sha256/"); ok {
		b, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return sum, false, fmt.Errorf("not base64: %w", err)
		}

		if len(b) != sha256.Size {
			return sum, false, fmt.Errorf("%d bytes, not a SHA-256 hash", len(b))
		}

		return [sha256.Size]byte(b), false, nil
	}

	b, err := hex.DecodeString(strings.ReplaceAll(v, ":", ""))
	if err != nil || len(b) != sha256.Size {
		return sum, false, errors.New(`neither "sha256/<base64>" nor a SHA-256 hash in hex`)
	}

	return [sha256.Size]byte(b), true, nil
}

// Lookup returns the entry covering host: its own, else the nearest
// parent's that includes subdomains; nil when none does.
func (s Set) Lookup(host string) *Entry {
	if len(s) == 0 {
		return nil
	}

	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip := net.ParseIP(host); ip != nil {
		return s[ip.String()]
	}

	if e := s[host]; e != nil {
		return e
	}

	for {
		_, parent, ok := strings.Cut(host, ".")
		if !ok {
			return nil
		}

		if e := s[parent]; e != nil && e.subdomains {
			return e
		}

		host = parent
	}
}

// Matches reports whether a certificate of one of cs's verified chains
// has a key or fingerprint e accepts.
func (e *Entry) Matches(cs tls.ConnectionState) bool {
	for _, chain := range cs.VerifiedChains {
		for _, c := range chain {
			if e.spki[sha256.Sum256(c.RawSubjectPublicKeyInfo)] || e.certs[sha256.Sum256(c.Raw)] {
				return true
			}
		}
	}

	return false
}

// Presented is the first verified chain's (else the peer's) keys as
// "sha256/<base64>" pins, leaf first.
func Presented(cs tls.ConnectionState) []string {
	chain := cs.PeerCertificates
	if len(cs.VerifiedChains) > 0 {
		chain = cs.VerifiedChains[0]
	}

	out := make([]string, len(chain))
	for i, c := range chain {
		out[i] = spkiPin(c)
	}

	return out
}

// spkiPin is c's key as a "sha256/<base64>" pin.
func spkiPin(c *x509.Certificate) string {
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return "sha256/" + base64.StdEncoding.EncodeToString(sum[:])
}

// Role is a certificate's place in a verified chain: the root's CertRole,
// with the same values.
type Role int

// Roles: a chain runs from the leaf through intermediates to its root.
const (
	Leaf Role = iota
	Intermediate
	Root
)

// Cert is a certificate of a verified chain and the values that pin it:
// the root's CertPin.
type Cert struct {
	Role        Role
	Subject     string
	Issuer      string
	NotAfter    time.Time
	SPKI        string
	SPKIHex     string
	Fingerprint string
}

// Chain returns cs's first verified chain, leaf first, with its pins.
func Chain(cs *tls.ConnectionState) ([]Cert, error) {
	if len(cs.VerifiedChains) == 0 {
		return nil, errors.New("mimicry: the connection has no verified chain")
	}

	chain := cs.VerifiedChains[0]
	out := make([]Cert, len(chain))

	for i, c := range chain {
		role := Intermediate
		switch {
		case i == 0:
			role = Leaf
		case i == len(chain)-1:
			role = Root
		}

		spki := sha256.Sum256(c.RawSubjectPublicKeyInfo)
		fp := sha256.Sum256(c.Raw)
		hexes := make([]string, len(fp))

		for j, b := range fp {
			hexes[j] = fmt.Sprintf("%02X", b)
		}

		out[i] = Cert{
			Role:        role,
			Subject:     c.Subject.String(),
			Issuer:      c.Issuer.String(),
			NotAfter:    c.NotAfter,
			SPKI:        "sha256/" + base64.StdEncoding.EncodeToString(spki[:]),
			SPKIHex:     hex.EncodeToString(spki[:]),
			Fingerprint: strings.Join(hexes, ":"),
		}
	}

	return out, nil
}
