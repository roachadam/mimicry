package handshake

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/crypto/cryptobyte"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// testECHConfig is an ECHConfig of version, KEM and suites, with an
// extension of type ext when not zero.
func testECHConfig(version, kem uint16, suites [][2]uint16, ext uint16) []byte {
	var b cryptobyte.Builder

	b.AddUint16(version)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint8(1)
		b.AddUint16(kem)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(make([]byte, 32))
		})
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			for _, s := range suites {
				b.AddUint16(s[0])
				b.AddUint16(s[1])
			}
		})
		b.AddUint8(0)
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes([]byte("public.example"))
		})
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			if ext != 0 {
				b.AddUint16(ext)
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

// SelectECH's refusals and skips, which no capture reaches: what a list
// that does not parse, an unmeasured KEM or suite, a mandatory extension
// or another version leads to.
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
	x25519 := testECHConfig(0xfe0d, 0x0020, aes, 0)

	tests := []struct {
		name string
		list []byte
		want []byte
		err  error
	}{
		{"usable", testECHList(x25519), x25519, nil},
		{"skipped KEM first", testECHList(testECHConfig(0xfe0d, 0x0010, aes, 0), x25519), x25519, nil},
		{"other version first", testECHList(testECHConfig(0xfe0e, 0x0020, aes, 0), x25519), x25519, nil},
		{"mandatory extension first", testECHList(testECHConfig(0xfe0d, 0x0020, aes, 0x8001), x25519), x25519, nil},
		{"none usable", testECHList(testECHConfig(0xfe0d, 0x0010, aes, 0)), nil, nil},
		{"unmeasured KEM first", testECHList(testECHConfig(0xfe0d, 0x0041, aes, 0), x25519), nil, headerorder.ErrNotMeasured},
		{"unmeasured first suite", testECHList(testECHConfig(0xfe0d, 0x0020, [][2]uint16{{1, 3}, {1, 1}}, 0)), nil, headerorder.ErrNotMeasured},
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
			case tt.want != nil && (got == nil || !bytes.Equal(got.List, testECHList(tt.want)) || got.PublicName != "public.example"):
				t.Fatalf("picked %+v, want %x", got, tt.want)
			}
		})
	}
}
