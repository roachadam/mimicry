package handshake

import (
	"encoding/binary"
	"io"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/internal/fingerprint"
	"github.com/roachadam/mimicry/profile"
	"github.com/roachadam/mimicry/profiles"
)

// sentHello is what a ClientHello on the wire says about its shape.
type sentHello struct {
	recordVersion   uint16
	legacyVersion   uint16
	sessionIDLength int
	keyShareLengths []uint16
	ech             *profile.ECH
}

// sendHello runs a handshake with hello into a peer that reads the
// ClientHello's records and hangs up, and returns what they carried.
func sendHello(t *testing.T, hello fingerprint.Hello) sentHello {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		_, _, err := Client(t.Context(), client, hello, Config{
			ServerName: "localhost",
		})
		done <- err
	}()
	var records, msg []byte
	for need := -1; need < 0 || len(msg) < need; {
		header := make([]byte, 5)
		if _, err := io.ReadFull(server, header); err != nil {
			t.Fatal(err)
		}
		fragment := make([]byte, binary.BigEndian.Uint16(header[3:]))
		if _, err := io.ReadFull(server, fragment); err != nil {
			t.Fatal(err)
		}
		records = append(records, header...)
		msg = append(msg, fragment...)
		if need < 0 && len(msg) >= 4 {
			need = 4 + (int(msg[1])<<16 | int(msg[2])<<8 | int(msg[3]))
		}
	}
	_ = server.Close()
	if err := <-done; err == nil {
		t.Error("the handshake succeeded with a peer that hung up")
	}
	return parseSent(t, records, msg)
}

func parseSent(t *testing.T, records, msg []byte) sentHello {
	t.Helper()
	s := sentHello{
		recordVersion: binary.BigEndian.Uint16(records[1:]),
	}
	b := msg[4:]
	s.legacyVersion = binary.BigEndian.Uint16(b)
	b = b[34:]
	s.sessionIDLength = int(b[0])
	b = b[1+int(b[0]):]
	b = b[2+int(binary.BigEndian.Uint16(b)):]
	b = b[1+int(b[0]):]
	exts := b[2:]
	for len(exts) > 0 {
		typ := profile.Code(binary.BigEndian.Uint16(exts))
		n := int(binary.BigEndian.Uint16(exts[2:]))
		data := exts[4 : 4+n]
		exts = exts[4+n:]
		switch typ {
		case profile.ExtKeyShare:
			for list := data[2:]; len(list) > 0; {
				n := binary.BigEndian.Uint16(list[2:])
				s.keyShareLengths = append(s.keyShareLengths, n)
				list = list[4+int(n):]
			}
		case profile.ExtEncryptedClientHello:
			if data[0] != 0 {
				t.Fatalf("ECH type %d, want an outer ClientHello's", data[0])
			}
			enc := binary.BigEndian.Uint16(data[6:])
			s.ech = &profile.ECH{
				CipherSuites: []profile.HPKESuite{
					{
						KDF:  profile.Code(binary.BigEndian.Uint16(data[1:])),
						AEAD: profile.Code(binary.BigEndian.Uint16(data[3:])),
					},
				},
				EncLength: enc,
				PayloadLengths: []uint16{
					binary.BigEndian.Uint16(data[8+int(enc):]),
				},
			}
		}
	}
	return s
}

// Every built-in profile's ClientHello leaves with the profile's shape: TLS
// 1.0 records, its legacy_version and session ID length, its key share
// lengths, and a GREASE ECH of its encapsulated key length whose cipher
// suite and payload length are among the profile's, on every connection.
func TestClientSendsTheProfilesShape(t *testing.T) {
	for _, name := range profiles.Names() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.ByName(name)
			if err != nil {
				t.Fatal(err)
			}
			var keyShares *profile.Extension
			var ech *profile.ECH
			for i := range p.TLS.Extensions {
				switch e := &p.TLS.Extensions[i]; e.Type {
				case profile.ExtKeyShare:
					keyShares = e
				case profile.ExtEncryptedClientHello:
					ech = e.ECH
				}
			}
			plan := fingerprint.Compile(p)
			for range 8 {
				got := sendHello(t, plan.Hello())
				if profile.Code(got.recordVersion) != p.TLS.RecordVersion {
					t.Errorf("record version %#04x, want %s", got.recordVersion, p.TLS.RecordVersion)
				}
				if profile.Code(got.legacyVersion) != p.TLS.LegacyVersion || got.sessionIDLength != int(p.TLS.SessionIDLength) {
					t.Errorf("legacy_version %#04x, %d-byte session ID; want %s, %d", got.legacyVersion, got.sessionIDLength, p.TLS.LegacyVersion, p.TLS.SessionIDLength)
				}
				if !slices.Equal(got.keyShareLengths, keyShares.KeyShareLengths) {
					t.Errorf("key share lengths %v, want %v", got.keyShareLengths, keyShares.KeyShareLengths)
				}
				if (got.ech == nil) != (ech == nil) {
					t.Fatalf("ECH sent %v, in the profile %v", got.ech != nil, ech != nil)
				}
				if ech == nil {
					continue
				}
				if got.ech.EncLength != ech.EncLength ||
					!slices.Contains(ech.CipherSuites, got.ech.CipherSuites[0]) ||
					!slices.Contains(ech.PayloadLengths, got.ech.PayloadLengths[0]) {
					t.Errorf("ECH %+v, outside the profile's %+v", *got.ech, *ech)
				}
			}
		})
	}
}

func TestSpecRefusesShapeUTLSCannotSend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(h *fingerprint.Hello)
		want   string
	}{
		{
			name: "record version",
			mutate: func(h *fingerprint.Hello) {
				h.RecordVersion = 0x0303
			},
			want: "record version 0x0303",
		},
		{
			name: "legacy_version",
			mutate: func(h *fingerprint.Hello) {
				h.LegacyVersion = 0x0301
			},
			want: "legacy_version 0x0301",
		},
		{
			name: "session ID",
			mutate: func(h *fingerprint.Hello) {
				h.SessionIDLength = 0
			},
			want: "0-byte session ID",
		},
		{
			name: "key share lengths",
			mutate: func(h *fingerprint.Hello) {
				for i := range h.Extensions {
					if h.Extensions[i].Type == profile.ExtKeyShare {
						h.Extensions[i].KeyShareLengths = nil
					}
				}
			},
			want: "0 key share lengths",
		},
		{
			name: "ECH encapsulated key",
			mutate: func(h *fingerprint.Hello) {
				for i := range h.Extensions {
					if e := &h.Extensions[i]; e.ECH != nil {
						ech := *e.ECH
						ech.EncLength = 65
						e.ECH = &ech
					}
				}
			},
			want: "encapsulated key of 65 bytes",
		},
		{
			name: "ECH AEAD",
			mutate: func(h *fingerprint.Hello) {
				for i := range h.Extensions {
					if e := &h.Extensions[i]; e.ECH != nil {
						ech := *e.ECH
						ech.CipherSuites = []profile.HPKESuite{
							{
								KDF:  1,
								AEAD: 0xffff,
							},
						}
						e.ECH = &ech
					}
				}
			},
			want: "AEAD 0xffff",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := fingerprint.Compile(profiles.Chrome155MacOS()).Hello()
			tc.mutate(&h)
			if _, err := Spec(h); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Spec error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
