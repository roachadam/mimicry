package handshake

import (
	"errors"
	"fmt"
	"slices"

	"golang.org/x/crypto/cryptobyte"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// ErrMalformedECH is an ECH config list that does not parse (RFC 9849,
// section 4), from a server's DNS HTTPS record.
var ErrMalformedECH = errors.New("handshake: an ECH config list that does not parse")

// ECHConfig is the ECH config a connection encrypts its inner hello to.
type ECHConfig struct {
	// List is the config as an ECHConfigList of its own.
	List []byte
	// PublicName is the config's public name: the outer hello's
	// server_name, and the name a rejecting server's certificate must
	// be valid for.
	PublicName string
}

// ECHRejectedError is the error of a handshake whose server rejected ECH,
// having proved itself the config's public name: the request goes again
// on a new connection, with RetryConfigs (a list SelectECH reads) or,
// without, none.
type ECHRejectedError struct {
	RetryConfigs []byte
}

func (e *ECHRejectedError) Error() string {
	return "handshake: the server rejected ECH"
}

// echConfigVersion is the version of an RFC 9849 ECHConfig; a list may
// hold configs of other versions, which a client skips.
const echConfigVersion = 0xfe0d

// SelectECH picks the ECH config of list, the ech SvcParam of a server's
// HTTPS record, that the browser of r encrypts its inner hello to (profile
// tls.realEch): the first, in the list's order, with a measured KEM, the
// version RFC 9849 defines and no mandatory extension (RFC 9849, section
// 6.1). It returns nil for a list with none the browser uses, which is
// then sent the ClientHello as to any server. A list that does not parse
// is ErrMalformedECH. A config the browser was never seen to use or pass
// over, ahead of the one picked, and a picked config whose first HPKE
// suite (the one the browser takes) is not among r.CipherSuites, are
// ErrNotMeasured: what the browser would send is unknown.
func SelectECH(list []byte, r *profile.RealECH) (*ECHConfig, error) {
	s := cryptobyte.String(list)

	var configs cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&configs) || !s.Empty() || configs.Empty() {
		return nil, ErrMalformedECH
	}

	for !configs.Empty() {
		var (
			version  uint16
			contents cryptobyte.String
		)

		start := configs
		if !configs.ReadUint16(&version) || !configs.ReadUint16LengthPrefixed(&contents) {
			return nil, ErrMalformedECH
		}

		raw := start[:len(start)-len(configs)]

		if version != echConfigVersion {
			continue
		}

		c, err := parseECHConfig(contents)
		if err != nil {
			return nil, err
		}

		switch {
		case c.mandatory:
			continue
		case slices.Contains(r.SkippedKEMs, c.kem):
			continue
		case !slices.Contains(r.KEMs, c.kem):
			return nil, fmt.Errorf("%w: an ECH config with KEM %s, which the profile's browser was never seen to use or pass over (tls.realEch.kems)", headerorder.ErrNotMeasured, c.kem)
		}

		if !slices.Contains(r.CipherSuites, c.suites[0]) {
			return nil, fmt.Errorf("%w: an ECH config whose first HPKE suite is %s/%s, which the profile's browser was never seen to use (tls.realEch.cipherSuites)", headerorder.ErrNotMeasured, c.suites[0].KDF, c.suites[0].AEAD)
		}

		var b cryptobyte.Builder

		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(raw)
		})

		return &ECHConfig{
			List:       b.BytesOrPanic(),
			PublicName: c.publicName,
		}, nil
	}

	return nil, nil //nolint:nilnil // nil: no config the browser uses
}

// echConfig is what SelectECH reads of an ECHConfig.
type echConfig struct {
	kem        profile.Code
	suites     []profile.HPKESuite
	publicName string
	// mandatory reports an extension the client must understand (the top
	// bit of its type, RFC 9849, section 4.2); none is defined.
	mandatory bool
}

// parseECHConfig reads the contents of a version 0xfe0d ECHConfig.
func parseECHConfig(s cryptobyte.String) (echConfig, error) {
	var (
		c                       echConfig
		configID                uint8
		kem                     uint16
		key, suites, name, exts cryptobyte.String
		maxNameLength           uint8
	)

	if !s.ReadUint8(&configID) || !s.ReadUint16(&kem) || !s.ReadUint16LengthPrefixed(&key) || key.Empty() ||
		!s.ReadUint16LengthPrefixed(&suites) || suites.Empty() || len(suites)%4 != 0 ||
		!s.ReadUint8(&maxNameLength) || !s.ReadUint8LengthPrefixed(&name) || name.Empty() ||
		!s.ReadUint16LengthPrefixed(&exts) || !s.Empty() {
		return c, ErrMalformedECH
	}

	c.kem = profile.Code(kem)
	c.publicName = string(name)

	for !suites.Empty() {
		var kdf, aead uint16

		suites.ReadUint16(&kdf)
		suites.ReadUint16(&aead)

		c.suites = append(c.suites, profile.HPKESuite{
			KDF:  profile.Code(kdf),
			AEAD: profile.Code(aead),
		})
	}

	for !exts.Empty() {
		var (
			typ  uint16
			body cryptobyte.String
		)

		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&body) {
			return c, ErrMalformedECH
		}

		c.mandatory = c.mandatory || typ&0x8000 != 0
	}

	return c, nil
}
