package profile

import (
	"bytes"
	"errors"
	"os"
	"slices"
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
			want: `unknown object member name "os"`,
		},
		{
			name: "miscased key",
			in:   strings.Replace(valid, `"extension_order"`, `"Extension_Order"`, 1),
			want: `unknown object member name "Extension_Order"`,
		},
		{
			name: "repeated key",
			in:   strings.Replace(valid, `"name": "example-chrome-like",`, `"name": "example-chrome-like", "name": "other",`, 1),
			want: `duplicate object member name "name"`,
		},
		{
			name: "key repeated in a map",
			in: strings.Replace(valid, `"size_updates": "on_change"
    }`, `"size_updates": "on_change",
      "never_index_below": {
        "cookie": 20,
        "cookie": 21
      }
    }`, 1),
			want: `duplicate object member name "cookie"`,
		},
		{
			name: "trailing data",
			in:   valid + "{}",
			want: "after top-level value",
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
				// With the initial 65535, one past the largest window.
				p.HTTP2.WindowUpdate = 1<<31 - 65535
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
			name: "non-positive never-index length",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.HPACK = &HPACK{
					NeverIndexBelow: map[string]int{
						"cookie": 0,
					},
					NameMatch:   NameMatchStaticFirst,
					Huffman:     HuffmanShorter,
					SizeUpdates: SizeUpdatesOnChange,
				}
			},
			path: "http2.hpack.never_index_below[cookie]",
			want: "positive length",
		},
		{
			name: "unknown size update rule",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.HPACK = &HPACK{
					NameMatch:   NameMatchStaticFirst,
					Huffman:     HuffmanShorter,
					SizeUpdates: "sometimes",
				}
			},
			path: "http2.hpack.size_updates",
			want: `must be "on_change" or "always"`,
		},
		{
			name: "unknown third-party policy",
			mutate: func(_ *testing.T, p *Profile) {
				p.Cookies = DefaultCookies()
				p.Cookies.ThirdParty = "block_all"
			},
			path: "cookies.third_party",
			want: "must be one of",
		},
		{
			name: "Lax exception without Lax by default",
			mutate: func(_ *testing.T, p *Profile) {
				p.Cookies = DefaultCookies()
				p.Cookies.UnsetSameSite = SameSiteNone
				p.Cookies.LaxAllowsUnsafeSeconds = 120
			},
			path: "cookies.lax_allows_unsafe_seconds",
			want: "applies only when",
		},
		{
			name: "HTTP/1.1 header repeated in another case",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP1 = &HTTP1{
					Headers: []Header{
						{
							Name: "Host",
						},
						{
							Name:  "Accept",
							Value: "*/*",
						},
						{
							Name:  "accept",
							Value: "*/*",
						},
					},
				}
			},
			path: "http1.headers[2]",
			want: "repeats header accept",
		},
		{
			name: "HTTP/1.1 headers without Host",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP1 = &HTTP1{
					Headers: []Header{
						{
							Name:  "Accept",
							Value: "*/*",
						},
					},
				}
			},
			path: "http1.headers",
			want: "must place Host",
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
		{
			name: "unknown kind",
			mutate: func(_ *testing.T, p *Profile) {
				p.Kinds = map[string]*Kind{
					"websocket": {
						Headers: p.Headers,
					},
				}
			},
			path: "kinds.websocket",
			want: "is not a request kind",
		},
		{
			name: "other-headers marker in the navigation",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers[1].Name = OtherHeaders
			},
			path: "headers[1]",
			want: "belongs only in a kind's lists",
		},
		{
			name: "variant repeating the default condition",
			mutate: func(_ *testing.T, p *Profile) {
				p.Variants = []Variant{
					{
						Headers: p.Headers,
					},
				}
			},
			path: "variants[0]",
			want: "repeats the default condition",
		},
		{
			name: "two variants with one condition",
			mutate: func(_ *testing.T, p *Profile) {
				v := Variant{
					Method:  "POST",
					Site:    SiteCrossSite,
					Headers: p.Headers,
				}
				p.Variants = []Variant{v, v}
			},
			path: "variants[1]",
			want: "repeats an earlier variant's condition",
		},
		{
			name: "variant with an unknown site",
			mutate: func(_ *testing.T, p *Profile) {
				p.Variants = []Variant{
					{
						Site:    "same-origin",
						Headers: p.Headers,
					},
				}
			},
			path: "variants[0].site",
			want: "must be empty (same-origin)",
		},
		{
			name: "page-set headers without cors_checked",
			mutate: func(_ *testing.T, p *Profile) {
				p.Variants = []Variant{
					{
						Method:  "POST",
						PageSet: true,
						Headers: p.Headers,
					},
				}
			},
			path: "variants[0].page_set",
			want: "needs cors_checked",
		},
		{
			name: "two page-set variants with one condition",
			mutate: func(_ *testing.T, p *Profile) {
				v := Variant{
					CORSChecked: true,
					PageSet:     true,
					Headers:     p.Headers,
				}
				p.Variants = []Variant{v, v}
			},
			path: "variants[1]",
			want: "repeats an earlier variant's condition",
		},
		{
			name: "name with an empty part",
			mutate: func(_ *testing.T, p *Profile) {
				p.Name = "chrome--macos"
			},
			path: "name",
			want: "lowercase letters and digits in parts joined by dashes",
		},
		{
			name: "name with a dot",
			mutate: func(_ *testing.T, p *Profile) {
				p.Name = "safari-26.1-macos"
			},
			path: "name",
			want: "lowercase letters and digits in parts joined by dashes",
		},
		{
			name: "TLS 1.2 only",
			mutate: func(t *testing.T, p *Profile) {
				p.TLS.Extensions[extIndex(t, p, ExtSupportedVersions)].Versions = []Code{
					0x0303,
				}
			},
			path: "tls.extensions",
			want: "must offer TLS 1.3",
		},
		{
			name: "no supported_versions",
			mutate: func(_ *testing.T, p *Profile) {
				removeExtension(p, ExtSupportedVersions)
			},
			path: "tls.extensions",
			want: "must offer TLS 1.3",
		},
		{
			name: "three GREASE extensions",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Extensions = append(p.TLS.Extensions, Extension{
					Type: GREASE,
				})
			},
			path: "tls.extensions[19]",
			want: "more than 2 GREASE extensions",
		},
		{
			name: "first GREASE extension with a body uTLS does not send",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Extensions[0].Data = Bytes{
					1, 2, 3, 4,
				}
			},
			path: "tls.extensions[0]",
			want: "body must be empty, the one uTLS sends, not 01020304",
		},
		{
			name: "second GREASE extension without the body uTLS sends",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Extensions[17].Data = nil
			},
			path: "tls.extensions[17]",
			want: "body must be 00",
		},
		{
			name: "status_request body uTLS does not send",
			mutate: func(t *testing.T, p *Profile) {
				p.TLS.Extensions[extIndex(t, p, ExtStatusRequest)].Data = Bytes{
					1, 0,
				}
			},
			path: "tls.extensions[8]",
			want: "body must be 0100000000",
		},
		{
			name: "extended_master_secret with a body",
			mutate: func(t *testing.T, p *Profile) {
				p.TLS.Extensions[extIndex(t, p, ExtExtendedMasterSecret)].Data = Bytes{
					0,
				}
			},
			path: "tls.extensions[2]",
			want: "body must be empty",
		},
		{
			name: "resumption leaving out key_share",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Resumption = &Resumption{
					Drop: []Code{
						ExtKeyShare,
					},
				}
			},
			path: "tls.resumption.drop[0]",
			want: "cannot leave out extension 0x0033",
		},
		{
			name: "resumption leaving out an extension twice",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Resumption = &Resumption{
					Drop: []Code{
						ExtSessionTicket,
						ExtSessionTicket,
					},
				}
			},
			path: "tls.resumption.drop",
			want: "repeats 0x0023",
		},
		{
			name: "resumption leaving out an extension not sent",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Resumption = &Resumption{
					Drop: []Code{
						ExtDelegatedCredentials,
					},
				}
			},
			path: "tls.resumption.drop[0]",
			want: "is not in the ClientHello",
		},
		{
			name: "resumption without psk_key_exchange_modes",
			mutate: func(_ *testing.T, p *Profile) {
				removeExtension(p, ExtPSKKeyExchangeModes)
				p.TLS.Resumption = &Resumption{}
			},
			path: "tls.resumption",
			want: "needs psk_key_exchange_modes",
		},
		{
			name: "TLS 1.2 tickets without session_ticket",
			mutate: func(_ *testing.T, p *Profile) {
				removeExtension(p, ExtSessionTicket)
				p.TLS.Resumption = &Resumption{
					TLS12: TLS12Reuse,
				}
			},
			path: "tls.resumption.tls12",
			want: "needs session_ticket",
		},
		{
			name: "unknown TLS 1.2 ticket use",
			mutate: func(_ *testing.T, p *Profile) {
				p.TLS.Resumption = &Resumption{
					TLS12: "forever",
				}
			},
			path: "tls.resumption.tls12",
			want: "must be empty",
		},
		{
			name: "server push enabled",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.Settings[1].Value = 1
			},
			path: "http2.settings[1]",
			want: "SETTINGS_ENABLE_PUSH must be 0",
		},
		{
			name: "initial window beyond the largest",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.Settings[2].Value = 1 << 31
			},
			path: "http2.settings[2]",
			want: "exceeds the maximum window",
		},
		{
			name: "frame size below the smallest",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.Settings = append(p.HTTP2.Settings, Setting{
					ID:    5,
					Value: 1<<14 - 1,
				})
			},
			path: "http2.settings[4]",
			want: "SETTINGS_MAX_FRAME_SIZE must be from 2^14 to 2^24-1",
		},
		{
			name: "frame size beyond the largest",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.Settings = append(p.HTTP2.Settings, Setting{
					ID:    5,
					Value: 1 << 24,
				})
			},
			path: "http2.settings[4]",
			want: "SETTINGS_MAX_FRAME_SIZE must be from 2^14 to 2^24-1",
		},
		{
			name: "stream window beyond the largest",
			mutate: func(_ *testing.T, p *Profile) {
				// SETTINGS_INITIAL_WINDOW_SIZE is 6291456.
				p.HTTP2.StreamWindowUpdate = 1<<31 - 6291456
			},
			path: "http2.stream_window_update",
			want: "exceeds the maximum window",
		},
		{
			name: "priority on a stream beyond 2^31",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.Priorities = []Priority{
					{
						StreamID: 1 << 31,
					},
				}
			},
			path: "http2.priorities[0]",
			want: "stream_id must be below 2^31",
		},
		{
			name: "priority depending on a stream beyond 2^31",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.Priorities = []Priority{
					{
						StreamID: 3,
						PriorityParam: PriorityParam{
							DependsOn: 1<<31 + 1,
						},
					},
				}
			},
			path: "http2.priorities[0]",
			want: "depends_on must be below 2^31",
		},
		{
			name: "HTTP/2 without HPACK rules",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP2.HPACK = nil
			},
			path: "http2.hpack",
			want: "is required",
		},
		{
			name: "header value with CR LF",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers[2].Value = "x\r\nInjected: 1"
			},
			path: "headers[2]",
			want: "must not contain CR, LF or NUL",
		},
		{
			name: "header value with NUL",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers[2].Value = "x\x00"
			},
			path: "headers[2]",
			want: "must not contain CR, LF or NUL",
		},
		{
			name: "header name not a token",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers[0].Name = "x y"
			},
			path: "headers[0]",
			want: "is not a header name",
		},
		{
			name: "connection header over HTTP/2",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers = append(p.Headers, Header{
					Name:  "connection",
					Value: "keep-alive",
				})
			},
			path: "headers[7]",
			want: "connection-specific",
		},
		{
			name: "host header over HTTP/2",
			mutate: func(_ *testing.T, p *Profile) {
				p.Headers = append(p.Headers, Header{
					Name: "host",
				})
			},
			path: "headers[7]",
			want: "connection-specific",
		},
		{
			name: "HTTP/1.1 header value with LF",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP1 = &HTTP1{
					Headers: []Header{
						{
							Name: "Host",
						},
						{
							Name:  "Accept",
							Value: "a\nb",
						},
					},
				}
			},
			path: "http1.headers[1]",
			want: "must not contain CR, LF or NUL",
		},
		{
			name: "HTTP/1.1 header name not a token",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP1 = &HTTP1{
					Headers: []Header{
						{
							Name: "Host",
						},
						{
							Name: "Bad(Name)",
						},
					},
				}
			},
			path: "http1.headers[1]",
			want: "is not a header name",
		},
		{
			name: "CONNECT without Host",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.Connect = p.Proxy.Connect[1:]
			},
			path: "proxy.connect",
			want: "must place Host",
		},
		{
			name: "CONNECT user agent with none in headers",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Headers = slices.DeleteFunc(p.Headers, func(h Header) bool {
					return h.Name == "user-agent"
				})
			},
			path: "proxy.connect",
			want: "places User-Agent, but headers has no user-agent",
		},
		{
			name: "credential timing without connect_auth",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.ConnectAuth = nil
			},
			path: "proxy",
			want: "need connect_auth",
		},
		{
			name: "connect_auth without Proxy-Authorization",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.ConnectAuth = p.Proxy.Connect
			},
			path: "proxy.connect_auth",
			want: "must place Proxy-Authorization",
		},
		{
			name: "CONNECT credentials sent always",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.ConnectCredentials = CredentialsAlways
			},
			path: "proxy.connect_credentials",
			want: `must be "after_challenge" or "on_challenge"`,
		},
		{
			name: "forward credentials on challenge",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.ForwardCredentials = CredentialsOnChallenge
			},
			path: "proxy.forward_credentials",
			want: `must be "after_challenge" or "always"`,
		},
		{
			name: "forward auth both after and before",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.ForwardAuth.Before = "accept"
			},
			path: "proxy.forward_auth",
			want: "not both",
		},
		{
			name: "forward connection not a header name",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.ForwardConnection = "X\r\nY"
			},
			path: "proxy.forward_connection",
			want: "is not a header name",
		},
		{
			name: "SOCKS5 offering authentication",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.SOCKS5.Methods = []int{
					0,
					2,
				}
			},
			path: "proxy.socks5.methods",
			want: "must be [0]",
		},
		{
			name: "SOCKS5 naming targets by address",
			mutate: func(_ *testing.T, p *Profile) {
				p.Proxy = validProxy()
				p.Proxy.SOCKS5.Address = "ipv4"
			},
			path: "proxy.socks5.address",
			want: `must be "domain"`,
		},
		{
			name: "nil kind",
			mutate: func(_ *testing.T, p *Profile) {
				p.Kinds = map[string]*Kind{
					KindImage: nil,
				}
			},
			path: "kinds.image",
			want: "is empty",
		},
		{
			name: "urgency that changes nothing",
			mutate: func(_ *testing.T, p *Profile) {
				p.Kinds = map[string]*Kind{
					KindImage: {
						Headers: p.Headers,
						High:    &Urgency{},
					},
				}
			},
			path: "kinds.image.high",
			want: "changes nothing",
		},
		{
			name: "kind without HTTP/1.1 headers when the navigation has them",
			mutate: func(_ *testing.T, p *Profile) {
				p.HTTP1 = &HTTP1{
					Headers: []Header{
						{
							Name: "Host",
						},
					},
				}
				p.Kinds = map[string]*Kind{
					KindImage: {
						Headers: p.Headers,
					},
				}
			},
			path: "kinds.image.http1",
			want: "is required because the profile has http1",
		},
		{
			name: "variant with method GET",
			mutate: func(_ *testing.T, p *Profile) {
				p.Variants = []Variant{
					{
						Method:  "GET",
						Site:    SiteCrossSite,
						Headers: p.Headers,
					},
				}
			},
			path: "variants[0].method",
			want: "must be empty for GET",
		},
		{
			name: "non-secure variant with HTTP/2 headers",
			mutate: func(_ *testing.T, p *Profile) {
				p.Variants = []Variant{
					{
						Insecure: true,
						Headers:  p.Headers,
						HTTP1: []Header{
							{
								Name: "Host",
							},
						},
					},
				}
			},
			path: "variants[0].headers",
			want: "must be empty",
		},
		{
			name: "non-secure variant without HTTP/1.1 headers",
			mutate: func(_ *testing.T, p *Profile) {
				p.Variants = []Variant{
					{
						Insecure: true,
					},
				}
			},
			path: "variants[0].http1",
			want: "is required for a non-secure variant",
		},
		{
			name: "unknown preflight cache",
			mutate: func(_ *testing.T, p *Profile) {
				p.Context = &Context{
					PreflightCache: "forever",
				}
			},
			path: "context.preflight_cache",
			want: `must be empty, "replace" or "merge"`,
		},
		{
			name: "no cookie rules",
			mutate: func(_ *testing.T, p *Profile) {
				p.Cookies = nil
			},
			path: "cookies",
			want: "is required",
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

func removeExtension(p *Profile, typ Code) {
	p.TLS.Extensions = slices.DeleteFunc(p.TLS.Extensions, func(e Extension) bool {
		return e.Type == typ
	})
}

// validProxy returns a proxy section TestValidateAcceptsLimits shows valid.
func validProxy() *Proxy {
	return &Proxy{
		Connect: []Header{
			{
				Name: "Host",
			},
			{
				Name: "User-Agent",
			},
		},
		ConnectAuth: []Header{
			{
				Name: "Host",
			},
			{
				Name: "User-Agent",
			},
			{
				Name: "Proxy-Authorization",
			},
		},
		ConnectCredentials: CredentialsAfterChallenge,
		ForwardCredentials: CredentialsAfterChallenge,
		ForwardConnection:  "Proxy-Connection",
		ForwardAuth: &ForwardAuth{
			After: []string{
				"connection",
			},
		},
		SOCKS5: &SOCKS5{
			Methods: []int{
				0,
			},
			Address: SOCKS5Domain,
		},
	}
}

// What the protocols and mimicry allow at their limits stays valid: the
// largest windows and frame size, the last stream ID, a full proxy section,
// and a secure variant measured over HTTP/2 only.
func TestValidateAcceptsLimits(t *testing.T) {
	p := parseValid(t)
	p.HTTP2.WindowUpdate = 1<<31 - 1 - 65535
	p.HTTP2.Settings[2].Value = 1<<31 - 1
	p.HTTP2.Settings = append(p.HTTP2.Settings, Setting{
		ID:    5,
		Value: 1<<24 - 1,
	})
	p.HTTP2.Priorities = []Priority{
		{
			StreamID: 1<<31 - 1,
			PriorityParam: PriorityParam{
				DependsOn: 1<<31 - 3,
			},
		},
	}
	p.Proxy = validProxy()
	p.HTTP1 = &HTTP1{
		Headers: []Header{
			{
				Name: "Host",
			},
		},
	}
	p.Variants = []Variant{
		{
			Method:  "POST",
			Headers: p.Headers,
		},
	}
	p.TLS.Resumption = &Resumption{
		Drop: []Code{
			ExtSessionTicket,
		},
		TLS12: TLS12SingleUse,
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A request carrying page-set headers is a condition of its own: a
// CORS-checked variant with them and one without coexist.
func TestValidatePageSetIsACondition(t *testing.T) {
	p := parseValid(t)
	p.Variants = []Variant{
		{
			CORSChecked: true,
			Headers:     p.Headers,
		},
		{
			CORSChecked: true,
			PageSet:     true,
			Headers:     p.Headers,
		},
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCookiesValidate(t *testing.T) {
	var none *Cookies
	if err := none.Validate(); err == nil || !strings.Contains(err.Error(), "cookies: is required") {
		t.Errorf("nil rules: Validate() = %v, want cookies: is required", err)
	}
	if err := DefaultCookies().Validate(); err != nil {
		t.Errorf("DefaultCookies: %v", err)
	}
	bad := DefaultCookies()
	bad.Order = "alphabetical"
	var verr *ValidationError
	if err := bad.Validate(); !errors.As(err, &verr) || len(verr.Problems) != 1 || verr.Problems[0].Path != "cookies.order" {
		t.Errorf("unknown order: Validate() = %v, want one problem at cookies.order", err)
	}
}

// A capture records one request, so it lacks what a complete profile needs;
// Decode reads it anyway, with Parse's strict JSON.
func TestDecodeDoesNotValidate(t *testing.T) {
	valid := string(readValid(t))
	capture := strings.Replace(valid, `"name": "example-chrome-like"`, `"name": ""`, 1)
	p, err := Decode([]byte(capture))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "" || p.Validate() == nil {
		t.Fatalf("decoded name %q, valid %v: want the invalid profile as written", p.Name, p.Validate())
	}
	if _, err := Decode([]byte(strings.Replace(valid, `"platform"`, `"Platform"`, 1))); err == nil {
		t.Fatal("Decode accepted a miscased key")
	}
}
