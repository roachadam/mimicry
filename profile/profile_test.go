package profile

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func readValid(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseValid(t *testing.T) *Profile {
	t.Helper()
	p, err := Parse(readValid(t))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The built-in profiles and harness captures are stored in Encode's format, so
// decoding and re-encoding must not change a single byte.
func TestEncodeRoundTrip(t *testing.T) {
	in := readValid(t)
	p, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("re-encoded profile differs from testdata/valid.json:\n%s", out)
	}
}

func TestCodeText(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Code
		text string
	}{
		{
			in:   "GREASE",
			want: GREASE,
			text: "GREASE",
		},
		{
			in:   "0x1a1a",
			want: GREASE,
			text: "GREASE",
		},
		{
			in:   "0xfafa",
			want: GREASE,
			text: "GREASE",
		},
		{
			in:   "0x1301",
			want: 0x1301,
			text: "0x1301",
		},
		{
			in:   "0x0",
			want: 0,
			text: "0x0000",
		},
		{
			in:   "0x0a1a",
			want: 0x0a1a,
			text: "0x0a1a",
		},
	} {
		var c Code
		if err := c.UnmarshalText([]byte(tc.in)); err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if c != tc.want {
			t.Errorf("%q parsed as %#04x, want %#04x", tc.in, uint16(c), uint16(tc.want))
		}
		if got, _ := c.MarshalText(); string(got) != tc.text {
			t.Errorf("%q encoded as %q, want %q", tc.in, got, tc.text)
		}
	}
	for _, in := range []string{"", "1301", "0x", "0x10000", "0xzz", "grease"} {
		var c Code
		if err := c.UnmarshalText([]byte(in)); err == nil {
			t.Errorf("%q: accepted, want an error", in)
		}
	}
}

func TestParseRejects(t *testing.T) {
	valid := string(readValid(t))
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "unknown field",
			in:   strings.Replace(valid, `"platform"`, `"os"`, 1),
			want: "unknown field",
		},
		{
			name: "trailing data",
			in:   valid + "{}",
			want: "trailing data",
		},
		{
			name: "byte out of range",
			in: strings.Replace(valid, `"compression_methods": [
      0
    ]`, `"compression_methods": [
      256
    ]`, 1),
			want: "does not fit in a byte",
		},
		{
			name: "bad hex",
			in:   strings.Replace(valid, `"data": "0100000000"`, `"data": "01x"`, 1),
			want: "invalid hex",
		},
		{
			name: "invalid profile",
			in:   strings.Replace(valid, `"name": "example-chrome-like"`, `"name": ""`, 1),
			want: "name: is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.in == valid {
				t.Fatal("test input was not modified; the replacement target is missing")
			}
			_, err := Parse([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func extIndex(t *testing.T, p *Profile, typ Code) int {
	t.Helper()
	for i := range p.TLS.Extensions {
		if p.TLS.Extensions[i].Type == typ {
			return i
		}
	}
	t.Fatalf("extension %s not in test profile", typ)
	return -1
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, p *Profile)
		path   string
		want   string
	}{
		{
			name: "no cipher suites",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.CipherSuites = nil
			},
			path: "tls.cipher_suites",
			want: "is required",
		},
		{
			name: "unknown extension order",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.ExtensionOrder = "random"
			},
			path: "tls.extension_order",
			want: `must be "fixed" or "shuffled"`,
		},
		{
			name: "repeated key share group",
			mutate: func(t *testing.T, p *Profile) {
				ks := &p.TLS.Extensions[extIndex(t, p, ExtKeyShare)]
				ks.KeyShares = append(ks.KeyShares, 0x001d)
			},
			path: "tls.extensions[11].key_shares",
			want: "repeats 0x001d",
		},
		{
			name: "repeated extension",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Extensions = append(p.TLS.Extensions, Extension{
					Type: 0x0017,
				})
			},
			path: "tls.extensions[19]",
			want: "duplicates extension 0x0017",
		},
		{
			name: "pre_shared_key not last",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Extensions = append([]Extension{
					{
						Type: ExtPreSharedKey,
					},
				}, p.TLS.Extensions...)
			},
			path: "tls.extensions[0]",
			want: "must be the last extension",
		},
		{
			name: "payload on a generated extension",
			mutate: func(t *testing.T, p *Profile) {
				p.TLS.Extensions[extIndex(t, p, ExtServerName)].Data = Bytes{
					0x00,
				}
			},
			path: "tls.extensions[1]",
			want: "generated per connection; remove data",
		},
		{
			name: "typed extension without its field",
			mutate: func(t *testing.T, p *Profile) {
				p.TLS.Extensions[extIndex(t, p, ExtSupportedVersions)].Versions = nil
			},
			path: "tls.extensions[13]",
			want: "needs versions",
		},
		{
			name: "typed extension with the wrong field",
			mutate: func(t *testing.T, p *Profile) {
				p.TLS.Extensions[extIndex(t, p, ExtALPN)].ALPS = []string{
					"h2",
				}
			},
			path: "tls.extensions[7]",
			want: "takes alpn, not alps",
		},
		{
			name: "typed field on a data extension",
			mutate: func(t *testing.T, p *Profile) {
				p.TLS.Extensions[extIndex(t, p, 0x0005)].Groups = []Code{
					0x001d,
				}
			},
			path: "tls.extensions[8]",
			want: "takes data, not groups",
		},
		{
			name: "key share outside supported_groups",
			mutate: func(t *testing.T, p *Profile) {
				ks := &p.TLS.Extensions[extIndex(t, p, ExtKeyShare)]
				ks.KeyShares = append(ks.KeyShares, 0x0019)
			},
			path: "tls.extensions.key_share",
			want: "group 0x0019 is not in supported_groups",
		},
		{
			name: "h2 offered without http2",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2 = nil
			},
			path: "http2",
			want: "ALPN offers h2",
		},
		{
			name: "repeated setting",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.Settings = append(p.HTTP2.Settings, Setting{
					ID:    1,
					Value: 4096,
				})
			},
			path: "http2.settings[4]",
			want: "repeats setting 1",
		},
		{
			name: "window update too large",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.WindowUpdate = 1 << 31
			},
			path: "http2.window_update",
			want: "exceeds",
		},
		{
			name: "priority on itself",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.Priorities = []Priority{
					{
						StreamID: 3,
						PriorityParam: PriorityParam{
							DependsOn: 3,
						},
					},
				}
			},
			path: "http2.priorities[0]",
			want: "cannot depend on itself",
		},
		{
			name: "incomplete pseudo-header order",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.PseudoHeaderOrder = p.HTTP2.PseudoHeaderOrder[:3]
			},
			path: "http2.pseudo_header_order",
			want: "exactly once",
		},
		{
			name: "even first stream ID",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.FirstStreamID = 2
			},
			path: "http2.first_stream_id",
			want: "odd stream ID",
		},
		{
			name: "chained priorities without a priority",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.ChainPriorities = true
				p.HTTP2.HeadersPriority = nil
			},
			path: "http2.chain_priorities",
			want: "needs headers_priority",
		},
		{
			name: "update threshold beyond any window",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.ConnectionUpdateThreshold = 1 << 31
			},
			path: "http2.connection_update_threshold",
			want: "maximum window",
		},
		{
			name: "unknown HPACK name match",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.HPACK = &HPACK{
					NameMatch: "lowest",
					Huffman:   HuffmanShorter,
				}
			},
			path: "http2.hpack.name_match",
			want: `must be "static_first" or "highest_index"`,
		},
		{
			name: "header both without indexing and never indexed",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.HPACK = &HPACK{
					WithoutIndexing: []string{
						"cookie",
					},
					NeverIndexed: []string{
						"cookie",
					},
					NameMatch: NameMatchStaticFirst,
					Huffman:   HuffmanShorter,
				}
			},
			path: "http2.hpack.without_indexing[0]",
			want: "also in never_indexed",
		},
		{
			name: "uppercase header",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers[2].Name = "User-Agent"
			},
			path: "headers[2]",
			want: "must be lowercase",
		},
		{
			name: "repeated header",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers = append(p.Headers, Header{
					Name: "accept",
				})
			},
			path: "headers[7]",
			want: "repeats header accept",
		},
		{
			name: "pseudo-header in headers",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers[0].Name = ":path"
			},
			path: "headers[0]",
			want: "belongs in http2.pseudo_header_order",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := parseValid(t)
			tc.mutate(t, p)
			err := p.Validate()
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("Validate() = %v, want a *ValidationError", err)
			}
			for _, problem := range verr.Problems {
				if problem.Path == tc.path && strings.Contains(problem.Message, tc.want) {
					return
				}
			}
			t.Fatalf("no problem at %s containing %q; got:\n%v", tc.path, tc.want, err)
		})
	}
}

// Safari lists a signature algorithm twice; repeats the protocols allow must
// stay expressible.
func TestValidateAllowsRepeatsBrowsersSend(t *testing.T) {
	p := parseValid(t)
	sa := &p.TLS.Extensions[extIndex(t, p, ExtSignatureAlgorithms)]
	sa.SignatureAlgorithms = append(sa.SignatureAlgorithms, 0x0805)
	p.TLS.CipherSuites = append(p.TLS.CipherSuites, 0x1301)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}
