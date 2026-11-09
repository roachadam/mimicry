// Package tlsflight reads a TLS 1.2 or 1.3 handshake from both sides' bytes
// as they crossed the wire: the server's flight (the CertificateRequest in
// it) and the client's answer (Certificate, CertificateVerify, Finished,
// in order). TLS 1.3's encrypted handshake is decrypted with the server's
// key log (KeyLog, as tls.Config.KeyLogWriter); TLS 1.2's is in the clear
// up to ChangeCipherSpec. The capture server uses it to record how a
// browser answers a request for a client certificate.
package tlsflight

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"

	"golang.org/x/crypto/chacha20poly1305"
)

// TLS versions.
const (
	VersionTLS12 uint16 = 0x0303
	VersionTLS13 uint16 = 0x0304
)

// Handshake message types (RFC 8446, section 4; RFC 5246, section 7.4),
// and ChangeCipherSpec, which Message also stands for (its record is not a
// handshake message, but its place in the flight matters).
const (
	TypeClientHello         uint8 = 1
	TypeServerHello         uint8 = 2
	TypeNewSessionTicket    uint8 = 4
	TypeEncryptedExtensions uint8 = 8
	TypeCertificate         uint8 = 11
	TypeServerKeyExchange   uint8 = 12
	TypeCertificateRequest  uint8 = 13
	TypeServerHelloDone     uint8 = 14
	TypeCertificateVerify   uint8 = 15
	TypeClientKeyExchange   uint8 = 16
	TypeFinished            uint8 = 20
	// TypeChangeCipherSpec is not a handshake type: Message uses it for
	// a ChangeCipherSpec record.
	TypeChangeCipherSpec uint8 = 0xff
)

// TLS record content types.
const (
	recordChangeCipherSpec = 20
	recordAlert            = 21
	recordHandshake        = 22
	recordApplicationData  = 23
	recordHeaderLen        = 5
)

// Message is one handshake message, or a ChangeCipherSpec record
// (TypeChangeCipherSpec, no Body).
type Message struct {
	Type uint8
	// Body is the message without its 4-byte header.
	Body []byte
}

// Name is the message type's name in RFC 8446's and RFC 5246's terms
// ("certificate_verify"), or "type_N" for one this package does not name.
func (m Message) Name() string {
	if n, ok := names[m.Type]; ok {
		return n
	}

	return fmt.Sprintf("type_%d", m.Type)
}

var names = map[uint8]string{
	TypeClientHello:         "client_hello",
	TypeServerHello:         "server_hello",
	TypeNewSessionTicket:    "new_session_ticket",
	TypeEncryptedExtensions: "encrypted_extensions",
	TypeCertificate:         "certificate",
	TypeServerKeyExchange:   "server_key_exchange",
	TypeCertificateRequest:  "certificate_request",
	TypeServerHelloDone:     "server_hello_done",
	TypeCertificateVerify:   "certificate_verify",
	TypeClientKeyExchange:   "client_key_exchange",
	TypeFinished:            "finished",
	TypeChangeCipherSpec:    "change_cipher_spec",
}

// Handshake is one connection's handshake as both sides sent it.
type Handshake struct {
	// Version and Suite are what the ServerHello chose.
	Version uint16
	Suite   uint16
	// ClientHello is the client's first message.
	ClientHello Message
	// Server are the server's messages from its ServerHello to its
	// Finished (TLS 1.3), or to its ChangeCipherSpec (TLS 1.2, whose
	// Finished follows encrypted); Client the client's after its
	// ClientHello, to its Finished (in TLS 1.2 a Finished after
	// ChangeCipherSpec stands for the encrypted record that carries it).
	Server []Message
	Client []Message
	// ClientEncrypted is how many TLS 1.3 records carried the client's
	// encrypted handshake messages (Client but a ChangeCipherSpec): the
	// records before its first application data.
	ClientEncrypted int
}

// Find returns the first message of typ in msgs.
func Find(msgs []Message, typ uint8) (Message, bool) {
	for _, m := range msgs {
		if m.Type == typ {
			return m, true
		}
	}

	return Message{}, false
}

// Read reads a handshake from the bytes the client sent (from its
// ClientHello's first record) and those the server sent (from its
// ServerHello's), each at least to its Finished; whatever follows is
// ignored. TLS 1.3 needs keys, the server's key log, holding the
// connection's handshake traffic secrets. A handshake cut short (a client
// that gave up) returns what was read with the error, once both hellos
// were.
func Read(client, server []byte, keys *KeyLog) (*Handshake, error) {
	cr := &reader{
		b: client,
	}

	hello, err := cr.clear()
	if err != nil {
		return nil, fmt.Errorf("client: %w", err)
	}

	if hello.Type != TypeClientHello || len(hello.Body) < 34 {
		return nil, fmt.Errorf("client: %s instead of a ClientHello", hello.Name())
	}

	sr := &reader{
		b: server,
	}

	sh, err := sr.clear()
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	if sh.Type != TypeServerHello {
		return nil, fmt.Errorf("server: %s instead of a ServerHello", sh.Name())
	}

	h := &Handshake{
		ClientHello: hello,
		Server: []Message{
			sh,
		},
	}

	if h.Version, h.Suite, err = parseServerHello(sh.Body); err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	switch h.Version {
	case VersionTLS13:
		random := hello.Body[2:34]

		err = h.readTLS13(cr, sr, keys, random)

	case VersionTLS12:
		err = h.readTLS12(cr, sr)
	default:
		err = fmt.Errorf("version %#04x", h.Version)
	}

	return h, err
}

// readTLS13 reads both sides' encrypted flights.
func (h *Handshake) readTLS13(cr, sr *reader, keys *KeyLog, random []byte) error {
	if keys == nil {
		return errors.New("TLS 1.3 needs the server's key log")
	}

	for _, side := range []struct {
		name  string
		r     *reader
		label string
		into  *[]Message
	}{
		{"server", sr, "SERVER_HANDSHAKE_TRAFFIC_SECRET", &h.Server},
		{"client", cr, "CLIENT_HANDSHAKE_TRAFFIC_SECRET", &h.Client},
	} {
		secret := keys.Secret(side.label, random)
		if secret == nil {
			return fmt.Errorf("%s: no %s in the key log", side.name, side.label)
		}

		aead, iv, err := trafficKeys(h.Suite, secret)
		if err != nil {
			return err
		}

		side.r.aead, side.r.iv = aead, iv

		for {
			m, err := side.r.encrypted()
			if err != nil {
				return fmt.Errorf("%s: %w", side.name, err)
			}

			*side.into = append(*side.into, m)
			if m.Type == TypeFinished {
				break
			}
		}
	}

	h.ClientEncrypted = cr.opened

	return nil
}

// readTLS12 reads both sides' flights up to ChangeCipherSpec, then takes the
// client's next record as its Finished (it is encrypted).
func (h *Handshake) readTLS12(cr, sr *reader) error {
	for {
		m, err := sr.clear()
		if err != nil {
			return fmt.Errorf("server: %w", err)
		}

		h.Server = append(h.Server, m)
		if m.Type == TypeChangeCipherSpec || m.Type == TypeServerHelloDone {
			break
		}
	}

	for {
		m, err := cr.clear()
		if err != nil {
			return fmt.Errorf("client: %w", err)
		}

		h.Client = append(h.Client, m)
		if m.Type == TypeChangeCipherSpec {
			break
		}
	}

	typ, _, err := cr.record()
	if err != nil {
		return fmt.Errorf("client: after its ChangeCipherSpec: %w", err)
	}

	if typ != recordHandshake {
		return fmt.Errorf("client: record type %d after its ChangeCipherSpec, not its Finished", typ)
	}

	h.Client = append(h.Client, Message{
		Type: TypeFinished,
	})

	return nil
}

// reader reads one side's records, joining handshake messages across
// them.
type reader struct {
	b []byte
	// pending is handshake content read but not yet a whole message.
	pending []byte
	// aead and iv open TLS 1.3 encrypted records; seq numbers them and
	// opened counts those opened.
	aead   cipher.AEAD
	iv     []byte
	seq    uint64
	opened int
}

// record returns the next record's type and content.
func (r *reader) record() (uint8, []byte, error) {
	if len(r.b) < recordHeaderLen {
		return 0, nil, errors.New("the bytes end before the flight did")
	}

	n := int(binary.BigEndian.Uint16(r.b[3:5]))
	if len(r.b) < recordHeaderLen+n {
		return 0, nil, errors.New("the bytes end inside a record")
	}

	typ, header, body := r.b[0], r.b[:recordHeaderLen], r.b[recordHeaderLen:recordHeaderLen+n]

	r.b = r.b[recordHeaderLen+n:]

	if typ == recordAlert {
		return 0, nil, fmt.Errorf("alert %x", body)
	}

	if typ != recordApplicationData || r.aead == nil {
		return typ, body, nil
	}

	// TLS 1.3: the nonce is the IV XORed with the record's sequence
	// number; the additional data is the record header (RFC 8446,
	// section 5.2).
	nonce := bytes.Clone(r.iv)
	for i := range 8 {
		nonce[len(nonce)-1-i] ^= byte(r.seq >> (8 * i))
	}

	plain, err := r.aead.Open(nil, nonce, body, header)
	if err != nil {
		return 0, nil, fmt.Errorf("opening encrypted record %d: %w", r.seq, err)
	}

	r.seq++

	r.opened++

	// TLSInnerPlaintext: content, its real type, then zero padding.
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 {
		return 0, nil, errors.New("an encrypted record with no content type")
	}

	return plain[len(plain)-1], plain[:len(plain)-1], nil
}

// clear returns the next handshake message, or a ChangeCipherSpec, from
// records sent in the clear.
func (r *reader) clear() (Message, error) {
	for {
		if m, ok := r.message(); ok {
			return m, nil
		}

		typ, body, err := r.record()
		if err != nil {
			return Message{}, err
		}

		switch typ {
		case recordHandshake:
			r.pending = append(r.pending, body...)
		case recordChangeCipherSpec:
			if len(r.pending) > 0 {
				return Message{}, errors.New("ChangeCipherSpec inside a handshake message")
			}

			return Message{
				Type: TypeChangeCipherSpec,
			}, nil

		default:
			return Message{}, fmt.Errorf("record type %d during the handshake", typ)
		}
	}
}

// encrypted returns the next TLS 1.3 handshake message, skipping the
// ChangeCipherSpec records sent for middlebox compatibility; a skipped one
// comes back as a Message so the flight keeps its order.
func (r *reader) encrypted() (Message, error) {
	for {
		if m, ok := r.message(); ok {
			return m, nil
		}

		typ, body, err := r.record()
		if err != nil {
			return Message{}, err
		}

		switch typ {
		case recordHandshake:
			r.pending = append(r.pending, body...)
		case recordChangeCipherSpec:
			return Message{
				Type: TypeChangeCipherSpec,
			}, nil

		default:
			return Message{}, fmt.Errorf("record type %d during the handshake", typ)
		}
	}
}

// message takes a whole handshake message from pending, if there is one.
func (r *reader) message() (Message, bool) {
	if len(r.pending) < 4 {
		return Message{}, false
	}

	n := int(r.pending[1])<<16 | int(r.pending[2])<<8 | int(r.pending[3])
	if len(r.pending) < 4+n {
		return Message{}, false
	}

	m := Message{
		Type: r.pending[0],
		Body: bytes.Clone(r.pending[4 : 4+n]),
	}

	r.pending = r.pending[4+n:]

	return m, true
}

// parseServerHello returns the version a ServerHello chose (its
// supported_versions extension, else legacy_version) and its cipher suite.
func parseServerHello(b []byte) (version, suite uint16, err error) {
	p := parser(b)

	legacy, ok := p.uint16()
	if !ok || !p.skip(32) {
		return 0, 0, errors.New("short ServerHello")
	}

	if _, ok := p.vector8(); !ok {
		return 0, 0, errors.New("ServerHello session ID")
	}

	if suite, ok = p.uint16(); !ok || !p.skip(1) {
		return 0, 0, errors.New("ServerHello cipher suite")
	}

	version = legacy

	if len(p) == 0 {
		return version, suite, nil
	}

	exts, ok := p.vector16()
	if !ok {
		return 0, 0, errors.New("ServerHello extensions")
	}

	for len(exts) > 0 {
		typ, ok1 := exts.uint16()
		data, ok2 := exts.vector16()

		if !ok1 || !ok2 {
			return 0, 0, errors.New("ServerHello extension")
		}

		if typ == extSupportedVersions {
			if version, ok = data.uint16(); !ok {
				return 0, 0, errors.New("ServerHello supported_versions")
			}
		}
	}

	return version, suite, nil
}

// trafficKeys derives a TLS 1.3 traffic secret's record key and IV (RFC
// 8446, section 7.3).
func trafficKeys(suite uint16, secret []byte) (cipher.AEAD, []byte, error) {
	h, keyLen := sha256.New, 16

	switch suite {
	case 0x1301: // TLS_AES_128_GCM_SHA256
	case 0x1302: // TLS_AES_256_GCM_SHA384
		h, keyLen = sha512.New384, 32
	case 0x1303: // TLS_CHACHA20_POLY1305_SHA256
		keyLen = 32
	default:
		return nil, nil, fmt.Errorf("TLS 1.3 cipher suite %#04x", suite)
	}

	key, err := expandLabel(h, secret, "key", keyLen)
	if err != nil {
		return nil, nil, err
	}

	iv, err := expandLabel(h, secret, "iv", 12)
	if err != nil {
		return nil, nil, err
	}

	if suite == 0x1303 {
		aead, err := chacha20poly1305.New(key)

		return aead, iv, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}

	aead, err := cipher.NewGCM(block)

	return aead, iv, err
}

// expandLabel is HKDF-Expand-Label with an empty context (RFC 8446,
// section 7.1).
func expandLabel(h func() hash.Hash, secret []byte, label string, length int) ([]byte, error) {
	full := "tls13 " + label
	info := make([]byte, 0, 4+len(full))

	info = binary.BigEndian.AppendUint16(info, uint16(length))
	info = append(info, byte(len(full)))
	info = append(info, full...)
	info = append(info, 0)

	return hkdf.Expand(h, secret, string(info), length)
}
