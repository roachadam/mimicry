package quictrace

import (
	"encoding/hex"
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
