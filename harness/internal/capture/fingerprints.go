package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/profile"
)

// Fingerprints are the hashes anti-bot systems commonly key on, computed from a
// profile. They summarise a capture; the profile itself is what gets compared.
type Fingerprints struct {
	// JA4 follows FoxIO's specification (github.com/FoxIO-LLC/ja4). It sorts
	// ciphers and extensions, so it is stable across extension shuffles.
	JA4 string
	// Akamai is the HTTP/2 fingerprint from Akamai's 2017 Black Hat paper:
	// SETTINGS|WINDOW_UPDATE|PRIORITY|pseudo-header order. Empty without HTTP/2.
	Akamai string
}

// legacyVersion is the ClientHello legacy_version every TLS 1.2+ client
// sends, for captures made before profiles recorded it.
const legacyVersion = 0x0303

// Compute returns the fingerprints of p.
func Compute(p *profile.Profile) Fingerprints {
	f := Fingerprints{
		JA4: ja4(&p.TLS),
	}
	if p.HTTP2 != nil {
		f.Akamai = akamai(p.HTTP2)
	}

	return f
}

func extension(t *profile.TLS, typ profile.Code) *profile.Extension {
	for i := range t.Extensions {
		if t.Extensions[i].Type == typ {
			return &t.Extensions[i]
		}
	}

	return nil
}

// withoutGREASE returns codes in order with GREASE dropped.
func withoutGREASE(codes []profile.Code) []profile.Code {
	out := make([]profile.Code, 0, len(codes))
	for _, c := range codes {
		if c != profile.GREASE {
			out = append(out, c)
		}
	}

	return out
}

func extensionTypes(t *profile.TLS) []profile.Code {
	types := make([]profile.Code, 0, len(t.Extensions))
	for _, e := range t.Extensions {
		types = append(types, e.Type)
	}

	return types
}

// ja4 builds the TCP JA4: its prefix and the truncated SHA-256 of the sorted
// cipher and extension lists.
func ja4(t *profile.TLS) string {
	ciphers := withoutGREASE(t.CipherSuites)
	exts := withoutGREASE(extensionTypes(t))

	version := int(t.LegacyVersion)
	if version == 0 {
		version = legacyVersion
	}
	if e := extension(t, profile.ExtSupportedVersions); e != nil {
		for _, v := range withoutGREASE(e.Versions) {
			version = max(version, int(v))
		}
	}
	sni := "i"
	if slices.Contains(exts, profile.ExtServerName) {
		sni = "d"
	}
	alpn := "00"
	if e := extension(t, profile.ExtALPN); e != nil && len(e.ALPN) > 0 {
		alpn = alpnChars(e.ALPN[0])
	}
	a := fmt.Sprintf("t%s%s%02d%02d%s", tlsVersion(version), sni, min(len(ciphers), 99), min(len(exts), 99), alpn)

	sortedCiphers := hexList(ciphers)
	slices.Sort(sortedCiphers)
	b := strings.Join(sortedCiphers, ",")

	var rest []profile.Code
	for _, x := range exts {
		if x != profile.ExtServerName && x != profile.ExtALPN {
			rest = append(rest, x)
		}
	}
	sortedExts := hexList(rest)
	slices.Sort(sortedExts)
	c := strings.Join(sortedExts, ",")
	if e := extension(t, profile.ExtSignatureAlgorithms); e != nil {
		if algs := withoutGREASE(e.SignatureAlgorithms); len(algs) > 0 {
			c += "_" + strings.Join(hexList(algs), ",")
		}
	}

	return a + "_" + truncatedHash(b, len(ciphers) == 0) + "_" + truncatedHash(c, len(rest) == 0)
}

func tlsVersion(v int) string {
	switch v {
	case 0x0304:
		return "13"
	case 0x0303:
		return "12"
	case 0x0302:
		return "11"
	case 0x0301:
		return "10"
	case 0x0300:
		return "s3"
	default:
		return "00"
	}
}

// alpnChars is the first and last character of the first ALPN value, or of
// its hex form when either is not an ASCII letter or digit.
func alpnChars(v string) string {
	if v == "" {
		return "00"
	}
	first, last := v[0], v[len(v)-1]
	if isAlnum(first) && isAlnum(last) {
		return string([]byte{
			first,
			last,
		})
	}
	h := hex.EncodeToString([]byte(v))

	return string([]byte{
		h[0],
		h[len(h)-1],
	})
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func hexList(codes []profile.Code) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = fmt.Sprintf("%04x", uint16(c))
	}

	return out
}

func truncatedHash(s string, empty bool) string {
	if empty {
		return "000000000000"
	}
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])[:12]
}

// akamai builds SETTINGS|WINDOW_UPDATE|PRIORITY|pseudo-header order. Priority
// weights are the real weight (wire value plus one), as in the paper.
func akamai(h *profile.HTTP2) string {
	settings := make([]string, len(h.Settings))
	for i, s := range h.Settings {
		settings[i] = fmt.Sprintf("%d:%d", s.ID, s.Value)
	}
	window := "00"
	if h.WindowUpdate != 0 {
		window = strconv.FormatUint(uint64(h.WindowUpdate), 10)
	}
	priorities := "0"
	if len(h.Priorities) > 0 {
		parts := make([]string, len(h.Priorities))
		for i, p := range h.Priorities {
			exclusive := 0
			if p.Exclusive {
				exclusive = 1
			}
			parts[i] = fmt.Sprintf("%d:%d:%d:%d", p.StreamID, exclusive, p.DependsOn, int(p.Weight)+1)
		}
		priorities = strings.Join(parts, ",")
	}
	pseudo := make([]string, len(h.PseudoHeaderOrder))
	for i, name := range h.PseudoHeaderOrder {
		pseudo[i] = name[1:2]
	}

	return strings.Join([]string{
		strings.Join(settings, ";"),
		window,
		priorities,
		strings.Join(pseudo, ","),
	}, "|")
}
