package profile

import (
	"fmt"
	"slices"
)

// RealECH is what a browser sends to a server whose DNS HTTPS record (RFC
// 9460) carries an ECH config list (RFC 9849): the profile's ClientHello
// as the ClientHelloOuter, naming the config's public name and carrying
// the ClientHelloInner, encrypted to the config, in encrypted_client_hello
// where the GREASE extension goes otherwise. hellogen derives it from the
// ech capture.
//
// Every browser measured takes the first config of the list whose KEM it
// uses (KEMs) and the first of that config's HPKE suites, in the config's
// order; pads the encoded inner hello as RFC 9849, section 6.1.3 says;
// after a rejection with retry configs, connects again with them, once,
// and later connections use the DNS config again; after a rejection
// without, connects again with the GREASE extension and the real
// server_name.
type RealECH struct {
	// Ignore says the browser sends its usual ClientHello, the config
	// unused (Safari 27, which looks the record up). The other fields are
	// then empty.
	Ignore bool `json:"ignore,omitempty"`
	// KEMs are the HPKE KEMs of the configs the browser used, and
	// SkippedKEMs those of configs it passed over for a later one in the
	// list. A config whose KEM is in neither, ahead of a usable one, is
	// ErrNotMeasured.
	KEMs        []Code `json:"kems,omitempty"`
	SkippedKEMs []Code `json:"skippedKems,omitempty"`
	// CipherSuites are the HPKE suites the browser used. A config whose
	// suites list one not in this set ahead of the first that is, or none
	// that is, is ErrNotMeasured.
	CipherSuites []HPKESuite `json:"cipherSuites,omitempty"`
	// Inner is the ClientHelloInner.
	Inner *ECHInner `json:"inner,omitempty"`
	// OuterPSK is what the outer hello carries when the inner one resumes
	// a session (tls.resumption): OuterPSKNone or OuterPSKGREASE. The
	// outer hello is otherwise the resumed one's, without pre_shared_key.
	OuterPSK string `json:"outerPsk,omitempty"`
	// Malformed is what a config list that does not parse leads to:
	// MalformedGREASE or MalformedFail.
	Malformed string `json:"malformed,omitempty"`
	// RetryDrop are the extensions the ClientHello sent again after a
	// rejection leaves out, with retry configs or without (Firefox:
	// compress_certificate).
	RetryDrop []Code `json:"retryDrop,omitempty"`
	// RetryResume says a later connection to the host offers the session
	// a connection made with retry configs established (Chrome), though
	// the server rejects that config again; false: no such session is
	// kept (Firefox).
	RetryResume bool `json:"retryResume,omitempty"`
	// RetryPlain says the next connection to a host whose config the
	// server answered with retry configs carries no real ECH: the usual
	// resumed ClientHello, with the GREASE extension and the real
	// server_name, offering the session the connection with retry
	// configs established (Firefox 159 on Windows and Linux, resolving
	// through its own DoH). RetryResume is then false: no offer of the
	// config follows.
	RetryPlain bool `json:"retryPlain,omitempty"`
	// AltSvcIgnored says the browser takes no HTTP/3 alternative from
	// Alt-Svc for a host whose ECH config it uses: it sends no QUIC
	// packet there and stays on TCP (Chrome, Firefox; the ech-h3
	// capture), though it moves the host to HTTP/3 when the HTTPS record
	// carries no ECH config. False: not measured, as no browser was seen
	// moving such a host to HTTP/3 (hellogen fails on one).
	AltSvcIgnored bool `json:"altSvcIgnored,omitempty"`
}

// ECHInner is a browser's ClientHelloInner as it encodes it
// (EncodedClientHelloInner): its own extensions, and ech_outer_extensions
// naming the rest of the outer hello's, which the server copies from it.
type ECHInner struct {
	// CipherSuites in the order the inner hello offers them; GREASE takes
	// the outer hello's value.
	CipherSuites []Code `json:"cipherSuites"`
	// ExtensionOrder is ExtensionOrderFixed, or ExtensionOrderShuffled
	// when the browser permutes the inner hello's own extensions per
	// connection; ech_outer_extensions stays last either way.
	ExtensionOrder string `json:"extensionOrder"`
	// Extensions are the inner hello's own extensions: encrypted_client_
	// hello (its inner form, ExtEncryptedClientHello without fields),
	// server_name (the real one), supported_versions, and
	// ExtECHOuterExtensions (without fields), whose list is every
	// extension of the outer hello, in its order, but those here and
	// Drop. A resumed inner hello adds pre_shared_key last; GREASE takes
	// the outer hello's values.
	Extensions []Extension `json:"extensions"`
	// Drop are the outer hello's extensions the inner one leaves out
	// (TLS 1.2's).
	Drop []Code `json:"drop"`
}

// ExtECHOuterExtensions is ech_outer_extensions (RFC 9849, section 5.1),
// an inner hello's list of the extensions it takes from the outer one.
const ExtECHOuterExtensions Code = 0xfd00

// Values of RealECH.OuterPSK.
const (
	// OuterPSKNone: the outer hello carries no pre_shared_key (Chrome).
	OuterPSKNone = "none"
	// OuterPSKGREASE: the outer hello carries a pre_shared_key with an
	// identity and binder of the inner one's lengths, random (Firefox; RFC
	// 9849, section 10.12.3).
	OuterPSKGREASE = "grease"
)

// Values of RealECH.Malformed.
const (
	// MalformedGREASE: the usual ClientHello, with the GREASE extension
	// (Firefox).
	MalformedGREASE = "grease"
	// MalformedFail: no connection; the request fails (Chrome).
	MalformedFail = "fail"
)

// echKEMs are the HPKE KEMs the handshake can encrypt to (RFC 9180,
// section 7.1): DHKEM over P-256, P-384, P-521 and X25519.
var echKEMs = []Code{
	0x0010,
	0x0011,
	0x0012,
	0x0020,
}

// innerOwn are the extensions an inner hello must have itself.
var innerOwn = []Code{
	ExtEncryptedClientHello,
	ExtServerName,
	ExtSupportedVersions,
	ExtECHOuterExtensions,
}

// checkRealECH checks t.RealECH against t's ClientHello, its outer hello.
func (c *checker) checkRealECH(t *TLS) {
	r := t.RealECH
	if r == nil {
		return
	}

	const path = "tls.realEch"

	if r.Ignore {
		if len(r.KEMs) > 0 || len(r.SkippedKEMs) > 0 || len(r.CipherSuites) > 0 || r.Inner != nil ||
			r.OuterPSK != "" || r.Malformed != "" || len(r.RetryDrop) > 0 || r.RetryPlain || r.AltSvcIgnored {
			c.add(path, "ignore leaves every other field empty")
		}

		return
	}

	if t.extension(ExtEncryptedClientHello) == nil {
		c.add(path, "needs encrypted_client_hello in the ClientHello, the extension the inner hello goes in")
	}

	if t.extension(ExtServerName) == nil {
		c.add(path, "needs server_name in the ClientHello, which names the config's public name")
	}

	if len(r.KEMs) == 0 {
		c.add(path+".kems", "is required")
	}

	c.checkUnique(path+".kems", r.KEMs)
	c.checkUnique(path+".skippedKems", r.SkippedKEMs)

	for i, k := range r.KEMs {
		if !slices.Contains(echKEMs, k) {
			c.add(fmt.Sprintf("%s.kems[%d]", path, i), "KEM %s is not one the handshake can encrypt to (%v)", k, echKEMs)
		}

		if slices.Contains(r.SkippedKEMs, k) {
			c.add(fmt.Sprintf("%s.kems[%d]", path, i), "KEM %s is also in skippedKems", k)
		}
	}

	if len(r.CipherSuites) == 0 {
		c.add(path+".cipherSuites", "is required")
	}

	for i, s := range r.CipherSuites {
		p := fmt.Sprintf("%s.cipherSuites[%d]", path, i)
		if s.KDF < 1 || s.KDF > hpkeMaxKDF || s.AEAD < 1 || s.AEAD > hpkeMaxAEAD {
			c.add(p, "suite %s/%s is not one the handshake can encrypt with", s.KDF, s.AEAD)
		}

		if i > 0 && r.CipherSuites[i-1].Compare(s) >= 0 {
			c.add(p, "suites must be sorted by KDF, then AEAD, and unique")
		}
	}

	if t.Resumption != nil && r.OuterPSK != OuterPSKNone && r.OuterPSK != OuterPSKGREASE {
		c.add(path+".outerPsk", "must be %q or %q for a profile that resumes", OuterPSKNone, OuterPSKGREASE)
	}

	if t.Resumption == nil && r.OuterPSK != "" {
		c.add(path+".outerPsk", "is for a profile that resumes (tls.resumption)")
	}

	if r.Malformed != MalformedGREASE && r.Malformed != MalformedFail {
		c.add(path+".malformed", "must be %q or %q", MalformedGREASE, MalformedFail)
	}

	c.checkUnique(path+".retryDrop", r.RetryDrop)

	for i, d := range r.RetryDrop {
		if t.extension(d) == nil {
			c.add(fmt.Sprintf("%s.retryDrop[%d]", path, i), "extension %s is not in the ClientHello", d)
		}
	}

	if r.RetryPlain && r.RetryResume {
		c.add(path+".retryPlain", "leaves retryResume false: no offer of the config follows a connection with retry configs")
	}

	if r.RetryPlain && t.Resumption == nil {
		c.add(path+".retryPlain", "is for a profile that resumes (tls.resumption): the connection offers a session")
	}

	if r.Inner == nil {
		c.add(path+".inner", "is required")

		return
	}

	c.checkECHInner(t, r.Inner)
}

// checkECHInner checks an inner hello against t, its outer one.
func (c *checker) checkECHInner(t *TLS, in *ECHInner) {
	const path = "tls.realEch.inner"

	if len(in.CipherSuites) == 0 {
		c.add(path+".cipherSuites", "is required")
	}

	if in.ExtensionOrder != ExtensionOrderFixed && in.ExtensionOrder != ExtensionOrderShuffled {
		c.add(path+".extensionOrder", "must be %q or %q", ExtensionOrderFixed, ExtensionOrderShuffled)
	}

	for _, own := range innerOwn {
		if n := countType(in.Extensions, own); n != 1 {
			c.add(path+".extensions", "needs extension %s once, not %d times", own, n)
		}
	}

	if n := len(in.Extensions); n > 0 && in.Extensions[n-1].Type != ExtECHOuterExtensions {
		c.add(path+".extensions", "ech_outer_extensions must be last; a resumed inner hello adds pre_shared_key after it")
	}

	for i := range in.Extensions {
		e := &in.Extensions[i]
		p := fmt.Sprintf("%s.extensions[%d]", path, i)

		switch e.Type {
		case ExtEncryptedClientHello, ExtECHOuterExtensions:
			if set := e.setFields(); len(set) > 0 {
				c.add(p, "extension %s has no fields here, not %v", e.Type, set)
			}

		case ExtPreSharedKey:
			c.add(p, "pre_shared_key is added to a resumed inner hello, not listed")
		default:
			c.checkExtension(p, e)
		}

		if e.Type == ExtSupportedVersions && slices.ContainsFunc(e.Versions, func(v Code) bool {
			return v != tls13 && v != GREASE
		}) {
			c.add(p, "an inner hello offers TLS 1.3 alone (RFC 9849, section 7.1), not %v", e.Versions)
		}
	}

	c.checkUnique(path+".drop", in.Drop)

	for i, d := range in.Drop {
		p := fmt.Sprintf("%s.drop[%d]", path, i)
		if t.extension(d) == nil {
			c.add(p, "extension %s is not in the ClientHello", d)
		}

		if slices.ContainsFunc(in.Extensions, func(e Extension) bool {
			return e.Type == d
		}) {
			c.add(p, "extension %s is the inner hello's own", d)
		}
	}
}

// countType counts the extensions of type typ.
func countType(exts []Extension, typ Code) int {
	n := 0

	for _, e := range exts {
		if e.Type == typ {
			n++
		}
	}

	return n
}
