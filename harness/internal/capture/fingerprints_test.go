package capture

import (
	"strings"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// chromeLikeTLS has Chrome's cipher list and a typical extension set.
func chromeLikeTLS() profile.TLS {
	return profile.TLS{
		CipherSuites: []profile.Code{
			profile.GREASE, 0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f, 0xc02c, 0xc030,
			0xcca9, 0xcca8, 0xc013, 0xc014, 0x009c, 0x009d, 0x002f, 0x0035,
		},
		CompressionMethods: profile.ByteList{
			0,
		},
		Extensions: []profile.Extension{
			{
				Type: profile.GREASE,
			},
			{
				Type: profile.ExtServerName,
			},
			{
				Type: 0x0017,
			},
			{
				Type: profile.ExtSupportedGroups,
				Groups: []profile.Code{
					profile.GREASE,
					0x001d,
					0x0017,
				},
			},
			{
				Type: profile.ExtECPointFormats,
				PointFormats: profile.ByteList{
					0,
				},
			},
			{
				Type: profile.ExtALPN,
				ALPN: []string{
					"h2",
					"http/1.1",
				},
			},
			{
				Type: profile.ExtSignatureAlgorithms,
				SignatureAlgorithms: []profile.Code{
					profile.GREASE,
					0x0403,
					0x0804,
				},
			},
			{
				Type: profile.ExtSupportedVersions,
				Versions: []profile.Code{
					profile.GREASE,
					0x0304,
					0x0303,
				},
			},
		},
	}
}

// The cipher part of JA4 must match FoxIO's published Chrome example, which
// uses this cipher list.
func TestJA4CipherHashMatchesFoxIO(t *testing.T) {
	tls := chromeLikeTLS()
	parts := strings.Split(ja4(&tls, true), "_")
	if parts[1] != "8daaf6152771" {
		t.Fatalf("JA4 cipher hash %s, want FoxIO's 8daaf6152771", parts[1])
	}
}

// JA4 sorts, so Chrome's per-connection extension shuffle must not change it;
// JA3 keeps order, so it must.
func TestJA4StableUnderShuffle(t *testing.T) {
	a := chromeLikeTLS()
	b := chromeLikeTLS()
	b.Extensions[1], b.Extensions[6] = b.Extensions[6], b.Extensions[1]
	if ja4(&a, true) != ja4(&b, true) {
		t.Error("JA4 changed when extensions were reordered")
	}
	if ja3(&a) == ja3(&b) {
		t.Error("JA3 did not change when extensions were reordered")
	}
}

func TestJA4Prefix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(tls *profile.TLS)
		want   string
	}{
		{
			name:   "Chrome-like",
			mutate: func(*profile.TLS) {},
			want:   "t13d1507h2",
		},
		{
			name: "no server_name",
			mutate: func(tls *profile.TLS) {
				tls.Extensions = append(tls.Extensions[:1], tls.Extensions[2:]...)
			},
			want: "t13i1506h2",
		},
		{
			name: "TLS 1.2 only",
			mutate: func(tls *profile.TLS) {
				tls.Extensions = tls.Extensions[:len(tls.Extensions)-1]
			},
			want: "t12d1506h2",
		},
		{
			name: "no ALPN",
			mutate: func(tls *profile.TLS) {
				tls.Extensions[5].ALPN = nil
			},
			want: "t13d150700",
		},
		{
			name: "ALPN that is not alphanumeric",
			mutate: func(tls *profile.TLS) {
				tls.Extensions[5].ALPN = []string{
					"\xab\xcd",
				}
			},
			want: "t13d1507ad",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tls := chromeLikeTLS()
			tc.mutate(&tls)
			got, _, _ := strings.Cut(ja4(&tls, true), "_")
			if got != tc.want {
				t.Errorf("JA4 prefix %s, want %s", got, tc.want)
			}
		})
	}
}

func TestJA3ExcludesGREASE(t *testing.T) {
	tls := chromeLikeTLS()
	want := "771," +
		"4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53," +
		"0-23-10-11-16-13-43," +
		"29-23," +
		"0"
	if got := ja3(&tls); got != want {
		t.Errorf("JA3 string\n got %s\nwant %s", got, want)
	}
}

func TestAkamai(t *testing.T) {
	h := &profile.HTTP2{
		Settings: []profile.Setting{
			{
				ID:    1,
				Value: 65536,
			},
			{
				ID:    4,
				Value: 131072,
			},
		},
		Priorities: []profile.Priority{
			{
				StreamID: 3,
				PriorityParam: profile.PriorityParam{
					DependsOn: 0,
					Weight:    200,
				},
			},
			{
				StreamID: 5,
				PriorityParam: profile.PriorityParam{
					DependsOn: 3,
					Exclusive: true,
					Weight:    0,
				},
			},
		},
		PseudoHeaderOrder: []string{
			":method",
			":path",
			":authority",
			":scheme",
		},
	}
	// No WINDOW_UPDATE is "00"; weights are the real weight, wire value + 1.
	want := "1:65536;4:131072|00|3:0:0:201,5:1:3:1|m,p,a,s"
	if got := akamai(h); got != want {
		t.Errorf("Akamai\n got %s\nwant %s", got, want)
	}
}
