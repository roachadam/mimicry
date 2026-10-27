package mimicry

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/roachadam/mimicry/internal/pins"
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

// parsePins checks and parses ps (pins.Parse).
func parsePins(ps []Pin) (pins.Set, error) {
	in := make([]pins.Pin, len(ps))
	for i, p := range ps {
		in[i] = pins.Pin(p)
	}

	return pins.Parse(in)
}

// verifier is the engine's check of a connection for a request to host:
// the host's pins, then Config.VerifyConnection.
func (t *Transport) verifier(user func(tls.ConnectionState) error) func(host string, cs tls.ConnectionState) error {
	if len(t.pins) == 0 && user == nil {
		return nil
	}

	return func(host string, cs tls.ConnectionState) error {
		if e := t.pins.Lookup(host); e != nil && !e.Matches(cs) {
			return &PinError{
				Host:      host,
				Presented: pins.Presented(cs),
			}
		}

		if user != nil {
			return user(cs)
		}

		return nil
	}
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

	certs, err := pins.Chain(cs)
	if err != nil {
		return nil, err
	}

	out := make([]CertPin, len(certs))
	for i, c := range certs {
		out[i] = CertPin{
			Role:        CertRole(c.Role),
			Subject:     c.Subject,
			Issuer:      c.Issuer,
			NotAfter:    c.NotAfter,
			SPKI:        c.SPKI,
			SPKIHex:     c.SPKIHex,
			Fingerprint: c.Fingerprint,
		}
	}

	return out, nil
}
