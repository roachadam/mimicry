package mimicry

import (
	"context"
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
// keys: the verified chain (leaf, intermediate or trusted root) must
// contain a certificate matching one of SPKI. Pinning the intermediate is
// usual: it outlives leaf renewals. FetchPins lists a site's chain.
type Pin struct {
	// Host is a host name ("www.example.com") or IP address, in any case;
	// no scheme, port or wildcard.
	Host string
	// IncludeSubdomains extends the pin to every host under Host. A Pin
	// for the request's own host wins over one for a parent, and the
	// nearest parent over others.
	IncludeSubdomains bool
	// SPKI are the accepted keys, any of which matches, each one of:
	//   - "sha256/<base64>": the SHA-256 of a certificate's
	//     SubjectPublicKeyInfo, base64 (HPKP's form, and CertPin.SPKI);
	//   - that hash in hex (CertPin.SPKIHex);
	//   - a certificate's SHA-256 fingerprint in hex, colons optional
	//     (CertPin.Fingerprint, as browsers show it).
	// Hex values match either form. At least one is required.
	SPKI []string
}

// PinError is a connection to a pinned host whose verified chain matched
// none of its pins. Nothing was sent on it.
type PinError struct {
	// Host is the request's host.
	Host string
	// Presented are the chain's keys as "sha256/<base64>" pins, leaf
	// first.
	Presented []string
}

func (e *PinError) Error() string {
	return fmt.Sprintf("mimicry: no pin for %s matches its certificate chain (%s)", e.Host, strings.Join(e.Presented, ", "))
}

// pinEntry is a parsed Pin: the SHA-256 hashes it accepts, of keys and of
// whole certificates.
type pinEntry struct {
	subdomains bool
	spki       map[[sha256.Size]byte]bool
	certs      map[[sha256.Size]byte]bool
}

// pinSet is Config.Pins by host.
type pinSet map[string]*pinEntry

// parsePins checks and parses pins.
func parsePins(pins []Pin) (pinSet, error) {
	set := make(pinSet, len(pins))
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
		e := &pinEntry{
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

// lookup returns the entry covering host: its own, else the nearest
// parent's that includes subdomains; nil when none does.
func (s pinSet) lookup(host string) *pinEntry {
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

// matches reports whether a certificate of one of cs's verified chains
// has a key or fingerprint e accepts.
func (e *pinEntry) matches(cs tls.ConnectionState) bool {
	for _, chain := range cs.VerifiedChains {
		for _, c := range chain {
			if e.spki[sha256.Sum256(c.RawSubjectPublicKeyInfo)] || e.certs[sha256.Sum256(c.Raw)] {
				return true
			}
		}
	}

	return false
}

// verifier is the engine's check of a connection for a request to host:
// the host's pins, then Config.VerifyConnection.
func (t *Transport) verifier(user func(tls.ConnectionState) error) func(host string, cs tls.ConnectionState) error {
	if len(t.pins) == 0 && user == nil {
		return nil
	}

	return func(host string, cs tls.ConnectionState) error {
		if e := t.pins.lookup(host); e != nil && !e.matches(cs) {
			return &PinError{
				Host:      host,
				Presented: presented(cs),
			}
		}
		if user != nil {
			return user(cs)
		}

		return nil
	}
}

// presented is the first verified chain's (else the peer's) keys as
// "sha256/<base64>" pins, leaf first.
func presented(cs tls.ConnectionState) []string {
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

// CertRole is a certificate's place in a verified chain.
type CertRole int

// A chain runs from the server's certificate (the leaf) through
// intermediates to the root that verified it. A leaf that is itself
// trusted (a chain of one) is a leaf.
const (
	CertLeaf CertRole = iota
	CertIntermediate
	CertRoot
)

func (r CertRole) String() string {
	switch r {
	case CertLeaf:
		return "leaf"
	case CertIntermediate:
		return "intermediate"
	case CertRoot:
		return "root"
	}

	return fmt.Sprintf("CertRole(%d)", int(r))
}

// CertPin is a certificate of a site's verified chain, and the values
// that pin it (Pin.SPKI takes any of the three).
type CertPin struct {
	Role     CertRole
	Subject  string
	Issuer   string
	NotAfter time.Time
	// SPKI is the SHA-256 of its SubjectPublicKeyInfo as
	// "sha256/<base64>"; SPKIHex the same hash in hex. They survive a
	// certificate's renewal with the same key.
	SPKI    string
	SPKIHex string
	// Fingerprint is the SHA-256 of the whole certificate, hex bytes
	// joined by colons; it changes with every renewal.
	Fingerprint string
}

// FetchPins connects to rawURL's host over TCP as a request would (cfg's
// proxy, dialer, roots and the profile's TLS hello) and returns the
// verified chain, leaf first, with its pins. It sends no request and
// ignores cfg.Pins and cfg.VerifyConnection.
func FetchPins(ctx context.Context, cfg Config, rawURL string) ([]CertPin, error) {
	cfg.Pins = nil
	cfg.VerifyConnection = nil
	t, err := NewTransport(cfg)
	if err != nil {
		return nil, err
	}
	defer t.CloseIdleConnections()
	e, ok := t.engine.(interface {
		Handshake(ctx context.Context, rawURL string) (*tls.ConnectionState, error)
	})
	if !ok {
		return nil, errors.New("mimicry: the transport's engine makes no bare handshakes")
	}
	cs, err := e.Handshake(ctx, rawURL)
	if err != nil {
		return nil, publicError(err)
	}
	if len(cs.VerifiedChains) == 0 {
		return nil, errors.New("mimicry: the connection has no verified chain")
	}
	chain := cs.VerifiedChains[0]
	out := make([]CertPin, len(chain))
	for i, c := range chain {
		role := CertIntermediate
		switch {
		case i == 0:
			role = CertLeaf
		case i == len(chain)-1:
			role = CertRoot
		}
		spki := sha256.Sum256(c.RawSubjectPublicKeyInfo)
		fp := sha256.Sum256(c.Raw)
		hexes := make([]string, len(fp))
		for j, b := range fp {
			hexes[j] = fmt.Sprintf("%02X", b)
		}
		out[i] = CertPin{
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
