package quic

// mimicry: QUIC v2 by compatible version negotiation (client_version.go),
// on upstream's synthetic peer (testConn).

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	tls "github.com/roachadam/utls"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/internal/handshake"
	"github.com/roachadam/mimicry/profiles"
)

// newProfileTestConn is a client connection with a browser profile's
// ClientHello and transport parameters against the synthetic server,
// whose transport parameters opts may change.
func newProfileTestConn(t *testing.T, name string, opts ...any) *testConn {
	t.Helper()
	return newProfileTestConnVersion(t, name, 0, opts...)
}

// newProfileTestConnVersion is newProfileTestConn starting in a version
// (DialConfig.Version).
func newProfileTestConnVersion(t *testing.T, name string, version uint32, opts ...any) *testConn {
	t.Helper()
	return newProfileTestConnDial(t, name, DialConfig{Version: version}, opts...)
}

// newProfileTestConnDial is newProfileTestConn with cfg's Version and
// AfterVersionNegotiation.
func newProfileTestConnDial(t *testing.T, name string, cfg DialConfig, opts ...any) *testConn {
	t.Helper()
	prof, err := profiles.ByName(name)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := handshake.Spec(fingerprint.Compile(prof).QUICHello())
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig := newTestTLSConfig(clientSide)
	tlsConfig.ServerName = "example.com"
	cfg.Profile, cfg.Hello, cfg.TLS = prof.QUIC, spec, tlsConfig
	cc, _, err := newClientConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	opts = append([]any{
		func(c *Config) {
			c.TLSConfig = tlsConfig
			c.client = cc
		},
		func(p *transportParameters) {
			p.originalDstConnID = cc.dcid
		},
	}, opts...)
	return newTestConn(t, clientSide, opts...)
}

// serverVersionInfo is a server's version_information: chosen, then the
// available versions.
func serverVersionInfo(chosen uint32, available ...uint32) func(*transportParameters) {
	return func(p *transportParameters) {
		b := binary.BigEndian.AppendUint32(nil, chosen)
		for _, v := range available {
			b = binary.BigEndian.AppendUint32(b, v)
		}
		p.versionInfo = b
	}
}

// serverUpgrades answers the client's first flight in v2 from here on:
// the synthetic server's Initial keys and Version field.
func (tc *testConn) serverUpgrades() {
	keys := initialKeysVersion(tc.conn.client.dcid, serverSide, quicVersion2)
	tc.keysInitial = keys
	tc.version = quicVersion2
}

// readFlight reads what the client sends until it is idle, returning its
// packets' Version fields.
func (tc *testConn) readFlight() (versions []uint32) {
	tc.t.Helper()
	for {
		d := tc.readDatagram()
		if d == nil {
			return versions
		}
		for _, p := range d.packets {
			if p.ptype != packetType1RTT {
				versions = append(versions, p.version)
			}
		}
	}
}

// serverFlight sends the server's Initial and Handshake CRYPTO data, and
// releases the client's Finished (Dial's cork).
func (tc *testConn) serverFlight() {
	defer tc.conn.Uncork()
	tc.writeFrames(packetTypeInitial,
		debugFrameCrypto{data: tc.cryptoDataIn[tls.QUICEncryptionLevelInitial]})
	tc.writeFrames(packetTypeHandshake,
		debugFrameCrypto{data: tc.cryptoDataIn[tls.QUICEncryptionLevelHandshake]})
}

func TestVersionUpgradeToV2(t *testing.T) {
	synctest.Test(t, testVersionUpgradeToV2)
}
func testVersionUpgradeToV2(t *testing.T) {
	tc := newProfileTestConn(t, "firefox-158-macos",
		serverVersionInfo(quicVersion2, quicVersion2, quicVersion1))
	for _, v := range tc.readFlight() {
		if v != quicVersion1 {
			t.Fatalf("first flight in version %#x, want 1", v)
		}
	}
	tc.serverUpgrades()
	tc.serverFlight()
	versions := tc.readFlight()
	if len(versions) == 0 {
		t.Fatal("client sent no long header packet after the server's flight")
	}
	for _, v := range versions {
		if v != quicVersion2 {
			t.Fatalf("client answered in version %#x, want v2", v)
		}
	}
	if got := tc.conn.client.version; got != quicVersion2 {
		t.Fatalf("version in use %#x, want v2", got)
	}
	if !tc.conn.keysAppData.canRead() {
		t.Fatal("handshake did not complete in v2")
	}
	// 1-RTT in v2: the client's were read with v2's keys (readFlight), and
	// the server's HANDSHAKE_DONE is. Given to the connection directly:
	// the test endpoint routes short headers on 8-byte connection IDs.
	d := tc.newDatagramWithFrames(packetType1RTT, debugFrameHandshakeDone{})
	tc.conn.sendMsg(&datagram{b: encodeTestPacket(t, tc, d.packets[0], 0), path: tc.path})
	synctest.Wait()
	if !tc.conn.handshakeConfirmed.isSet() {
		t.Fatal("HANDSHAKE_DONE in a v2 1-RTT packet not processed")
	}
}

func TestVersionUpgradeMismatchedVersionInfo(t *testing.T) {
	for _, tt := range []struct {
		name string
		vi   func(*transportParameters)
	}{
		{"chosen v1", serverVersionInfo(quicVersion1, quicVersion1, quicVersion2)},
		{"missing", func(p *transportParameters) { p.versionInfo = nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := newProfileTestConn(t, "firefox-158-macos", tt.vi)
				tc.readFlight()
				tc.serverUpgrades()
				tc.serverFlight()
				tc.readFlight()
				var e localTransportError
				if err := tc.conn.waitReady(canceledContext()); !errors.As(err, &e) || e.code != errVersionNegotiationDraft {
					t.Fatalf("waitReady = %v, want VERSION_NEGOTIATION_ERROR", err)
				}
			})
		})
	}
}

func TestVersionNoUpgradeWithoutV2(t *testing.T) {
	for _, name := range []string{"chrome-155-macos", "safari-27-macos"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := newProfileTestConn(t, name)
				tc.readFlight()
				tc.serverUpgrades()
				tc.serverFlight()
				for _, v := range tc.readFlight() {
					if v != quicVersion1 {
						t.Fatalf("client sent version %#x, want 1", v)
					}
				}
				if got := tc.conn.client.version; got != quicVersion1 {
					t.Fatalf("version in use %#x, want 1", got)
				}
				if tc.conn.keysHandshake.canRead() {
					t.Fatal("client processed the server's v2 Initial")
				}
			})
		})
	}
}

// TestVersion2Vectors checks v2's packet protection against RFC 9369,
// appendix A.
func TestVersion2Vectors(t *testing.T) {
	unhex := func(s string) []byte {
		b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cid := unhex("8394c8f03e515708")
	// A.3: the server's Initial, read by the client.
	server := unhex(`dc6b3343cf0008f067a5502a4262b500 4075d92faaf16f05d8a4398c47089698
		baeea26b91eb761d9b89237bbf872630 17915358230035f7fd3945d88965cf17
		f9af6e16886c61bfc703106fbaf3cb4c fa52382dd16a393e42757507698075b2
		c984c707f0a0812d8cd5a6881eaf21ce da98f4bd23f6fe1a3e2c43edd9ce7ca8
		4bed8521e2e140`)
	if got := getPacketType(server); got != packetTypeInitial {
		t.Fatalf("server packet type %v, want Initial", got)
	}
	keys := initialKeysVersion(cid, clientSide, quicVersion2)
	p, n := parseLongHeaderPacket(server, keys.r, 0)
	if n != len(server) {
		t.Fatalf("server Initial: parsed %d of %d bytes", n, len(server))
	}
	if p.num != 1 || p.version != quicVersion2 || !bytes.HasPrefix(p.payload, unhex("02000000000600405a020000560303ee")) {
		t.Fatalf("server Initial: pnum %d, version %#x, payload %x", p.num, p.version, p.payload[:16])
	}
	// The client's Initial header protection and type bits, A.2.
	var w packetWriter
	w.reset(1500)
	lp := longPacket{ptype: packetTypeInitial, version: quicVersion2, num: 2, dstConnID: cid}
	w.startProtectedLongHeaderPacket(-1<<62, lp)
	w.b = append(w.b, unhex(`060040f1010000ed0303ebf8fa56f129 39b9584a3896472ec40bb863cfd3e868
		04fe3a47f06a2b69484c000004130113 02010000c000000010000e00000b6578
		616d706c652e636f6dff01000100000a 00080006001d00170018001000070005
		04616c706e0005000501000000000033 00260024001d00209370b2c9caa47fba
		baf4559fedba753de171fa71f50f1ce1 5d43e994ec74d748002b000302030400
		0d0010000e0403050306030203080408 050806002d00020101001c0002400100
		3900320408ffffffffffffffff050480 00ffff07048000ffff08011001048000
		75300901100f088394c8f03e51570806 048000ffff`)...)
	w.appendPaddingTo(1200)
	w.finishProtectedLongHeaderPacket(-1<<62, keys.w, lp)
	if got, want := w.datagram()[:22], unhex("d76b3343cf088394c8f03e5157080000449ea0c95e82"); !bytes.Equal(got, want) {
		t.Errorf("client Initial header %x, want %x", got, want)
	}
	// A.4: the Retry, with v2's integrity tag.
	retry := unhex("cf6b3343cf0008f067a5502a4262b5746f6b656ec8646ce8bfe33952d955543665dcc7b6")
	if got := getPacketType(retry); got != packetTypeRetry {
		t.Fatalf("Retry type %v", got)
	}
	if rp, ok := parseRetryPacket(retry, cid); !ok || rp.version != quicVersion2 || string(rp.token) != "token" {
		t.Fatalf("Retry: ok %v, %+v", ok, rp)
	}
	// A.5: ChaCha20 1-RTT keys and the key update secret.
	secret := unhex("9ac312a7f877468ebe69422748ad00a15443f18203a07d6060f688f30f21632b")
	var k fixedKeys
	k.initVersion(quicVersion2, tls.TLS_CHACHA20_POLY1305_SHA256, secret)
	pkt := unhex("5558b1c60ae7b6b932bc27d786f4bc2bb20f2162ba")
	pay, num, err := k.unprotect(pkt, 1, 654360563)
	if err != nil || num != 654360564 || !bytes.Equal(pay, []byte{1}) {
		t.Fatalf("ChaCha20 packet: %x %d %v", pay, num, err)
	}
	if got, want := updateSecretVersion(quicVersion2, tls.TLS_CHACHA20_POLY1305_SHA256, secret), unhex("c69374c49e3d2a9466fa689e49d476db5d0dfbc87d32ceeaa6343fd0ae4c7d88"); !bytes.Equal(got, want) {
		t.Errorf("ku %x, want %x", got, want)
	}
}

// A resumed Firefox connection whose ticket came from a v2 connection
// starts in v2, chosen version v2 (capture h3-v2).
func TestVersionStartInV2(t *testing.T) {
	synctest.Test(t, testVersionStartInV2)
}
func testVersionStartInV2(t *testing.T) {
	tc := newProfileTestConnVersion(t, "firefox-158-macos", quicVersion2,
		serverVersionInfo(quicVersion2, quicVersion2, quicVersion1))
	tc.serverUpgrades() // the flight must open with v2's Initial keys
	for _, v := range tc.readFlight() {
		if v != quicVersion2 {
			t.Fatalf("first flight in version %#x, want v2", v)
		}
	}
	if vi := tc.conn.client.local.versionInfo; len(vi) < 4 || binary.BigEndian.Uint32(vi) != quicVersion2 {
		t.Fatalf("version_information %x, want chosen v2", vi)
	}
	tc.serverFlight()
	tc.readFlight()
	if !tc.conn.keysAppData.canRead() {
		t.Fatal("handshake did not complete in v2")
	}
}

func TestTicketVersion(t *testing.T) {
	extra := rememberedVersion([][]byte{[]byte("other")}, quicVersion2)
	if got := TicketVersion(extra); got != quicVersion2 {
		t.Errorf("TicketVersion = %#x, want v2", got)
	}
	if got := TicketVersion(nil); got != quicVersion1 {
		t.Errorf("TicketVersion(nil) = %#x, want 1", got)
	}
}

// A Version Negotiation packet ends the connection at once with its
// versions; Firefox, listing v2, starts again in v2, Chrome does not
// (capture h3-vn).
func TestVersionNegotiationRetryVersion(t *testing.T) {
	for _, tt := range []struct {
		name string
		want uint32
		ok   bool
	}{
		{"firefox-158-macos", quicVersion2, true},
		{"chrome-155-macos", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := newProfileTestConn(t, tt.name)
				p := tc.readPacket()
				tc.conn.sendMsg(&datagram{
					b:    appendVersionNegotiation(nil, p.srcConnID, p.dstConnID, 0x7a7adafa, quicVersion2),
					path: tc.path,
				})
				synctest.Wait()
				err := tc.conn.waitReady(canceledContext())
				var vn *VersionNegotiationError
				if !errors.As(err, &vn) || !errors.Is(err, ErrVersionNegotiation) {
					t.Fatalf("waitReady = %v, want a VersionNegotiationError", err)
				}
				got, ok := RetryVersion(err, tc.conn.client.p, quicVersion1)
				if got != tt.want || ok != tt.ok {
					t.Fatalf("RetryVersion = %#x, %v; want %#x, %v", got, ok, tt.want, tt.ok)
				}
			})
		})
	}
}

// The connection started after a Version Negotiation packet needs the
// server's version_information, with Available Versions that lead to the
// same version (RFC 9368, section 4); Firefox closes with 0x53f8 without
// it (capture h3-vn).
func TestVersionAfterNegotiation(t *testing.T) {
	for _, tt := range []struct {
		name string
		vi   func(*transportParameters)
		fail bool
	}{
		{"valid", serverVersionInfo(quicVersion2, quicVersion2), false},
		{"missing", func(p *transportParameters) { p.versionInfo = nil }, true},
		{"no available versions", serverVersionInfo(quicVersion2), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := newProfileTestConnDial(t, "firefox-158-macos",
					DialConfig{Version: quicVersion2, AfterVersionNegotiation: true}, tt.vi)
				tc.serverUpgrades()
				p := tc.readPacket()
				// A second Version Negotiation packet is ignored.
				tc.conn.sendMsg(&datagram{
					b:    appendVersionNegotiation(nil, p.srcConnID, p.dstConnID, quicVersion1),
					path: tc.path,
				})
				tc.readFlight()
				tc.serverFlight()
				tc.readFlight()
				err := tc.conn.waitReady(canceledContext())
				var e localTransportError
				gotFail := errors.As(err, &e) && e.code == errVersionNegotiationDraft
				if gotFail != tt.fail {
					t.Fatalf("waitReady = %v; want VERSION_NEGOTIATION_ERROR %v", err, tt.fail)
				}
				if !tt.fail && !tc.conn.keysAppData.canRead() {
					t.Fatal("handshake did not complete")
				}
			})
		})
	}
}
