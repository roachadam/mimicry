package server

import (
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

// pskIdentities returns how many identities a ClientHello's pre_shared_key
// extension offers and a short hash of the first (the ticket), or 0 and ""
// without one.
func pskIdentities(msg []byte) (int, string) {
	s := cryptobyte.String(msg)
	var body, sessionID, suites, compression, exts cryptobyte.String
	if !s.Skip(1) || !s.ReadUint24LengthPrefixed(&body) ||
		!body.Skip(2+32) || !body.ReadUint8LengthPrefixed(&sessionID) ||
		!body.ReadUint16LengthPrefixed(&suites) || !body.ReadUint8LengthPrefixed(&compression) ||
		!body.ReadUint16LengthPrefixed(&exts) {
		return 0, ""
	}
	for !exts.Empty() {
		var typ uint16
		var data cryptobyte.String
		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return 0, ""
		}
		if code(typ) != profile.ExtPreSharedKey {
			continue
		}
		var ids cryptobyte.String
		if !data.ReadUint16LengthPrefixed(&ids) {
			return 0, ""
		}
		n, first := 0, ""
		for !ids.Empty() {
			var id cryptobyte.String
			if !ids.ReadUint16LengthPrefixed(&id) || !ids.Skip(4) {
				return 0, ""
			}
			if n == 0 {
				sum := sha256.Sum256(id)
				first = hex.EncodeToString(sum[:6])
			}
			n++
		}
		return n, first
	}
	return 0, ""
}

// ParseClientHello turns a ClientHello handshake message into the TLS section
// of a profile. GREASE values become profile.GREASE, and extension bodies are
// decoded into the profile's typed fields where it has one.
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
	for !exts.Empty() {
		var typ uint16
		var data cryptobyte.String
		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return profile.TLS{}, errMalformed
		}
		e, err := parseExtension(code(typ), data)
		if err != nil {
			return profile.TLS{}, fmt.Errorf("extension %s: %w", code(typ), err)
		}
		t.Extensions = append(t.Extensions, e)
	}
	return t, nil
}

func parseExtension(typ profile.Code, data cryptobyte.String) (profile.Extension, error) {
	e := profile.Extension{
		Type: typ,
	}
	var err error
	switch typ {
	case profile.ExtServerName, profile.ExtPadding, profile.ExtPreSharedKey, profile.ExtEncryptedClientHello:
		// Generated per connection; the profile records only the position.
		return e, nil
	case profile.ExtSupportedGroups:
		e.Groups, err = readCodeList16(&data)
	case profile.ExtSignatureAlgorithms, profile.ExtSignatureAlgorithmsCert, profile.ExtDelegatedCredentials:
		e.SignatureAlgorithms, err = readCodeList16(&data)
	case profile.ExtKeyShare:
		e.KeyShares, err = readKeyShareGroups(&data)
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

// readKeyShareGroups reads a key_share client body and keeps only the groups.
func readKeyShareGroups(s *cryptobyte.String) ([]profile.Code, error) {
	var shares cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&shares) {
		return nil, errMalformed
	}
	var groups []profile.Code
	for !shares.Empty() {
		var group uint16
		var key cryptobyte.String
		if !shares.ReadUint16(&group) || !shares.ReadUint16LengthPrefixed(&key) {
			return nil, errMalformed
		}
		groups = append(groups, code(group))
	}
	return groups, nil
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
