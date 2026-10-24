package diff

import (
	"os"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

func sample(order string) *profile.Profile {
	return &profile.Profile{
		TLS: profile.TLS{
			CipherSuites: []profile.Code{
				profile.GREASE,
				0x1301,
				0x1302,
			},
			CompressionMethods: profile.ByteList{
				0,
			},
			ExtensionOrder: order,
			Extensions: []profile.Extension{
				{
					Type: profile.GREASE,
				},
				{
					Type: profile.ExtServerName,
				},
				{
					Type: profile.ExtALPN,
					ALPN: []string{
						"h2",
						"http/1.1",
					},
				},
				{
					Type: 0x0017,
				},
				{
					Type: profile.GREASE,
					Data: profile.Bytes{
						0,
					},
				},
			},
		},
		HTTP2: &profile.HTTP2{
			Settings: []profile.Setting{
				{
					ID:    1,
					Value: 65536,
				},
			},
			WindowUpdate: 15663105,
			PseudoHeaderOrder: []string{
				":method",
				":authority",
				":scheme",
				":path",
			},
		},
		Headers: []profile.Header{
			{
				Name:  "user-agent",
				Value: "test/1",
			},
			{
				Name:  "accept",
				Value: "*/*",
			},
		},
	}
}

func paths(diffs []Difference) []string {
	out := make([]string, len(diffs))
	for i, d := range diffs {
		out[i] = d.Path
	}
	return out
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		name   string
		order  string
		mutate func(p *profile.Profile)
		want   []string
	}{
		{
			name:   "identical",
			order:  profile.ExtensionOrderShuffled,
			mutate: func(*profile.Profile) {},
			want:   nil,
		},
		{
			name:  "shuffled browser, extensions reordered",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				e := p.TLS.Extensions
				e[1], e[3] = e[3], e[1]
			},
			want: nil,
		},
		{
			name:  "fixed browser, extensions reordered",
			order: profile.ExtensionOrderFixed,
			mutate: func(p *profile.Profile) {
				e := p.TLS.Extensions
				e[1], e[3] = e[3], e[1]
			},
			want: []string{
				"tls.extensions (order)",
			},
		},
		{
			name:  "shuffled browser, GREASE moved",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				e := p.TLS.Extensions
				e[0], e[1] = e[1], e[0]
			},
			want: []string{
				"tls.extensions (pinned positions)",
			},
		},
		{
			name:  "extension content",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				p.TLS.Extensions[2].ALPN = []string{
					"http/1.1",
				}
			},
			want: []string{
				"tls.extensions[0x0010]",
			},
		},
		{
			name:  "second GREASE extension body",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				p.TLS.Extensions[4].Data = nil
			},
			want: []string{
				"tls.extensions[GREASE]#2",
			},
		},
		{
			name:  "missing extension",
			order: profile.ExtensionOrderFixed,
			mutate: func(p *profile.Profile) {
				p.TLS.Extensions = append(p.TLS.Extensions[:3], p.TLS.Extensions[4])
			},
			want: []string{
				"tls.extensions (order)",
				"tls.extensions[0x0017]",
			},
		},
		{
			name:  "cipher order",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				c := p.TLS.CipherSuites
				c[1], c[2] = c[2], c[1]
			},
			want: []string{
				"tls.cipher_suites",
			},
		},
		{
			name:  "HTTP/2 setting value",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				p.HTTP2.Settings[0].Value = 4096
			},
			want: []string{
				"http2.settings",
			},
		},
		{
			name:  "no HTTP/2",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				p.HTTP2 = nil
			},
			want: []string{
				"http2",
			},
		},
		{
			name:  "header value",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				p.Headers[0].Value = "test/2"
			},
			want: []string{
				"headers[user-agent]",
			},
		},
		{
			name:  "extra header",
			order: profile.ExtensionOrderShuffled,
			mutate: func(p *profile.Profile) {
				p.Headers = append(p.Headers, profile.Header{
					Name:  "dnt",
					Value: "1",
				})
			},
			want: []string{
				"headers (order)",
				"headers[dnt]",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := sample(tc.order)
			got := sample(tc.order)
			tc.mutate(got)
			if p := paths(Compare(want, got)); strings.Join(p, "|") != strings.Join(tc.want, "|") {
				t.Errorf("differences at %q, want %q", p, tc.want)
			}
		})
	}
}

// A header name sent twice is compared occurrence by occurrence, not through
// its last value.
func TestCompareRepeatedHeaders(t *testing.T) {
	headers := func(values ...string) []profile.Header {
		hs := make([]profile.Header, 0, len(values))
		for _, v := range values {
			hs = append(hs, profile.Header{
				Name:  "cookie",
				Value: v,
			})
		}
		return hs
	}
	for _, tc := range []struct {
		name      string
		want, got []string
		diffs     []string
	}{
		{
			name:  "identical",
			want:  []string{"a", "b"},
			got:   []string{"a", "b"},
			diffs: nil,
		},
		{
			name: "first value differs",
			want: []string{"a", "b"},
			got:  []string{"b", "b"},
			diffs: []string{
				"headers[cookie]#1",
			},
		},
		{
			name: "one sent fewer times",
			want: []string{"a", "b"},
			got:  []string{"a"},
			diffs: []string{
				"headers (order)",
				"headers[cookie]#2",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := sample(profile.ExtensionOrderFixed)
			got := sample(profile.ExtensionOrderFixed)
			want.Headers = headers(tc.want...)
			got.Headers = headers(tc.got...)
			if p := paths(Compare(want, got)); strings.Join(p, "|") != strings.Join(tc.diffs, "|") {
				t.Errorf("differences at %q, want %q", p, tc.diffs)
			}
		})
	}
}

func loadCapture(t *testing.T, name string) *profile.Profile {
	t.Helper()
	data, err := os.ReadFile("../../testdata/captures/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := profile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Real captures: each browser matches itself, and different browsers differ in
// their ClientHello, HTTP/2 settings and headers.
func TestCompareCaptures(t *testing.T) {
	chrome := loadCapture(t, "chrome-155-macos")
	firefox := loadCapture(t, "firefox-158-macos")
	safari := loadCapture(t, "safari-27-macos")
	for _, p := range []*profile.Profile{chrome, firefox, safari} {
		if diffs := Compare(p, p); len(diffs) > 0 {
			t.Errorf("%s differs from itself: %v", p.Name, diffs)
		}
	}
	for _, pair := range [][2]*profile.Profile{
		{chrome, firefox},
		{firefox, safari},
		{safari, chrome},
	} {
		got := strings.Join(paths(Compare(pair[0], pair[1])), "|")
		for _, want := range []string{"tls.cipher_suites", "http2.settings", "headers (order)"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s vs %s: no difference at %s", pair[0].Name, pair[1].Name, want)
			}
		}
	}
}
