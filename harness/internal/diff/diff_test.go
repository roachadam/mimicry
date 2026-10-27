package diff

import (
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
