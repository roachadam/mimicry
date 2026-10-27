package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/cryptobyte"

	"github.com/roachadam/mimicry/profile"
)

const (
	recordTypeHandshake  = 22
	recordHeaderLen      = 5
	maxRecordLen         = 1<<14 + 256 // plaintext limit plus slack for odd clients
	handshakeClientHello = 1
	// maxClientHelloLen bounds what a peer can make us buffer. Real hellos are a
	// few kilobytes, even with post-quantum key shares.
	maxClientHelloLen = 1 << 16
)

// readClientHello reads TLS records from r until it has a whole ClientHello
// handshake message, which a client may split across several records. It
// returns every byte read, so the connection can be replayed into a TLS server,
// and the ClientHello message itself (handshake header included).
func readClientHello(r io.Reader) (raw, msg []byte, err error) {
	var header [recordHeaderLen]byte
	need := -1
	for need < 0 || len(msg) < need {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return nil, nil, fmt.Errorf("reading record header: %w", err)
		}
		if header[0] != recordTypeHandshake {
			return nil, nil, fmt.Errorf("record type %d is not a handshake", header[0])
		}
		n := int(header[3])<<8 | int(header[4])
		if n == 0 || n > maxRecordLen {
			return nil, nil, fmt.Errorf("record length %d out of range", n)
		}
		fragment := make([]byte, n)
		if _, err := io.ReadFull(r, fragment); err != nil {
			return nil, nil, fmt.Errorf("reading record: %w", err)
		}
		raw = append(raw, header[:]...)
		raw = append(raw, fragment...)
		msg = append(msg, fragment...)
		if need < 0 && len(msg) >= 4 {
			if msg[0] != handshakeClientHello {
				return nil, nil, fmt.Errorf("handshake type %d is not a ClientHello", msg[0])
			}
			need = 4 + (int(msg[1])<<16 | int(msg[2])<<8 | int(msg[3]))
			if need > maxClientHelloLen {
				return nil, nil, fmt.Errorf("ClientHello length %d exceeds %d", need, maxClientHelloLen)
			}
		}
	}
	return raw, msg[:need], nil
}

var errMalformed = errors.New("malformed ClientHello")

// offer is what a ClientHello offers to resume: TLS 1.3 tickets
// (pre_shared_key identities: how many, and a short hash of the first) or a
// TLS 1.2 session ticket (a short hash of it).
type offer struct {
	pskIdentities int
	pskIdentity   string
	ticket        string
}

// resumptionOffer reads msg's offer; zero for a full handshake.
func resumptionOffer(msg []byte) offer {
	var o offer
	s := cryptobyte.String(msg)
	var body, sessionID, suites, compression, exts cryptobyte.String
	if !s.Skip(1) || !s.ReadUint24LengthPrefixed(&body) ||
		!body.Skip(2+32) || !body.ReadUint8LengthPrefixed(&sessionID) ||
		!body.ReadUint16LengthPrefixed(&suites) || !body.ReadUint8LengthPrefixed(&compression) ||
		!body.ReadUint16LengthPrefixed(&exts) {
		return o
	}
	short := func(b []byte) string {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:6])
	}
	for !exts.Empty() {
		var typ uint16
		var data cryptobyte.String
		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return offer{}
		}
		switch code(typ) {
		case profile.ExtSessionTicket:
			if len(data) > 0 {
				o.ticket = short(data)
			}
		case profile.ExtPreSharedKey:
			var ids cryptobyte.String
			if !data.ReadUint16LengthPrefixed(&ids) {
				return offer{}
			}
			for !ids.Empty() {
				var id cryptobyte.String
				if !ids.ReadUint16LengthPrefixed(&id) || !ids.Skip(4) {
					return offer{}
				}
				if o.pskIdentities == 0 {
					o.pskIdentity = short(id)
				}
				o.pskIdentities++
			}
		default:
			// Only the resumption extensions make the offer.
		}
	}
	return o
}

// ParseClientHello turns a ClientHello handshake message into the TLS section
// of a profile. Per-connection random values are left out, their lengths
// kept. A padding extension must be zeros and follow BoringSSL's rule, the
// only one a profile can describe.
func ParseClientHello(msg []byte) (profile.TLS, error) {
	s := cryptobyte.String(msg)
	var msgType uint8
	var body cryptobyte.String
	if !s.ReadUint8(&msgType) || msgType != handshakeClientHello ||
		!s.ReadUint24LengthPrefixed(&body) || !s.Empty() {
		return profile.TLS{}, errMalformed
	}
	var legacyVersion uint16
	var sessionID, suites, compression cryptobyte.String
	if !body.ReadUint16(&legacyVersion) ||
		!body.Skip(32) ||
		!body.ReadUint8LengthPrefixed(&sessionID) ||
		!body.ReadUint16LengthPrefixed(&suites) ||
		!body.ReadUint8LengthPrefixed(&compression) {
		return profile.TLS{}, errMalformed
	}
	t := profile.TLS{
		LegacyVersion:      profile.Code(legacyVersion),
		SessionIDLength:    uint8(len(sessionID)),
		CompressionMethods: profile.ByteList(append([]byte(nil), compression...)),
	}
	var err error
	if t.CipherSuites, err = readCodes(&suites); err != nil {
		return profile.TLS{}, fmt.Errorf("cipher suites: %w", err)
	}
	if body.Empty() {
		return t, nil
	}
	var exts cryptobyte.String
	if !body.ReadUint16LengthPrefixed(&exts) || !body.Empty() {
		return profile.TLS{}, errMalformed
	}
	padding := -1
	for !exts.Empty() {
		var typ uint16
		var data cryptobyte.String
		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return profile.TLS{}, errMalformed
		}
		if typ == uint16(profile.ExtPadding) {
			padding = len(data)
		}
		e, err := parseExtension(code(typ), data)
		if err != nil {
			return profile.TLS{}, fmt.Errorf("extension %s: %w", code(typ), err)
		}
		t.Extensions = append(t.Extensions, e)
	}
	if padding >= 0 {
		if want, pads := boringPadding(len(msg) - 4 - padding); !pads || want != padding {
			return profile.TLS{}, fmt.Errorf("extension %s: %d bytes of padding are not BoringSSL's for a %d-byte ClientHello, and a profile cannot describe another rule",
				profile.ExtPadding, padding, len(msg)-4-padding)
		}
	}
	return t, nil
}

// boringPadding is BoringSSL's padding rule, which uTLS implements
// (BoringPaddingStyle): a ClientHello message of 256 to 511 bytes without
// padding is padded to 512, with at least one byte of padding. It returns
// the padding's length, and whether to pad.
func boringPadding(unpadded int) (int, bool) {
	if unpadded <= 0xff || unpadded >= 0x200 {
		return 0, false
	}
	n := 0x200 - unpadded
	if n >= 4+1 {
		return n - 4, true
	}
	return 1, true
}

// ParseRecords splits the TLS records that carried a ClientHello into the
// record-layer version and the handshake message. Every record must carry
// the same version, and nothing may follow the message.
func ParseRecords(records []byte) (uint16, []byte, error) {
	raw, msg, err := readClientHello(bytes.NewReader(records))
	if err != nil {
		return 0, nil, err
	}
	if len(raw) != len(records) {
		return 0, nil, fmt.Errorf("%d bytes follow the ClientHello's records", len(records)-len(raw))
	}
	version := uint16(raw[1])<<8 | uint16(raw[2])
	for rest := raw; len(rest) > 0; {
		if v := uint16(rest[1])<<8 | uint16(rest[2]); v != version {
			return 0, nil, fmt.Errorf("records carry versions %#04x and %#04x", version, v)
		}
		rest = rest[recordHeaderLen+(int(rest[3])<<8|int(rest[4])):]
	}
	return version, msg, nil
}

func parseExtension(typ profile.Code, data cryptobyte.String) (profile.Extension, error) {
	e := profile.Extension{
		Type: typ,
	}
	var err error
	switch typ {
	case profile.ExtServerName, profile.ExtSessionTicket, profile.ExtPreSharedKey:
		// Generated per connection; the profile records only the position.
		return e, nil
	case profile.ExtPadding:
		// Its length is checked against the whole message.
		for _, b := range data {
			if b != 0 {
				return e, errors.New("padding that is not zeros")
			}
		}
		return e, nil
	case profile.ExtEncryptedClientHello:
		e.ECH, err = readGREASEECH(&data)
	case profile.ExtSupportedGroups:
		e.Groups, err = readCodeList16(&data)
	case profile.ExtSignatureAlgorithms, profile.ExtSignatureAlgorithmsCert, profile.ExtDelegatedCredentials:
		e.SignatureAlgorithms, err = readCodeList16(&data)
	case profile.ExtKeyShare:
		e.KeyShares, e.KeyShareLengths, err = readKeyShares(&data)
	case profile.ExtSupportedVersions:
		var list cryptobyte.String
		if !data.ReadUint8LengthPrefixed(&list) {
			return e, errMalformed
		}
		e.Versions, err = readCodes(&list)
	case profile.ExtALPN, profile.ExtApplicationSettingsOld, profile.ExtApplicationSettings:
		var protocols []string
		protocols, err = readProtocols(&data)
		if typ == profile.ExtALPN {
			e.ALPN = protocols
		} else {
			e.ALPS = protocols
		}
	case profile.ExtECPointFormats:
		e.PointFormats, err = readByteList8(&data)
	case profile.ExtPSKKeyExchangeModes:
		e.PSKModes, err = readByteList8(&data)
	case profile.ExtCompressCertificate:
		var list cryptobyte.String
		if !data.ReadUint8LengthPrefixed(&list) {
			return e, errMalformed
		}
		e.CertCompression, err = readCodes(&list)
	case profile.ExtRecordSizeLimit:
		if !data.ReadUint16(&e.RecordSizeLimit) {
			return e, errMalformed
		}
	default:
		if len(data) > 0 {
			e.Data = profile.Bytes(append([]byte(nil), data...))
		}
		return e, nil
	}
	if err != nil {
		return e, err
	}
	if !data.Empty() {
		return e, errors.New("trailing bytes in extension body")
	}
	return e, nil
}

// code maps a wire value to a profile code, folding GREASE values together.
func code(v uint16) profile.Code {
	if profile.IsGREASE(v) {
		return profile.GREASE
	}
	return profile.Code(v)
}

// readCodes reads 16-bit values until s is empty.
func readCodes(s *cryptobyte.String) ([]profile.Code, error) {
	var codes []profile.Code
	for !s.Empty() {
		var v uint16
		if !s.ReadUint16(&v) {
			return nil, errMalformed
		}
		codes = append(codes, code(v))
	}
	return codes, nil
}

// readCodeList16 reads a 16-bit-length-prefixed list of 16-bit values.
func readCodeList16(s *cryptobyte.String) ([]profile.Code, error) {
	var list cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&list) {
		return nil, errMalformed
	}
	return readCodes(&list)
}

// readKeyShares reads a key_share client body: each share's group and the
// length of its key.
func readKeyShares(s *cryptobyte.String) ([]profile.Code, []uint16, error) {
	var shares cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&shares) {
		return nil, nil, errMalformed
	}
	var groups []profile.Code
	var lengths []uint16
	for !shares.Empty() {
		var group uint16
		var key cryptobyte.String
		if !shares.ReadUint16(&group) || !shares.ReadUint16LengthPrefixed(&key) {
			return nil, nil, errMalformed
		}
		groups = append(groups, code(group))
		lengths = append(lengths, uint16(len(key)))
	}
	return groups, lengths, nil
}

// echOuter is the ECHClientHello type of an outer ClientHello's extension,
// the only one a ClientHello on the wire carries.
const echOuter = 0

// readGREASEECH reads an outer encrypted_client_hello body: its cipher
// suite, and the lengths of its encapsulated key and payload. The config_id
// and the bytes are random per connection and not kept.
func readGREASEECH(s *cryptobyte.String) (*profile.ECH, error) {
	var typ uint8
	var kdf, aead uint16
	var enc, payload cryptobyte.String
	if !s.ReadUint8(&typ) {
		return nil, errMalformed
	}
	if typ != echOuter {
		return nil, fmt.Errorf("ECH type %d is not an outer ClientHello's", typ)
	}
	if !s.ReadUint16(&kdf) || !s.ReadUint16(&aead) ||
		!s.Skip(1) || // config_id
		!s.ReadUint16LengthPrefixed(&enc) ||
		!s.ReadUint16LengthPrefixed(&payload) {
		return nil, errMalformed
	}
	return &profile.ECH{
		CipherSuites: []profile.HPKESuite{
			{
				KDF:  profile.Code(kdf),
				AEAD: profile.Code(aead),
			},
		},
		EncLength: uint16(len(enc)),
		PayloadLengths: []uint16{
			uint16(len(payload)),
		},
	}, nil
}

// readProtocols reads an ALPN-style list: 16-bit total length, then
// 8-bit-length-prefixed names.
func readProtocols(s *cryptobyte.String) ([]string, error) {
	var list cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&list) {
		return nil, errMalformed
	}
	var protocols []string
	for !list.Empty() {
		var name cryptobyte.String
		if !list.ReadUint8LengthPrefixed(&name) {
			return nil, errMalformed
		}
		protocols = append(protocols, string(name))
	}
	return protocols, nil
}

// readByteList8 reads an 8-bit-length-prefixed list of bytes.
func readByteList8(s *cryptobyte.String) (profile.ByteList, error) {
	var list cryptobyte.String
	if !s.ReadUint8LengthPrefixed(&list) {
		return nil, errMalformed
	}
	return profile.ByteList(append([]byte(nil), list...)), nil
}
