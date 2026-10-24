package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

const captures = "../../testdata/captures"

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a file in the repository
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The committed profiles and captures hold the shape their captured
// ClientHellos show; rerunning hellogen changes neither.
func TestRegenerateReproducesProfiles(t *testing.T) {
	paths, err := filepath.Glob("../../../profiles/data/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no profiles found")
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			data := read(t, path)
			captured := read(t, filepath.Join(captures, name+".json"))
			got, gotCapture, err := regenerate(data, captured, captures, name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("regenerating %s changes it; rerun hellogen on it and review the diff", path)
			}
			if !bytes.Equal(gotCapture, captured) {
				t.Errorf("regenerating %s changes its capture; rerun hellogen on it and review the diff", path)
			}
		})
	}
}

// A profile whose ClientHello is not the captured one is refused rather
// than given a shape that does not fit it.
func TestRegenerateRefusesAnotherHello(t *testing.T) {
	const name = "firefox-158-macos"
	p, err := profile.Parse(read(t, "../../../profiles/data/"+name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.TLS.Extensions {
		if e := &p.TLS.Extensions[i]; e.Type == profile.ExtKeyShare {
			e.KeyShares = e.KeyShares[1:]
			e.KeyShareLengths = e.KeyShareLengths[1:]
		}
	}
	data, err := profile.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = regenerate(data, read(t, filepath.Join(captures, name+".json")), captures, name)
	if err == nil || !strings.Contains(err.Error(), "tls.extensions[0x0033]") {
		t.Fatalf("error = %v, want the key_share difference", err)
	}
}

// A page connection's GREASE ECH choice joins the full hello's sets, or,
// for a TLS 1.3 resumed hello, the resumed one's payload lengths; a
// connection captured before shapes were recorded adds nothing, and one
// whose shape is not the samples' is refused.
func TestShapeAddsEveryConnectionsECH(t *testing.T) {
	ech := func(aead profile.Code, payload uint16) profile.Extension {
		return profile.Extension{
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
		}
	}
	hello := func(exts ...profile.Extension) *profile.TLS {
		return &profile.TLS{
			RecordVersion:   0x0301,
			LegacyVersion:   0x0303,
			SessionIDLength: 32,
			Extensions:      exts,
		}
	}
	psk := profile.Extension{
		Type: profile.ExtPreSharedKey,
	}
	sh := &shape{
		full: hello(ech(1, 240)),
	}
	for _, conn := range []capture.Connection{
		{
			Hello:         hello(ech(3, 240)),
			RecordVersion: 0x0301,
		},
		{
			Hello: hello(ech(1, 400), psk),
		},
		{
			Hello: &profile.TLS{},
		},
	} {
		if err := sh.add(&conn); err != nil {
			t.Fatal(err)
		}
	}
	got := sh.full.Extensions[0].ECH
	if !slices.Equal(got.CipherSuites, []profile.HPKESuite{
		{
			KDF:  1,
			AEAD: 1,
		},
		{
			KDF:  1,
			AEAD: 3,
		},
	}) || !slices.Equal(got.PayloadLengths, []uint16{
		240,
	}) {
		t.Errorf("full hello's ECH %+v", *got)
	}
	if !slices.Equal(sh.resumedECHLengths, []uint16{
		400,
	}) {
		t.Errorf("resumed payload lengths %v, want [400]", sh.resumedECHLengths)
	}
	for _, conn := range []capture.Connection{
		{
			Hello:         hello(ech(1, 240)),
			RecordVersion: 0x0303,
		},
		{
			Hello: &profile.TLS{
				LegacyVersion:   0x0303,
				SessionIDLength: 0,
			},
		},
		{
			Hello: hello(),
		},
	} {
		if err := sh.add(&conn); err == nil {
			t.Errorf("accepted %+v", conn)
		}
	}
}
