package quictrace

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
)

// QUIC versions this package decodes.
const (
	Version1 uint32 = 0x00000001 // RFC 9000
	Version2 uint32 = 0x6b3343cf // RFC 9369
)

// initialSalts are each version's salt for its Initial secrets (RFC 9001,
// section 5.2; RFC 9369, section 3.3.1).
var initialSalts = map[uint32][]byte{
	Version1: {0x38, 0x76, 0x2c, 0xf7, 0xf5, 0x59, 0x34, 0xb3, 0x4d, 0x17, 0x9a, 0xe6, 0xa4, 0xc8, 0x0c, 0xad, 0xcc, 0xbb, 0x7f, 0x0a},
	Version2: {0x0d, 0xed, 0xe3, 0xde, 0xf7, 0x00, 0xa6, 0xdb, 0x81, 0x93, 0x81, 0xbe, 0x6e, 0x26, 0x9d, 0xcb, 0xf9, 0xbd, 0x2e, 0xd9},
}

// labelPrefix is the prefix of a version's packet protection labels: "quic
// key", "quic iv", "quic hp" and "quic ku" in version 1, "quicv2 ..." in
// version 2.
func labelPrefix(version uint32) string {
	if version == Version2 {
		return "quicv2 "
	}
	return "quic "
}

// suite is a TLS 1.3 cipher suite as QUIC packet protection uses it.
type suite struct {
	hash   func() hash.Hash
	keyLen int
	aead   func(key []byte) (cipher.AEAD, error)
	hp     func(key []byte) (headerMask, error)
}

// headerMask computes a header protection mask from a sample of the
// protected payload (RFC 9001, section 5.4).
type headerMask func(sample []byte) [5]byte

// TLS 1.3 cipher suites.
const (
	tlsAES128GCMSHA256        uint16 = 0x1301
	tlsAES256GCMSHA384        uint16 = 0x1302
	tlsChaCha20Poly1305SHA256 uint16 = 0x1303
)

var suites = map[uint16]suite{
	tlsAES128GCMSHA256: {
		hash:   sha256.New,
		keyLen: 16,
		aead:   aesGCM,
		hp:     aesMask,
	},
	tlsAES256GCMSHA384: {
		hash:   sha512.New384,
		keyLen: 32,
		aead:   aesGCM,
		hp:     aesMask,
	},
	tlsChaCha20Poly1305SHA256: {
		hash:   sha256.New,
		keyLen: 32,
		aead:   chacha20poly1305.New,
		hp:     chachaMask,
	},
}

// suiteOrder is the order suites are tried in when a secret's suite is not
// known: a secret's length tells SHA-384 from SHA-256, and an AEAD that
// authenticates the first packet tells the rest.
var suiteOrder = []uint16{
	tlsAES128GCMSHA256,
	tlsChaCha20Poly1305SHA256,
	tlsAES256GCMSHA384,
}

func aesGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func aesMask(key []byte) (headerMask, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return func(sample []byte) [5]byte {
		var out [16]byte
		block.Encrypt(out[:], sample)
		return [5]byte(out[:5])
	}, nil
}

func chachaMask(key []byte) (headerMask, error) {
	if len(key) != chacha20.KeySize {
		return nil, errors.New("bad ChaCha20 key length")
	}
	k := append([]byte(nil), key...)
	return func(sample []byte) [5]byte {
		var out [5]byte
		c, err := chacha20.NewUnauthenticatedCipher(k, sample[4:16])
		if err != nil {
			return out
		}
		c.SetCounter(binary.LittleEndian.Uint32(sample[:4]))
		c.XORKeyStream(out[:], out[:])
		return out
	}, nil
}

// expandLabel is TLS 1.3's HKDF-Expand-Label with an empty context (RFC
// 8446, section 7.1).
func expandLabel(h func() hash.Hash, secret []byte, label string, length int) ([]byte, error) {
	return expandLabelContext(h, secret, label, nil, length)
}

// expandLabelContext is HKDF-Expand-Label with a context.
func expandLabelContext(h func() hash.Hash, secret []byte, label string, context []byte, length int) ([]byte, error) {
	full := "tls13 " + label
	info := make([]byte, 0, 4+len(full)+len(context))
	info = binary.BigEndian.AppendUint16(info, uint16(length)) //nolint:gosec // key, IV and secret lengths
	info = append(info, byte(len(full)))                       //nolint:gosec // labels are short
	info = append(info, full...)
	info = append(info, byte(len(context))) //nolint:gosec // a transcript hash
	info = append(info, context...)
	return hkdf.Expand(h, secret, string(info), length)
}

// clientEarlyTrafficSecret derives client_early_traffic_secret from a
// resumption PSK and the ClientHello offering it (RFC 8446, section 7.1):
// Derive-Secret(HKDF-Extract(0, PSK), "c e traffic", ClientHello).
func clientEarlyTrafficSecret(suiteID uint16, psk, clientHello []byte) ([]byte, error) {
	s, ok := suites[suiteID]
	if !ok {
		return nil, fmt.Errorf("unknown cipher suite %#04x", suiteID)
	}
	early, err := hkdf.Extract(s.hash, psk, nil)
	if err != nil {
		return nil, err
	}
	transcript := s.hash()
	transcript.Write(clientHello)
	return expandLabelContext(s.hash, early, "c e traffic", transcript.Sum(nil), transcript.Size())
}

// keys protect one direction of one packet number space (or one key phase
// of 1-RTT).
type keys struct {
	suite   suite
	version uint32
	secret  []byte
	aead    cipher.AEAD
	iv      []byte
	mask    headerMask
}

// newKeys derives packet protection keys from a traffic secret (RFC 9001,
// section 5.1).
func newKeys(s suite, version uint32, secret []byte) (*keys, error) {
	prefix := labelPrefix(version)
	key, err := expandLabel(s.hash, secret, prefix+"key", s.keyLen)
	if err != nil {
		return nil, err
	}
	iv, err := expandLabel(s.hash, secret, prefix+"iv", 12)
	if err != nil {
		return nil, err
	}
	hpKey, err := expandLabel(s.hash, secret, prefix+"hp", s.keyLen)
	if err != nil {
		return nil, err
	}
	aead, err := s.aead(key)
	if err != nil {
		return nil, err
	}
	mask, err := s.hp(hpKey)
	if err != nil {
		return nil, err
	}
	return &keys{
		suite:   s,
		version: version,
		secret:  secret,
		aead:    aead,
		iv:      iv,
		mask:    mask,
	}, nil
}

// next derives the keys of the following key phase (RFC 9001, section
// 6.1); header protection keys do not change.
func (k *keys) next() (*keys, error) {
	secret, err := expandLabel(k.suite.hash, k.secret, labelPrefix(k.version)+"ku", len(k.secret))
	if err != nil {
		return nil, err
	}
	n, err := newKeys(k.suite, k.version, secret)
	if err != nil {
		return nil, err
	}
	n.mask = k.mask
	return n, nil
}

// clientInitialKeys derives the keys protecting a client's Initial packets
// from the Destination Connection ID of its first Initial (RFC 9001,
// section 5.2).
func clientInitialKeys(version uint32, dcid []byte) (*keys, error) {
	salt, ok := initialSalts[version]
	if !ok {
		return nil, fmt.Errorf("no Initial salt for version %#08x", version)
	}
	initial, err := hkdf.Extract(sha256.New, dcid, salt)
	if err != nil {
		return nil, err
	}
	secret, err := expandLabel(sha256.New, initial, "client in", sha256.Size)
	if err != nil {
		return nil, err
	}
	return newKeys(suites[tlsAES128GCMSHA256], version, secret)
}

// keysForSecret derives keys from a traffic secret of an unknown suite,
// keeping the first whose AEAD authenticates the packet p (as unprotect
// reads it); it returns nil if none does.
func keysForSecret(version uint32, secret []byte, p []byte, pnOffset int, short bool, largest int64) *keys {
	for _, id := range suiteOrder {
		s := suites[id]
		if s.hash().Size() != len(secret) {
			continue
		}
		k, err := newKeys(s, version, secret)
		if err != nil {
			continue
		}
		if _, err := k.unprotect(p, pnOffset, short, largest); err == nil {
			return k
		}
	}
	return nil
}

// opened is a packet with its protection removed.
type opened struct {
	firstByte byte
	pnLen     int
	pn        uint64
	payload   []byte
}

// errShortPacket is a packet too short to carry a header protection
// sample.
var errShortPacket = errors.New("packet too short for header protection")

// unprotect removes header protection from p (a copy is made), decodes the
// packet number against the largest one received in its space (-1 for
// none), and opens the payload. pnOffset is where the packet number
// starts; short is a short-header (1-RTT) packet.
func (k *keys) unprotect(p []byte, pnOffset int, short bool, largest int64) (opened, error) {
	if len(p) < pnOffset+4+16 {
		return opened{}, errShortPacket
	}
	mask := k.mask(p[pnOffset+4 : pnOffset+4+16])
	first := p[0]
	if short {
		first ^= mask[0] & 0x1f
	} else {
		first ^= mask[0] & 0x0f
	}
	pnLen := int(first&0x03) + 1
	header := append([]byte(nil), p[:pnOffset+pnLen]...)
	header[0] = first
	var truncated uint64
	for i := range pnLen {
		header[pnOffset+i] ^= mask[1+i] //nolint:gosec // pnLen is at most 4, mask has 5 bytes
		truncated = truncated<<8 | uint64(header[pnOffset+i])
	}
	pn := decodePacketNumber(largest, truncated, pnLen)
	nonce := append([]byte(nil), k.iv...)
	for i := range 8 {
		nonce[len(nonce)-1-i] ^= byte(pn >> (8 * i)) //nolint:gosec // one byte of pn at a time
	}
	payload, err := k.aead.Open(nil, nonce, p[pnOffset+pnLen:], header)
	if err != nil {
		return opened{}, err
	}
	return opened{
		firstByte: first,
		pnLen:     pnLen,
		pn:        pn,
		payload:   payload,
	}, nil
}

// decodePacketNumber recovers a full packet number from its truncated
// encoding (RFC 9000, appendix A.3).
func decodePacketNumber(largest int64, truncated uint64, pnLen int) uint64 {
	expected := uint64(largest + 1) //nolint:gosec // largest is -1 or a packet number below 2^62
	win := uint64(1) << (8 * pnLen)
	hwin := win / 2
	mask := win - 1
	candidate := (expected &^ mask) | truncated
	switch {
	case candidate+hwin <= expected && candidate < (1<<62)-win:
		return candidate + win
	case candidate > expected+hwin && candidate >= win:
		return candidate - win
	}
	return candidate
}
