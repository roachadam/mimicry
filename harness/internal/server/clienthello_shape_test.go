package server

import (
	"encoding/binary"
	"fmt"
	"slices"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/harness/internal/diff"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// helloParts is a ClientHello message taken apart, to be changed and put
// back together.
type helloParts struct {
	version     uint16
	random      []byte
	sessionID   []byte
	suites      []byte // with its length prefix
	compression []byte // with its length prefix
	extensions  []helloExtension
}

type helloExtension struct {
	typ  uint16
	data []byte
}

func splitHello(t *testing.T, msg []byte) helloParts {
	t.Helper()
	b := msg[4:]
	h := helloParts{
		version: binary.BigEndian.Uint16(b),
		random:  b[2:34],
	}
	b = b[34:]
	h.sessionID, b = b[1:1+int(b[0])], b[1+int(b[0]):]
	n := 2 + int(binary.BigEndian.Uint16(b))
	h.suites, b = b[:n], b[n:]
	n = 1 + int(b[0])
	h.compression, b = b[:n], b[n:]
	exts := b[2 : 2+int(binary.BigEndian.Uint16(b))]
	for len(exts) > 0 {
		n := int(binary.BigEndian.Uint16(exts[2:]))
		h.extensions = append(h.extensions, helloExtension{
			typ:  binary.BigEndian.Uint16(exts),
			data: exts[4 : 4+n],
		})
		exts = exts[4+n:]
	}

	return h
}

func (h helloParts) bytes() []byte {
	var exts []byte
	for _, e := range h.extensions {
		exts = binary.BigEndian.AppendUint16(exts, e.typ)
		exts = binary.BigEndian.AppendUint16(exts, uint16(len(e.data)))
		exts = append(exts, e.data...)
	}
	var body []byte
	body = binary.BigEndian.AppendUint16(body, h.version)
	body = append(body, h.random...)
	body = append(body, byte(len(h.sessionID)))
	body = append(body, h.sessionID...)
	body = append(body, h.suites...)
	body = append(body, h.compression...)
	body = binary.BigEndian.AppendUint16(body, uint16(len(exts)))
	body = append(body, exts...)
	// The handshake type, then a 24-bit length.
	header := binary.BigEndian.AppendUint32(nil, handshakeClientHello<<24|uint32(len(body)))

	return append(header, body...)
}

// withExtension returns h with the body of its extension typ changed by f.
func (h helloParts) withExtension(t *testing.T, typ uint16, f func([]byte) []byte) helloParts {
	t.Helper()
	i := slices.IndexFunc(h.extensions, func(e helloExtension) bool {
		return e.typ == typ
	})
	if i < 0 {
		t.Fatalf("no extension %#04x", typ)
	}
	h.extensions = slices.Clone(h.extensions)
	h.extensions[i].data = f(slices.Clone(h.extensions[i].data))

	return h
}

// keyShares rewrites a key_share body, changing each key's length by f.
func keyShares(data []byte, f func(i, n int) int) []byte {
	var out []byte
	list := data[2:]
	for i := 0; len(list) > 0; i++ {
		n := int(binary.BigEndian.Uint16(list[2:]))
		out = append(out, list[:2]...)
		m := f(i, n)
		out = binary.BigEndian.AppendUint16(out, uint16(m))
		out = append(out, make([]byte, m)...)
		list = list[4+n:]
	}
	out = append(binary.BigEndian.AppendUint16(nil, uint16(len(out))), out...)

	return out
}

// echBody rewrites an outer ECH body: its fields at fixed offsets, and the
// encapsulated key and payload resized to enc and payload bytes.
func echBody(data []byte, kdf, aead uint16, enc, payload int) []byte {
	out := []byte{
		data[0],
	}
	out = binary.BigEndian.AppendUint16(out, kdf)
	out = binary.BigEndian.AppendUint16(out, aead)
	out = append(out, data[5])
	out = binary.BigEndian.AppendUint16(out, uint16(enc))
	out = append(out, make([]byte, enc)...)
	out = binary.BigEndian.AppendUint16(out, uint16(payload))

	return append(out, make([]byte, payload)...)
}

const (
	extKeyShare uint16 = 0x0033
	extECH      uint16 = 0xfe0d
	extPadding  uint16 = 0x0015
)

// capturedHellos returns the first captured ClientHello of every browser.
func capturedHellos(t *testing.T) map[string][]byte {
	t.Helper()
	hellos := map[string][]byte{}
	for _, name := range profiles.Names() {
		samples, err := capture.ReadClientHellos("../../testdata/captures/" + name + ".clienthello")
		if err != nil {
			t.Fatal(err)
		}
		hello := samples[0]
		if hello[0] == recordTypeHandshake {
			if _, hello, err = ParseRecords(hello); err != nil {
				t.Fatal(err)
			}
		}
		hellos[name] = hello
	}

	return hellos
}

// helloMutation changes one thing in a ClientHello.
type helloMutation struct {
	name   string
	mutate func(t *testing.T, h helloParts) helloParts
}

// Every value a browser keeps constant that the parser records shows up as
// a difference when a real ClientHello changes it, or the changed hello is
// refused; nothing it sends is normalised away. (The record-layer version
// is not in the profile yet; ParseRecords reads it.)
func TestParseClientHelloSeesEveryShapeChange(t *testing.T) {
	common := []helloMutation{
		{
			name: "legacy_version",
			mutate: func(_ *testing.T, h helloParts) helloParts {
				h.version = 0x0301
				return h
			},
		},
		{
			name: "no session ID",
			mutate: func(_ *testing.T, h helloParts) helloParts {
				h.sessionID = nil
				return h
			},
		},
		{
			name: "shorter session ID",
			mutate: func(_ *testing.T, h helloParts) helloParts {
				h.sessionID = h.sessionID[:16]
				return h
			},
		},
		{
			name: "padding where BoringSSL pads none",
			mutate: func(_ *testing.T, h helloParts) helloParts {
				h.extensions = append(slices.Clone(h.extensions), helloExtension{
					typ:  extPadding,
					data: make([]byte, 20),
				})

				return h
			},
		},
		{
			name: "an extra extension (total length)",
			mutate: func(_ *testing.T, h helloParts) helloParts {
				h.extensions = append(slices.Clone(h.extensions), helloExtension{
					typ: 0x7a7a,
				})

				return h
			},
		},
	}
	for name, msg := range capturedHellos(t) {
		h := splitHello(t, msg)
		if got := h.bytes(); string(got) != string(msg) {
			t.Fatalf("%s: taking the hello apart and back changes it", name)
		}
		original, err := ParseClientHello(msg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mutations := slices.Clone(common)
		ks := slices.IndexFunc(h.extensions, func(e helloExtension) bool {
			return e.typ == extKeyShare
		})
		for i := range countShares(h.extensions[ks].data) {
			mutations = append(mutations, helloMutation{
				name: fmt.Sprintf("key share %d one byte shorter", i+1),
				mutate: func(t *testing.T, h helloParts) helloParts {
					return h.withExtension(t, extKeyShare, func(data []byte) []byte {
						return keyShares(data, func(j, n int) int {
							if j == i {
								return n - 1
							}

							return n
						})
					})
				},
			})
		}
		if slices.ContainsFunc(h.extensions, func(e helloExtension) bool {
			return e.typ == extECH
		}) {
			mutations = append(mutations, echMutations()...)
		}
		for _, m := range mutations {
			t.Run(name+"/"+m.name, func(t *testing.T) {
				mutated, err := ParseClientHello(m.mutate(t, h).bytes())
				if err != nil {
					return
				}
				diffs := diff.Compare(&profile.Profile{
					TLS: original,
				}, &profile.Profile{
					TLS: mutated,
				})
				if len(diffs) == 0 {
					t.Error("the changed ClientHello shows no difference")
				}
			})
		}
	}
}

func countShares(data []byte) int {
	n := 0
	keyShares(data, func(_, m int) int {
		n++
		return m
	})

	return n
}

func echMutations() []helloMutation {
	ech := func(f func(data []byte) []byte) func(t *testing.T, h helloParts) helloParts {
		return func(t *testing.T, h helloParts) helloParts {
			return h.withExtension(t, extECH, f)
		}
	}
	fields := func(data []byte) (kdf, aead uint16, enc, payload int) {
		enc = int(binary.BigEndian.Uint16(data[6:]))
		return binary.BigEndian.Uint16(data[1:]), binary.BigEndian.Uint16(data[3:]), enc, int(binary.BigEndian.Uint16(data[8+enc:]))
	}

	return []helloMutation{
		{
			name: "ECH type inner",
			mutate: ech(func(data []byte) []byte {
				data[0] = 1
				return data
			}),
		},
		{
			name: "ECH KDF",
			mutate: ech(func(data []byte) []byte {
				_, aead, enc, payload := fields(data)
				return echBody(data, 0x0002, aead, enc, payload)
			}),
		},
		{
			name: "ECH AEAD",
			mutate: ech(func(data []byte) []byte {
				kdf, _, enc, payload := fields(data)
				return echBody(data, kdf, 0x0002, enc, payload)
			}),
		},
		{
			name: "ECH encapsulated key length",
			mutate: ech(func(data []byte) []byte {
				kdf, aead, enc, payload := fields(data)
				return echBody(data, kdf, aead, enc/2, payload)
			}),
		},
		{
			name: "ECH payload length",
			mutate: ech(func(data []byte) []byte {
				kdf, aead, enc, payload := fields(data)
				return echBody(data, kdf, aead, enc, payload+1)
			}),
		},
		{
			name: "ECH body cut short",
			mutate: ech(func(data []byte) []byte {
				return data[:10]
			}),
		},
	}
}
