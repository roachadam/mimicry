package quictrace

import (
	"bytes"
	"encoding/hex"
	"errors"
	"sync"

	"golang.org/x/crypto/cryptobyte"
)

// Key log labels of the client's traffic secrets, and of the server's
// handshake and first application traffic secrets (NSS key log format).
const (
	labelClientEarly     = "CLIENT_EARLY_TRAFFIC_SECRET"
	labelClientHandshake = "CLIENT_HANDSHAKE_TRAFFIC_SECRET"
	labelClientTraffic   = "CLIENT_TRAFFIC_SECRET_0"
	labelServerHandshake = "SERVER_HANDSHAKE_TRAFFIC_SECRET"
	labelServerTraffic   = "SERVER_TRAFFIC_SECRET_0"
)

// KeyLog collects the TLS key log a server writes (tls.Config.KeyLogWriter,
// NSS format, one line per write): every connection's traffic secrets, by
// the client random of its ClientHello. Decode reads it to open the
// client's Handshake and 1-RTT packets and the server's Handshake packets
// (and its 1-RTT packets' headers).
// A Go server logs no early traffic secret, so the PSKs of the tickets
// clients resume with are kept too (AddTicket), from which Decode derives
// it for 0-RTT packets.
type KeyLog struct {
	mu      sync.Mutex
	secrets map[string]map[string][]byte
	tickets map[string]ticket
}

// ticket is a session ticket's cipher suite and resumption PSK.
type ticket struct {
	suite uint16
	psk   []byte
}

// AddTicket records the PSK of a ticket a client offered: identity is the
// ticket as the ClientHello's pre_shared_key carries it, session the
// server's state for it as crypto/tls encodes it (tls.SessionState.Bytes:
// version, type, cipher suite, creation time, then the secret, which for
// TLS 1.3 is the PSK).
func (k *KeyLog) AddTicket(identity, session []byte) error {
	s := cryptobyte.String(session)
	var version, suite uint16
	var typ uint8
	var created uint64
	var secret []byte
	if !s.ReadUint16(&version) || !s.ReadUint8(&typ) || !s.ReadUint16(&suite) ||
		!s.ReadUint64(&created) || !s.ReadUint8LengthPrefixed((*cryptobyte.String)(&secret)) {
		return errors.New("malformed session state")
	}
	if version != 0x0304 {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.tickets == nil {
		k.tickets = map[string]ticket{}
	}
	k.tickets[hex.EncodeToString(identity)] = ticket{
		suite: suite,
		psk:   append([]byte(nil), secret...),
	}
	return nil
}

// earlySecret derives the client_early_traffic_secret of a ClientHello
// whose first PSK identity is identity, or nil if that ticket's PSK is not
// known.
func (k *KeyLog) earlySecret(identity, clientHello []byte) []byte {
	if k == nil || identity == nil {
		return nil
	}
	k.mu.Lock()
	t, ok := k.tickets[hex.EncodeToString(identity)]
	k.mu.Unlock()
	if !ok {
		return nil
	}
	secret, err := clientEarlyTrafficSecret(t.suite, t.psk, clientHello)
	if err != nil {
		return nil
	}
	return secret
}

// Write records the secrets of the lines in p; lines it cannot read are
// ignored.
func (k *KeyLog) Write(p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.secrets == nil {
		k.secrets = map[string]map[string][]byte{}
	}
	for line := range bytes.SplitSeq(p, []byte("\n")) {
		f := bytes.Fields(line)
		if len(f) != 3 {
			continue
		}
		secret, err := hex.DecodeString(string(f[2]))
		if err != nil {
			continue
		}
		random := string(bytes.ToLower(f[1]))
		if k.secrets[random] == nil {
			k.secrets[random] = map[string][]byte{}
		}
		k.secrets[random][string(f[0])] = secret
	}
	return len(p), nil
}

// secret returns the secret logged under label for the connection whose
// ClientHello carried random, or nil.
func (k *KeyLog) secret(random []byte, label string) []byte {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.secrets[hex.EncodeToString(random)][label]
}
