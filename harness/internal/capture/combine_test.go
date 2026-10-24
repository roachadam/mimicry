package capture

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// sample is one connection's capture with a GREASE ECH extension of the
// given suite and payload length, sent in TLS 1.0 records.
func sample(aead profile.Code, payload uint16) *Capture {
	return &Capture{
		Profile: profile.Profile{
			TLS: profile.TLS{
				LegacyVersion:   0x0303,
				SessionIDLength: 32,
				Extensions: []profile.Extension{
					{
						Type: profile.ExtKeyShare,
						KeyShares: []profile.Code{
							0x001d,
						},
						KeyShareLengths: []uint16{
							32,
						},
					},
					{
						Type: profile.ExtEncryptedClientHello,
						ECH: &profile.ECH{
							CipherSuites: []profile.HPKESuite{
								{
									KDF:  1,
									AEAD: aead,
								},
							},
							EncLength: 32,
							PayloadLengths: []uint16{
								payload,
							},
						},
					},
				},
			},
		},
		ClientHello: []byte{
			1,
			0,
			0,
			0,
		},
		Records: []byte{
			0x16,
			3,
			1,
			0,
			4,
			1,
			0,
			0,
			0,
		},
	}
}

// A browser picks GREASE ECH's cipher suite and payload length per
// connection: Combine keeps every one the samples show, sorted, and leaves
// the samples' own values alone.
func TestCombineMergesECHChoices(t *testing.T) {
	samples := []*Capture{
		sample(3, 208),
		sample(1, 176),
		sample(3, 176),
	}
	got, err := Combine(samples)
	if err != nil {
		t.Fatal(err)
	}
	ech := got.Profile.TLS.Extensions[1].ECH
	if !slices.Equal(ech.CipherSuites, []profile.HPKESuite{
		{
			KDF:  1,
			AEAD: 1,
		},
		{
			KDF:  1,
			AEAD: 3,
		},
	}) {
		t.Errorf("cipher suites %v", ech.CipherSuites)
	}
	if !slices.Equal(ech.PayloadLengths, []uint16{
		176,
		208,
	}) {
		t.Errorf("payload lengths %v", ech.PayloadLengths)
	}
	if l := samples[1].Profile.TLS.Extensions[1].ECH.PayloadLengths; !slices.Equal(l, []uint16{
		176,
	}) {
		t.Errorf("sample 2's own payload lengths changed to %v", l)
	}
	if len(got.SampleClientHellos) != 3 || string(got.SampleClientHellos[1]) != string(samples[1].Records) {
		t.Errorf("sample ClientHellos %x", got.SampleClientHellos)
	}
}

// Anything else a browser varies between connections is randomness a
// profile cannot describe.
func TestCombineRefusesOtherHelloDifferences(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(c *Capture)
		want   string
	}{
		{
			name: "legacy_version",
			mutate: func(c *Capture) {
				c.Profile.TLS.LegacyVersion = 0x0301
			},
			want: "tls.legacy_version",
		},
		{
			name: "session ID length",
			mutate: func(c *Capture) {
				c.Profile.TLS.SessionIDLength = 0
			},
			want: "tls.session_id_length",
		},
		{
			name: "key share length",
			mutate: func(c *Capture) {
				c.Profile.TLS.Extensions[0].KeyShareLengths[0] = 31
			},
			want: "tls.extensions[0x0033]",
		},
		{
			name: "ECH encapsulated key length",
			mutate: func(c *Capture) {
				c.Profile.TLS.Extensions[1].ECH.EncLength = 16
			},
			want: "ech.enc_length",
		},
		{
			name: "record version",
			mutate: func(c *Capture) {
				c.Records[2] = 3
			},
			want: "records carry version 0x0303",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			second := sample(1, 208)
			tc.mutate(second)
			_, err := Combine([]*Capture{
				sample(1, 208),
				second,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming %s", err, tc.want)
			}
		})
	}
}

// Older samples kept the bare handshake message, with no record version:
// they are left out of the record-version check, and the combined hello
// takes the version the samples with records agree on.
func TestCombineSkipsSamplesWithoutRecords(t *testing.T) {
	bare := sample(1, 208)
	bare.Records = nil
	got, err := Combine([]*Capture{
		sample(1, 208),
		sample(1, 176),
		bare,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Profile.TLS.RecordVersion != 0x0301 {
		t.Errorf("record version %s, want 0x0301", got.Profile.TLS.RecordVersion)
	}
	bare = sample(1, 208)
	bare.Records = nil
	if _, err := Combine([]*Capture{
		bare,
		sample(1, 208),
	}); err != nil {
		t.Errorf("a bare first sample: %v", err)
	}
}

// The .clienthello file holds every sample, one per line, and reads back.
func TestWriteKeepsEverySampleHello(t *testing.T) {
	dir := t.TempDir()
	c, err := Combine([]*Capture{
		sample(1, 208),
		sample(1, 176),
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Profile.Name = "chrome-155-macos"
	if _, err := Write(dir, c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "chrome-155-macos.clienthello")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1603010004" + "01000000\n" + "1603010004" + "01000000\n"; string(data) != want {
		t.Errorf("clienthello file %q, want %q", data, want)
	}
	hellos, err := ReadClientHellos(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hellos) != 2 || string(hellos[0]) != string(c.Records) {
		t.Errorf("read back %x", hellos)
	}
}

func TestReadClientHellosRefusesOtherBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.clienthello")
	if err := os.WriteFile(path, []byte("0100\n1703\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClientHellos(path); err == nil || !strings.Contains(err.Error(), ":2: neither") {
		t.Fatalf("error = %v", err)
	}
}
