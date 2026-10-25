package quictrace

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// rfc9001ClientInitial is the protected client Initial packet of RFC 9001,
// appendix A.2, and rfc9001CryptoFrame the CRYPTO frame it carries.
const (
	rfc9001ClientInitial = "" +
		"c000000001088394c8f03e5157080000449e7b9aec34d1b1c98dd7689fb8ec11" +
		"d242b123dc9bd8bab936b47d92ec356c0bab7df5976d27cd449f63300099f399" +
		"1c260ec4c60d17b31f8429157bb35a1282a643a8d2262cad67500cadb8e7378c" +
		"8eb7539ec4d4905fed1bee1fc8aafba17c750e2c7ace01e6005f80fcb7df6212" +
		"30c83711b39343fa028cea7f7fb5ff89eac2308249a02252155e2347b63d58c5" +
		"457afd84d05dfffdb20392844ae812154682e9cf012f9021a6f0be17ddd0c208" +
		"4dce25ff9b06cde535d0f920a2db1bf362c23e596d11a4f5a6cf3948838a3aec" +
		"4e15daf8500a6ef69ec4e3feb6b1d98e610ac8b7ec3faf6ad760b7bad1db4ba3" +
		"485e8a94dc250ae3fdb41ed15fb6a8e5eba0fc3dd60bc8e30c5c4287e53805db" +
		"059ae0648db2f64264ed5e39be2e20d82df566da8dd5998ccabdae053060ae6c" +
		"7b4378e846d29f37ed7b4ea9ec5d82e7961b7f25a9323851f681d582363aa5f8" +
		"9937f5a67258bf63ad6f1a0b1d96dbd4faddfcefc5266ba6611722395c906556" +
		"be52afe3f565636ad1b17d508b73d8743eeb524be22b3dcbc2c7468d54119c74" +
		"68449a13d8e3b95811a198f3491de3e7fe942b330407abf82a4ed7c1b311663a" +
		"c69890f4157015853d91e923037c227a33cdd5ec281ca3f79c44546b9d90ca00" +
		"f064c99e3dd97911d39fe9c5d0b23a229a234cb36186c4819e8b9c5927726632" +
		"291d6a418211cc2962e20fe47feb3edf330f2c603a9d48c0fcb5699dbfe58964" +
		"25c5bac4aee82e57a85aaf4e2513e4f05796b07ba2ee47d80506f8d2c25e50fd" +
		"14de71e6c418559302f939b0e1abd576f279c4b2e0feb85c1f28ff18f58891ff" +
		"ef132eef2fa09346aee33c28eb130ff28f5b766953334113211996d20011a198" +
		"e3fc433f9f2541010ae17c1bf202580f6047472fb36857fe843b19f5984009dd" +
		"c324044e847a4f4a0ab34f719595de37252d6235365e9b84392b061085349d73" +
		"203a4a13e96f5432ec0fd4a1ee65accdd5e3904df54c1da510b0ff20dcc0c77f" +
		"cb2c0e0eb605cb0504db87632cf3d8b4dae6e705769d1de354270123cb11450e" +
		"fc60ac47683d7b8d0f811365565fd98c4c8eb936bcab8d069fc33bd801b03ade" +
		"a2e1fbc5aa463d08ca19896d2bf59a071b851e6c239052172f296bfb5e724047" +
		"90a2181014f3b94a4e97d117b438130368cc39dbb2d198065ae3986547926cd2" +
		"162f40a29f0c3c8745c0f50fba3852e566d44575c29d39a03f0cda721984b6f4" +
		"40591f355e12d439ff150aab7613499dbd49adabc8676eef023b15b65bfc5ca0" +
		"6948109f23f350db82123535eb8a7433bdabcb909271a6ecbcb58b936a88cd4e" +
		"8f2e6ff5800175f113253d8fa9ca8885c2f552e657dc603f252e1a8e308f76f0" +
		"be79e2fb8f5d5fbbe2e30ecadd220723c8c0aea8078cdfcb3868263ff8f09400" +
		"54da48781893a7e49ad5aff4af300cd804a6b6279ab3ff3afb64491c85194aab" +
		"760d58a606654f9f4400e8b38591356fbf6425aca26dc85244259ff2b19c41b9" +
		"f96f3ca9ec1dde434da7d2d392b905ddf3d1f9af93d1af5950bd493f5aa731b4" +
		"056df31bd267b6b90a079831aaf579be0a39013137aac6d404f518cfd4684064" +
		"7e78bfe706ca4cf5e9c5453e9f7cfd2b8b4c8d169a44e55c88d4a9a7f9474241" +
		"e221af44860018ab0856972e194cd934"
	rfc9001CryptoFrame = "" +
		"060040f1010000ed0303ebf8fa56f12939b9584a3896472ec40bb863cfd3e868" +
		"04fe3a47f06a2b69484c00000413011302010000c000000010000e00000b6578" +
		"616d706c652e636f6dff01000100000a00080006001d00170018001000070005" +
		"04616c706e000500050100000000003300260024001d00209370b2c9caa47fba" +
		"baf4559fedba753de171fa71f50f1ce15d43e994ec74d748002b000302030400" +
		"0d0010000e0403050306030203080408050806002d00020101001c0002400100" +
		"3900320408ffffffffffffffff05048000ffff07048000ffff08011001048000" +
		"75300901100f088394c8f03e51570806048000ffff"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDecodeRFC9001Samples opens RFC 9001's sample packets: the client
// Initial of appendix A.2, through Decode as the server's datagrams (keys
// from its Destination Connection ID, header fields, frames, the
// ClientHello reassembled from its CRYPTO frame), and the ChaCha20-Poly1305
// short header packet of appendix A.5 with the keys of its secret.
func TestDecodeRFC9001Samples(t *testing.T) {
	t.Run("client Initial", func(t *testing.T) {
		start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
		datagram := mustHex(t, rfc9001ClientInitial)
		conns := Decode(start, []Received{
			{
				At:   start.Add(1500 * time.Millisecond),
				Data: datagram,
			},
		}, 8, nil)
		if len(conns) != 1 {
			t.Fatalf("got %d connections, want 1", len(conns))
		}
		c := conns[0]
		if c.Millis != 1500 || c.Version != "00000001" || c.Host != "example.com" {
			t.Errorf("connection: ms %d, version %s, host %q; want 1500, 00000001, example.com", c.Millis, c.Version, c.Host)
		}
		if len(c.Datagrams) != 1 || c.Datagrams[0].Size != 1200 || c.Datagrams[0].Trailing != 0 || len(c.Datagrams[0].Packets) != 1 {
			t.Fatalf("datagrams: %+v; want one of 1200 bytes holding one packet", c.Datagrams)
		}
		p := c.Datagrams[0].Packets[0]
		if p.Error != "" {
			t.Fatalf("packet not opened: %s", p.Error)
		}
		if p.Type != PacketInitial || p.FirstByte != 0xc3 || p.Version != "00000001" || p.DCIDLength != 8 ||
			p.SCIDLength == nil || *p.SCIDLength != 0 || p.TokenLength == nil || *p.TokenLength != 0 ||
			p.Length == nil || *p.Length != 1182 || p.PNLength != 4 || p.PN != 2 || p.Size != 1200 {
			t.Errorf("header: %+v; want the appendix's c3, version 1, 8-byte DCID, no SCID or token, length 1182, 4-byte packet number 2", p)
		}
		want := []Frame{
			{
				Type:   FrameCrypto,
				Code:   0x06,
				Length: 241,
			},
			{
				Type:  FramePadding,
				Count: 1162 - 245,
			},
		}
		if len(p.Frames) != len(want) {
			t.Fatalf("frames: %+v; want %+v", p.Frames, want)
		}
		for i := range want {
			if p.Frames[i].Type != want[i].Type || p.Frames[i].Code != want[i].Code || p.Frames[i].Offset != want[i].Offset ||
				p.Frames[i].Length != want[i].Length || p.Frames[i].Count != want[i].Count {
				t.Errorf("frame %d: %+v; want %+v", i, p.Frames[i], want[i])
			}
		}
		frame := mustHex(t, rfc9001CryptoFrame)
		if got, want := c.ClientHello, hex.EncodeToString(frame[4:]); got != want {
			t.Errorf("ClientHello %s; want the CRYPTO frame's data %s", got, want)
		}
		if len(c.TransportParameters) != 8 {
			t.Errorf("got %d transport parameters, want the ClientHello's 8", len(c.TransportParameters))
		}
	})
	t.Run("ChaCha20 short header", func(t *testing.T) {
		secret := mustHex(t, "9ac312a7f877468ebe69422748ad00a15443f18203a07d6060f688f30f21632b")
		k, err := newKeys(suites[tlsChaCha20Poly1305SHA256], Version1, secret)
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(k.iv); got != "e0459b3474bdd0e44a41c144" {
			t.Errorf("iv %s; want e0459b3474bdd0e44a41c144", got)
		}
		next, err := k.next()
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(next.secret); got != "1223504755036d556342ee9361d253421a826c9ecdf3c7148684b36b714881f9" {
			t.Errorf("ku %s; want the appendix's", got)
		}
		o, err := k.unprotect(mustHex(t, "4cfe4189655e5cd55c41f69080575d7999c25a5bfb"), 1, true, 654360563)
		if err != nil {
			t.Fatal(err)
		}
		if o.firstByte != 0x42 || o.pnLen != 3 || o.pn != 654360564 || !slices.Equal(o.payload, []byte{0x01}) {
			t.Errorf("opened first byte %#x, %d-byte packet number %d, payload %x; want 0x42, 3, 654360564, 01", o.firstByte, o.pnLen, o.pn, o.payload)
		}
	})
}

// RFC 9369's sample packets (appendix A): the client Initial of A.2, the
// server Initial answering it (A.3) and a Retry (A.4); and RFC 9001's
// Retry (appendix A.4) answering its version 1 client Initial. All use the
// client's Destination Connection ID 8394c8f03e515708 and the server's
// connection ID f067a5502a4262b5.
const (
	rfc9369ClientInitial = "" +
		"d76b3343cf088394c8f03e5157080000449ea0c95e82ffe67b6abcdb4298b485" +
		"dd04de806071bf03dceebfa162e75d6c96058bdbfb127cdfcbf903388e99ad04" +
		"9f9a3dd4425ae4d0992cfff18ecf0fdb5a842d09747052f17ac2053d21f57c5d" +
		"250f2c4f0e0202b70785b7946e992e58a59ac52dea6774d4f03b55545243cf1a" +
		"12834e3f249a78d395e0d18f4d766004f1a2674802a747eaa901c3f10cda5500" +
		"cb9122faa9f1df66c392079a1b40f0de1c6054196a11cbea40afb6ef5253cd68" +
		"18f6625efce3b6def6ba7e4b37a40f7732e093daa7d52190935b8da58976ff33" +
		"12ae50b187c1433c0f028edcc4c2838b6a9bfc226ca4b4530e7a4ccee1bfa2a3" +
		"d396ae5a3fb512384b2fdd851f784a65e03f2c4fbe11a53c7777c023462239dd" +
		"6f7521a3f6c7d5dd3ec9b3f233773d4b46d23cc375eb198c63301c21801f6520" +
		"bcfb7966fc49b393f0061d974a2706df8c4a9449f11d7f3d2dcbb90c6b877045" +
		"636e7c0c0fe4eb0f697545460c806910d2c355f1d253bc9d2452aaa549e27a1f" +
		"ac7cf4ed77f322e8fa894b6a83810a34b361901751a6f5eb65a0326e07de7c12" +
		"16ccce2d0193f958bb3850a833f7ae432b65bc5a53975c155aa4bcb4f7b2c4e5" +
		"4df16efaf6ddea94e2c50b4cd1dfe06017e0e9d02900cffe1935e0491d77ffb4" +
		"fdf85290fdd893d577b1131a610ef6a5c32b2ee0293617a37cbb08b847741c3b" +
		"8017c25ca9052ca1079d8b78aebd47876d330a30f6a8c6d61dd1ab5589329de7" +
		"14d19d61370f8149748c72f132f0fc99f34d766c6938597040d8f9e2bb522ff9" +
		"9c63a344d6a2ae8aa8e51b7b90a4a806105fcbca31506c446151adfeceb51b91" +
		"abfe43960977c87471cf9ad4074d30e10d6a7f03c63bd5d4317f68ff325ba3bd" +
		"80bf4dc8b52a0ba031758022eb025cdd770b44d6d6cf0670f4e990b22347a7db" +
		"848265e3e5eb72dfe8299ad7481a408322cac55786e52f633b2fb6b614eaed18" +
		"d703dd84045a274ae8bfa73379661388d6991fe39b0d93debb41700b41f90a15" +
		"c4d526250235ddcd6776fc77bc97e7a417ebcb31600d01e57f32162a8560cacc" +
		"7e27a096d37a1a86952ec71bd89a3e9a30a2a26162984d7740f81193e8238e61" +
		"f6b5b984d4d3dfa033c1bb7e4f0037febf406d91c0dccf32acf423cfa1e70710" +
		"10d3f270121b493ce85054ef58bada42310138fe081adb04e2bd901f2f13458b" +
		"3d6758158197107c14ebb193230cd1157380aa79cae1374a7c1e5bbcb80ee23e" +
		"06ebfde206bfb0fcbc0edc4ebec309661bdd908d532eb0c6adc38b7ca7331dce" +
		"8dfce39ab71e7c32d318d136b6100671a1ae6a6600e3899f31f0eed19e3417d1" +
		"34b90c9058f8632c798d4490da4987307cba922d61c39805d072b589bd52fdf1" +
		"e86215c2d54e6670e07383a27bbffb5addf47d66aa85a0c6f9f32e59d85a44dd" +
		"5d3b22dc2be80919b490437ae4f36a0ae55edf1d0b5cb4e9a3ecabee93dfc6e3" +
		"8d209d0fa6536d27a5d6fbb17641cde27525d61093f1b28072d111b2b4ae5f89" +
		"d5974ee12e5cf7d5da4d6a31123041f33e61407e76cffcdcfd7e19ba58cf4b53" +
		"6f4c4938ae79324dc402894b44faf8afbab35282ab659d13c93f70412e85cb19" +
		"9a37ddec600545473cfb5a05e08d0b209973b2172b4d21fb69745a262ccde96b" +
		"a18b2faa745b6fe189cf772a9f84cbfc"
	rfc9369ServerInitial = "" +
		"dc6b3343cf0008f067a5502a4262b5004075d92faaf16f05d8a4398c47089698" +
		"baeea26b91eb761d9b89237bbf87263017915358230035f7fd3945d88965cf17" +
		"f9af6e16886c61bfc703106fbaf3cb4cfa52382dd16a393e42757507698075b2" +
		"c984c707f0a0812d8cd5a6881eaf21ceda98f4bd23f6fe1a3e2c43edd9ce7ca8" +
		"4bed8521e2e140"
	rfc9369Retry = "cf6b3343cf0008f067a5502a4262b5746f6b656ec8646ce8bfe33952d955543665dcc7b6"
	rfc9001Retry = "ff000000010008f067a5502a4262b5746f6b656e04a265ba2eff4d829058fb3f0f2496ba"
)

// sealPacket protects a packet as its sender would (RFC 9001, section 5):
// header ends in the packet number, of the length its first byte says;
// short is a short header.
func sealPacket(k *keys, header, payload []byte, short bool) []byte {
	pnLen := int(header[0]&0x03) + 1
	pnOffset := len(header) - pnLen
	var pn uint64
	for _, b := range header[pnOffset:] {
		pn = pn<<8 | uint64(b)
	}
	nonce := append([]byte(nil), k.iv...)
	for i := range 8 {
		nonce[len(nonce)-1-i] ^= byte(pn >> (8 * i)) //nolint:gosec // one byte of pn at a time
	}
	p := k.aead.Seal(append([]byte(nil), header...), nonce, payload, header)
	mask := k.mask(p[pnOffset+4 : pnOffset+4+16])
	if short {
		p[0] ^= mask[0] & 0x1f
	} else {
		p[0] ^= mask[0] & 0x0f
	}
	for i := range pnLen {
		p[pnOffset+i] ^= mask[1+i] //nolint:gosec // pnLen is at most 4, mask has 5 bytes
	}
	return p
}

// longPacketHeader is a long header of type bits typ with a 2-byte packet
// number pn, for a payload of n bytes; an Initial's has an empty token.
func longPacketHeader(typ byte, version uint32, dcid, scid []byte, initial bool, pn uint16, n int) []byte {
	h := []byte{0xc0 | typ<<4 | 0x01}
	h = binary.BigEndian.AppendUint32(h, version)
	h = append(h, byte(len(dcid))) //nolint:gosec // test connection IDs are short
	h = append(h, dcid...)
	h = append(h, byte(len(scid))) //nolint:gosec // test connection IDs are short
	h = append(h, scid...)
	if initial {
		h = append(h, 0)
	}
	h = binary.BigEndian.AppendUint16(h, 0x4000|uint16(2+n+16)) //nolint:gosec // test payloads are short
	return binary.BigEndian.AppendUint16(h, pn)
}

// cryptoFrame is a CRYPTO frame carrying data at offset 0.
func cryptoFrame(data []byte) []byte {
	return append(AppendVarint([]byte{0x06, 0x00}, uint64(len(data))), data...)
}

// checkPackets compares packets' types, versions, errors and frame types
// with want, one "type version error: frames" line per packet.
func checkPackets(t *testing.T, what string, datagrams []Datagram, want []string) {
	t.Helper()
	var got []string
	for _, d := range datagrams {
		for _, p := range d.Packets {
			var frames []string
			for _, f := range p.Frames {
				frames = append(frames, f.Type)
			}
			got = append(got, fmt.Sprintf("%s %s %q: %s", p.Type, p.Version, p.Error, strings.Join(frames, " ")))
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s packets:\n%s\nwant:\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestDecodeRFC9369Samples decodes QUIC version 2 (RFC 9369) and version
// negotiation through Decode, server datagrams merged with the client's:
// the appendix's client Initial, the server's Initial and Retry answering
// it (and RFC 9001's version 1 Retry), a corrupted Retry; a compatible
// upgrade from version 1 to 2 (RFC 9368) whose other packets are sealed
// here (Handshake and 1-RTT secrets from a key log, the server's transport
// parameters); the same connection migrating to a second client address;
// the server's 1-RTT headers across a key update; and an incompatible
// negotiation, which starts a new connection.
func TestDecodeRFC9369Samples(t *testing.T) {
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	client := func(ms int, b []byte) Received {
		return Received{
			At:   start.Add(time.Duration(ms) * time.Millisecond),
			Data: b,
		}
	}
	server := func(ms int, b []byte) Received {
		return Received{
			At:     start.Add(time.Duration(ms) * time.Millisecond),
			Data:   b,
			Server: true,
		}
	}
	odcid := mustHex(t, "8394c8f03e515708")
	serverCID := mustHex(t, "f067a5502a4262b5")
	v1Initial := mustHex(t, rfc9001ClientInitial)
	v2Initial := mustHex(t, rfc9369ClientInitial)
	serverInitial := mustHex(t, rfc9369ServerInitial)
	frame := mustHex(t, rfc9001CryptoFrame)

	// The rest of a compatible upgrade's handshake, sealed here with the
	// secrets of a key log for the ClientHello's client random.
	hello, err := readHello(frame[4:])
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string][]byte{
		labelClientHandshake: bytes.Repeat([]byte{0x01}, 32),
		labelServerHandshake: bytes.Repeat([]byte{0x02}, 32),
		labelClientTraffic:   bytes.Repeat([]byte{0x03}, 32),
		labelServerTraffic:   bytes.Repeat([]byte{0x04}, 32),
	}
	keyLogOf := func(labels ...string) *KeyLog {
		var keyLog KeyLog
		for _, label := range labels {
			if _, err := fmt.Fprintf(&keyLog, "%s %x %x\n", label, hello.random, secrets[label]); err != nil {
				t.Fatal(err)
			}
		}
		return &keyLog
	}
	seal := func(k *keys, err error, header, payload []byte, short bool) []byte {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return sealPacket(k, header, payload, short)
	}
	traffic := func(label string, version uint32) (*keys, error) {
		return newKeys(suites[tlsAES128GCMSHA256], version, secrets[label])
	}
	// The client's version 2 Initial goes to the server's connection ID,
	// with keys from its original one.
	ack := []byte{0x02, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	k, err := initialKeys(Version2, odcid, false)
	clientInitial := seal(k, err, longPacketHeader(0x01, Version2, serverCID, nil, true, 3, len(ack)), ack, false)
	// EncryptedExtensions with version_information (chosen version 2, then
	// 2 and 1) and initial_max_data.
	tp := []byte{0x11, 0x0c, 0x6b, 0x33, 0x43, 0xcf, 0x6b, 0x33, 0x43, 0xcf, 0x00, 0x00, 0x00, 0x01, 0x04, 0x02, 0x44, 0x00}
	ee := []byte{0x08, 0x00, 0x00, byte(6 + len(tp)), 0x00, byte(4 + len(tp)), 0x00, 0x39, 0x00, byte(len(tp))} //nolint:gosec // 18 bytes of parameters
	payload := cryptoFrame(slices.Concat(ee, tp))
	k, err = traffic(labelServerHandshake, Version2)
	serverHandshake := seal(k, err, longPacketHeader(0x03, Version2, nil, serverCID, false, 0, len(payload)), payload, false)
	payload = cryptoFrame(append([]byte{0x14, 0x00, 0x00, 0x20}, make([]byte, 32)...))
	k, err = traffic(labelClientHandshake, Version2)
	clientHandshake := seal(k, err, longPacketHeader(0x03, Version2, serverCID, nil, false, 0, len(payload)), payload, false)
	k, err = traffic(labelClientTraffic, Version2)
	client1RTT := seal(k, err, append(append([]byte{0x41}, serverCID...), 0x00, 0x00), []byte{0x01, 0x00}, true)
	upgrade := []Received{
		client(0, v1Initial),
		server(2, serverInitial),
		client(4, clientInitial),
		server(5, serverHandshake),
		client(6, clientHandshake),
		client(7, client1RTT),
	}

	t.Run("client Initial", func(t *testing.T) {
		for _, k := range []struct {
			server bool
			iv     string
		}{
			{
				server: false,
				iv:     "91f73e2351d8fa91660e909f",
			},
			{
				server: true,
				iv:     "dd13c276499c0249d3310652",
			},
		} {
			keys, err := initialKeys(Version2, odcid, k.server)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(keys.iv); got != k.iv {
				t.Errorf("server %v: iv %s; want %s", k.server, got, k.iv)
			}
		}
		conns := Decode(start, []Received{
			client(1500, v2Initial),
		}, 8, nil)
		if len(conns) != 1 {
			t.Fatalf("got %d connections, want 1", len(conns))
		}
		c := conns[0]
		if c.Millis != 1500 || c.Version != "6b3343cf" || c.NegotiatedVersion != "" || c.Host != "example.com" {
			t.Errorf("connection: ms %d, version %s, negotiated %q, host %q; want 1500, 6b3343cf, none, example.com", c.Millis, c.Version, c.NegotiatedVersion, c.Host)
		}
		checkPackets(t, "client", c.Datagrams, []string{
			`Initial 6b3343cf "": CRYPTO PADDING`,
		})
		p := c.Datagrams[0].Packets[0]
		if p.FirstByte != 0xd3 || p.DCIDLength != 8 || p.SCIDLength == nil || *p.SCIDLength != 0 ||
			p.TokenLength == nil || *p.TokenLength != 0 || p.Length == nil || *p.Length != 1182 ||
			p.PNLength != 4 || p.PN != 2 || p.Size != 1200 || p.Frames[0].Length != 241 || p.Frames[1].Count != 1162-245 {
			t.Errorf("packet: %+v; want the appendix's d3, 8-byte DCID, no SCID or token, length 1182, 4-byte packet number 2, CRYPTO of 241 bytes, padding", p)
		}
		if got, want := c.ClientHello, hex.EncodeToString(frame[4:]); got != want {
			t.Errorf("ClientHello %s; want the CRYPTO frame's data %s", got, want)
		}
	})

	t.Run("server Initial", func(t *testing.T) {
		conns := Decode(start, []Received{
			client(0, v2Initial),
			server(3, serverInitial),
		}, 8, nil)
		if len(conns) != 1 || len(conns[0].Server) != 1 {
			t.Fatalf("got %d connections; want 1 with one server datagram", len(conns))
		}
		c := conns[0]
		checkPackets(t, "client", c.Datagrams, []string{
			`Initial 6b3343cf "": CRYPTO PADDING`,
		})
		checkPackets(t, "server", c.Server, []string{
			`Initial 6b3343cf "": ACK CRYPTO`,
		})
		d := c.Server[0]
		p := d.Packets[0]
		if d.Micros != 3000 || d.Size != 135 || p.FirstByte != 0xd1 || p.DCIDLength != 0 || p.SCIDLength == nil || *p.SCIDLength != 8 ||
			p.TokenLength == nil || *p.TokenLength != 0 || p.Length == nil || *p.Length != 117 ||
			p.PNLength != 2 || p.PN != 1 || p.Size != 135 || p.Frames[1].Length != 90 {
			t.Errorf("datagram at %d us of %d bytes, packet %+v; want 3000 us, 135 bytes, d1, no DCID, 8-byte SCID, no token, length 117, 2-byte packet number 1, CRYPTO of 90 bytes", d.Micros, d.Size, p)
		}
	})

	t.Run("Retry", func(t *testing.T) {
		corrupted := mustHex(t, rfc9369Retry)
		corrupted[len(corrupted)-1] ^= 0x01
		for _, tt := range []struct {
			name    string
			initial []byte
			retry   []byte
			version string
			err     string
		}{
			{
				name:    "version 2",
				initial: v2Initial,
				retry:   mustHex(t, rfc9369Retry),
				version: "6b3343cf",
			},
			{
				name:    "version 1",
				initial: v1Initial,
				retry:   mustHex(t, rfc9001Retry),
				version: "00000001",
			},
			{
				name:    "corrupted",
				initial: v2Initial,
				retry:   corrupted,
				version: "6b3343cf",
				err:     errRetryTag.Error(),
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				conns := Decode(start, []Received{
					client(0, tt.initial),
					server(1, tt.retry),
				}, 8, nil)
				if len(conns) != 1 {
					t.Fatalf("got %d connections, want 1", len(conns))
				}
				checkPackets(t, "server", conns[0].Server, []string{
					fmt.Sprintf("Retry %s %q: ", tt.version, tt.err),
				})
				p := conns[0].Server[0].Packets[0]
				if p.DCIDLength != 0 || p.SCIDLength == nil || *p.SCIDLength != 8 || p.TokenLength == nil || *p.TokenLength != 5 || p.Size != len(tt.retry) {
					t.Errorf("packet %+v; want no DCID, 8-byte SCID, 5-byte token, %d bytes", p, len(tt.retry))
				}
			})
		}
	})

	t.Run("compatible upgrade", func(t *testing.T) {
		keyLog := keyLogOf(labelClientHandshake, labelServerHandshake, labelClientTraffic)
		server1RTT := append([]byte{0x41}, make([]byte, 24)...)
		datagrams := append(slices.Clone(upgrade), server(8, server1RTT))

		conns := Decode(start, datagrams[:3], 8, keyLog)
		if len(conns) != 1 || conns[0].NegotiatedVersion != "" {
			t.Fatalf("before any Handshake packet: %d connections; want 1 without a negotiated version", len(conns))
		}
		checkPackets(t, "client", conns[0].Datagrams, []string{
			`Initial 00000001 "": CRYPTO PADDING`,
			`Initial 6b3343cf "": ACK PADDING`,
		})

		conns = Decode(start, datagrams, 8, keyLog)
		if len(conns) != 1 {
			t.Fatalf("got %d connections, want 1", len(conns))
		}
		c := conns[0]
		if c.Version != "00000001" || c.NegotiatedVersion != "6b3343cf" {
			t.Errorf("version %s, negotiated %q; want 00000001, 6b3343cf", c.Version, c.NegotiatedVersion)
		}
		checkPackets(t, "client", c.Datagrams, []string{
			`Initial 00000001 "": CRYPTO PADDING`,
			`Initial 6b3343cf "": ACK PADDING`,
			`Handshake 6b3343cf "": CRYPTO`,
			`1-RTT  "": PING PADDING`,
		})
		checkPackets(t, "server", c.Server, []string{
			`Initial 6b3343cf "": ACK CRYPTO`,
			`Handshake 6b3343cf "": CRYPTO`,
			`1-RTT  "": `,
		})
		if p := c.Datagrams[1].Packets[0]; p.PN != 3 || p.DCIDLength != 8 {
			t.Errorf("client's version 2 Initial: packet number %d, %d-byte DCID; want 3, 8", p.PN, p.DCIDLength)
		}
		if p := c.Server[2].Packets[0]; p.DCIDLength != 0 || p.Size != 25 || p.FirstByte != 0x40 {
			t.Errorf("server's 1-RTT packet: %+v; want no DCID, 25 bytes, first byte 40", p)
		}
		tps := c.ServerTransportParameters
		if len(tps) != 2 || tps[0].Name != "version_information" || !slices.Equal(tps[0].Versions, []string{"6b3343cf", "6b3343cf", "00000001"}) ||
			tps[1].Name != "initial_max_data" || tps[1].Value == nil || *tps[1].Value != 1024 {
			t.Errorf("server transport parameters %+v; want version_information 6b3343cf 6b3343cf 00000001, initial_max_data 1024", tps)
		}
	})

	t.Run("migration", func(t *testing.T) {
		from := func(addr string, r Received) Received {
			r.Addr = addr
			return r
		}
		k, err := traffic(labelClientTraffic, Version2)
		migrated := seal(k, err, append(append([]byte{0x41}, serverCID...), 0x00, 0x01), []byte{0x01, 0x00}, true)
		k, err = traffic(labelServerTraffic, Version2)
		toNewPath := seal(k, err, []byte{0x41, 0x00, 0x00}, []byte{0x01, 0x00}, true)
		datagrams := make([]Received, 0, len(upgrade)+4)
		for _, r := range upgrade {
			datagrams = append(datagrams, from("a", r))
		}
		datagrams = append(datagrams,
			// To an address no connection uses yet: dropped.
			from("c", server(8, toNewPath)),
			from("b", client(9, migrated)),
			from("b", server(10, toNewPath)),
			// From an address no connection uses, opened by none: dropped.
			from("c", client(11, append([]byte{0x41}, make([]byte, 30)...))),
		)
		conns := Decode(start, datagrams, 8, keyLogOf(labelClientHandshake, labelServerHandshake, labelClientTraffic, labelServerTraffic))
		if len(conns) != 1 {
			t.Fatalf("got %d connections, want 1", len(conns))
		}
		c := conns[0]
		checkPackets(t, "client", c.Datagrams, []string{
			`Initial 00000001 "": CRYPTO PADDING`,
			`Initial 6b3343cf "": ACK PADDING`,
			`Handshake 6b3343cf "": CRYPTO`,
			`1-RTT  "": PING PADDING`,
			`1-RTT  "": PING PADDING`,
		})
		checkPackets(t, "server", c.Server, []string{
			`Initial 6b3343cf "": ACK CRYPTO`,
			`Handshake 6b3343cf "": CRYPTO`,
			`1-RTT  "": `,
		})
		paths := func(ds []Datagram) []int {
			out := make([]int, 0, len(ds))
			for _, d := range ds {
				out = append(out, d.Path)
			}
			return out
		}
		if got := paths(c.Datagrams); c.Addr != "a" || !slices.Equal(got, []int{0, 0, 0, 0, 1}) {
			t.Errorf("started on %q, client paths %v; want a, 0 0 0 0 1", c.Addr, got)
		}
		if got := paths(c.Server); !slices.Equal(got, []int{0, 0, 1}) {
			t.Errorf("server paths %v; want 0 0 1", got)
		}
		if p := c.Datagrams[4].Packets[0]; p.PN != 1 {
			t.Errorf("client's packet on the new path: packet number %d; want 1", p.PN)
		}
		if p := c.Server[2].Packets[0]; p.FirstByte != 0x41 || p.PNLength != 2 || p.PN != 0 || p.DCIDLength != 0 {
			t.Errorf("server's packet on the new path: %+v; want first byte 41, 2-byte packet number 0, no DCID", p)
		}
	})

	t.Run("server 1-RTT key phase", func(t *testing.T) {
		k, err := traffic(labelServerTraffic, Version1)
		phase0 := seal(k, err, []byte{0x41, 0x00, 0x07}, []byte{0x01, 0x00}, true)
		next, err := k.next()
		phase1 := seal(next, err, []byte{0x44, 0x08}, []byte{0x01, 0x00, 0x00}, true)
		for _, tt := range []struct {
			name   string
			keyLog *KeyLog
			// want are each server packet's first byte, packet number
			// length and packet number.
			want []string
		}{
			{
				name:   "SERVER_TRAFFIC_SECRET_0 logged",
				keyLog: keyLogOf(labelServerTraffic),
				want: []string{
					"41 2 7",
					"44 1 8",
				},
			},
			{
				name:   "not logged",
				keyLog: keyLogOf(),
				want: []string{
					"40 0 0",
					"40 0 0",
				},
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				conns := Decode(start, []Received{
					client(0, v1Initial),
					server(1, phase0),
					server(2, phase1),
				}, 8, tt.keyLog)
				if len(conns) != 1 {
					t.Fatalf("got %d connections, want 1", len(conns))
				}
				var got []string
				for _, d := range conns[0].Server {
					for _, p := range d.Packets {
						got = append(got, fmt.Sprintf("%02x %d %d", p.FirstByte, p.PNLength, p.PN))
						if p.Type != Packet1RTT || p.Frames != nil || p.Error != "" {
							t.Errorf("packet %+v; want a 1-RTT header without frames or error", p)
						}
					}
				}
				if !slices.Equal(got, tt.want) {
					t.Errorf("server 1-RTT headers %q; want %q", got, tt.want)
				}
			})
		}
	})

	t.Run("version negotiation", func(t *testing.T) {
		vn := append(append([]byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x08}, odcid...), 0x00, 0x00, 0x00, 0x01, 0xff, 0x00, 0x00, 0x1d)
		for _, tt := range []struct {
			name      string
			datagrams []Received
			// versions are each connection's, with its client and server
			// datagram counts.
			versions []string
		}{
			{
				name: "incompatible",
				datagrams: []Received{
					server(0, vn),
					client(1, v2Initial),
					server(2, vn),
					client(3, v1Initial),
				},
				versions: []string{
					"6b3343cf 1 1",
					"00000001 1 0",
				},
			},
			{
				name: "without Version Negotiation",
				datagrams: []Received{
					client(1, v2Initial),
					client(3, v1Initial),
				},
				versions: []string{
					"6b3343cf 2 0",
				},
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				conns := Decode(start, tt.datagrams, 8, nil)
				var got []string
				for _, c := range conns {
					got = append(got, fmt.Sprintf("%s %d %d", c.Version, len(c.Datagrams), len(c.Server)))
					for _, d := range c.Datagrams {
						if p := d.Packets[0]; p.Type != PacketInitial || p.Error != "" {
							t.Errorf("connection %s: packet %s not opened: %s", c.Version, p.Type, p.Error)
						}
					}
				}
				if !slices.Equal(got, tt.versions) {
					t.Fatalf("connections %q; want %q", got, tt.versions)
				}
				if len(conns[0].Server) == 0 {
					return
				}
				p := conns[0].Server[0].Packets[0]
				if p.Type != PacketVN || p.Error != "" || p.DCIDLength != 0 || p.SCIDLength == nil || *p.SCIDLength != 8 ||
					!slices.Equal(p.Versions, []string{"00000001", "ff00001d"}) {
					t.Errorf("Version Negotiation packet %+v; want no DCID, 8-byte SCID, versions 00000001 ff00001d", p)
				}
			})
		}
	})
}
