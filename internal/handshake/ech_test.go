package handshake

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"testing"

	"golang.org/x/crypto/cryptobyte"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// testConfig describes an ECHConfig for TestSelectECH.
type testConfig struct {
	version, kem uint16
	key          []byte
	suites       [][2]uint16
	publicName   string
	// ext, when not zero, is the type of an extension it carries.
	ext uint16
}

func (c testConfig) marshal() []byte {
	var b cryptobyte.Builder

	b.AddUint16(c.version)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint8(1)
		b.AddUint16(c.kem)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(c.key)
		})
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			for _, s := range c.suites {
				b.AddUint16(s[0])
				b.AddUint16(s[1])
			}
		})
		b.AddUint8(0)
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes([]byte(c.publicName))
		})
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			if c.ext != 0 {
				b.AddUint16(c.ext)
				b.AddUint16(0)
			}
		})
	})

	return b.BytesOrPanic()
}

func testECHList(configs ...[]byte) []byte {
	var b cryptobyte.Builder

	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		for _, c := range configs {
			b.AddBytes(c)
		}
	})

	return b.BytesOrPanic()
}

// testKey is a public key of curve, as an ECHConfig carries it.
func testKey(t *testing.T, curve ecdh.Curve) []byte {
	t.Helper()

	k, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	return k.PublicKey().Bytes()
}

// SelectECH's refusals and skips, which no capture reaches: what a list
// that does not parse, an unmeasured KEM or suite, and a config the
// handshake cannot use (another version, a mandatory extension, a public
// name that is no DNS name, a key that does not parse, no usable suite)
// lead to.
func TestSelectECH(t *testing.T) {
	r := &profile.RealECH{
		KEMs: []profile.Code{
			0x0020,
		},
		SkippedKEMs: []profile.Code{
			0x0010,
		},
		CipherSuites: []profile.HPKESuite{
			{
				KDF:  1,
				AEAD: 1,
			},
		},
	}
	aes := [][2]uint16{
		{1, 1},
	}
	good := testConfig{
		version:    0xfe0d,
		kem:        0x0020,
		key:        testKey(t, ecdh.X25519()),
		suites:     aes,
		publicName: "public.example",
	}
	with := func(change func(*testConfig)) []byte {
		c := good
		change(&c)

		return c.marshal()
	}

	tests := []struct {
		name string
		list []byte
		want []byte
		err  error
	}{
		{"usable", testECHList(good.marshal()), good.marshal(), nil},
		{"skipped KEM first", testECHList(with(func(c *testConfig) {
			c.kem, c.key = 0x0010, testKey(t, ecdh.P256())
		}), good.marshal()), good.marshal(), nil},
		{"other version first", testECHList(with(func(c *testConfig) {
			c.version = 0xfe0e
		}), good.marshal()), good.marshal(), nil},
		{"mandatory extension first", testECHList(with(func(c *testConfig) {
			c.ext = 0x8001
		}), good.marshal()), good.marshal(), nil},
		{"single-label public name first", testECHList(with(func(c *testConfig) {
			c.publicName = "localhost"
		}), good.marshal()), good.marshal(), nil},
		{"public name with a leading hyphen first", testECHList(with(func(c *testConfig) {
			c.publicName = "-bad.example"
		}), good.marshal()), good.marshal(), nil},
		{"IPv4 public name, as uTLS takes it", testECHList(with(func(c *testConfig) {
			c.publicName = "192.0.2.1"
		})), with(func(c *testConfig) {
			c.publicName = "192.0.2.1"
		}), nil},
		{"key not parsing first", testECHList(with(func(c *testConfig) {
			c.key = make([]byte, 31)
		}), good.marshal()), good.marshal(), nil},
		{"KEM the handshake lacks first", testECHList(with(func(c *testConfig) {
			c.kem = 0x9999
		}), good.marshal()), good.marshal(), nil},
		{"no usable suite first", testECHList(with(func(c *testConfig) {
			c.suites = [][2]uint16{{1, 0xffff}, {9, 1}}
		}), good.marshal()), good.marshal(), nil},
		{"unusable suite before a measured one", testECHList(with(func(c *testConfig) {
			c.suites = [][2]uint16{{1, 0xffff}, {1, 1}}
		})), with(func(c *testConfig) {
			c.suites = [][2]uint16{{1, 0xffff}, {1, 1}}
		}), nil},
		{"only unusable configs", testECHList(with(func(c *testConfig) {
			c.publicName = "localhost"
		})), nil, nil},
		{"none usable", testECHList(with(func(c *testConfig) {
			c.kem, c.key = 0x0010, testKey(t, ecdh.P256())
		})), nil, nil},
		{"unmeasured KEM first", testECHList(with(func(c *testConfig) {
			c.kem, c.key = 0x0011, testKey(t, ecdh.P384())
		}), good.marshal()), nil, headerorder.ErrNotMeasured},
		{"unmeasured first usable suite", testECHList(with(func(c *testConfig) {
			c.suites = [][2]uint16{{1, 3}, {1, 1}}
		})), nil, headerorder.ErrNotMeasured},
		{"truncated config", []byte{0x00, 0x05, 0xfe, 0x0d, 0x00, 0x01, 0x07}, nil, ErrMalformedECH},
		{"bad framing", []byte{0x00, 0x09, 0xfe}, nil, ErrMalformedECH},
		{"empty", []byte{0x00, 0x00}, nil, ErrMalformedECH},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SelectECH(tt.list, r)
			if !errors.Is(err, tt.err) || (tt.err == nil) != (err == nil) {
				t.Fatalf("error %v, want %v", err, tt.err)
			}

			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("picked %x, want none", got.List)
			case tt.want != nil && (got == nil || !bytes.Equal(got.List, testECHList(tt.want))):
				t.Fatalf("picked %+v, want %x", got, tt.want)
			}
		})
	}
}
