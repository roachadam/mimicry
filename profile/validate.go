package profile

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Problem is one validation failure. Path locates the value, for example
// "tls.extensions[3].key_shares".
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string {
	return p.Path + ": " + p.Message
}

// ValidationError lists every problem found in a profile.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	return "profile: invalid:\n  " + strings.Join(lines, "\n  ")
}

type checker struct {
	problems []Problem
}

func (c *checker) add(path, format string, args ...any) {
	c.problems = append(c.problems, Problem{
		Path:    path,
		Message: fmt.Sprintf(format, args...),
	})
}

// err returns the problems found as a *ValidationError, or nil.
func (c *checker) err() error {
	if len(c.problems) == 0 {
		return nil
	}
	return &ValidationError{
		Problems: c.problems,
	}
}

// profileName is the form of Profile.Name, which also names the built-in
// profile's file and accessor.
var profileName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate checks p and returns a *ValidationError listing every problem, or
// nil. It rejects what is malformed, forbidden by the protocols, beyond what
// mimicry can send, or not measured (decision 0008), not what is merely
// unusual: real browsers do odd things, such as Safari listing a signature
// algorithm twice, and a profile must be able to reproduce them.
func (p *Profile) Validate() error {
	c := &checker{}
	switch {
	case p.Name == "":
		c.add("name", "is required")
	case !profileName.MatchString(p.Name):
		c.add("name", "%q must be lowercase letters and digits in parts joined by dashes, as chrome-155-macos", p.Name)
	}
	if p.Browser == "" {
		c.add("browser", "is required")
	}
	if p.Version == "" {
		c.add("version", "is required")
	}
	if p.Platform == "" {
		c.add("platform", "is required")
	}
	c.checkTLS(&p.TLS)
	alpn := p.TLS.extension(ExtALPN)
	if p.HTTP2 != nil {
		c.checkHTTP2(p.HTTP2)
	} else if alpn != nil && slices.Contains(alpn.ALPN, "h2") {
		c.add("http2", "is required because ALPN offers h2")
	}
	c.checkHeaderList("headers", p.Headers, false)
	if p.HTTP1 != nil {
		c.checkHTTP1Headers("http1.headers", p.HTTP1.Headers, false)
	}
	c.checkVariants("variants", p.Variants, false)
	c.checkKinds(p)
	if ctx := p.Context; ctx != nil && ctx.PreflightCache != "" && ctx.PreflightCache != PreflightCacheReplace && ctx.PreflightCache != PreflightCacheMerge {
		c.add("context.preflight_cache", "must be empty, %q or %q", PreflightCacheReplace, PreflightCacheMerge)
	}
	c.checkCookies(p.Cookies)
	if p.Proxy != nil {
		c.checkProxy(p.Proxy, p.Headers)
	}
	c.checkRedirects(p.Redirects)
	c.checkConnections(p.Connections)
	c.checkWebSocketHello(&p.TLS)
	c.checkWebSocket(p)
	return c.err()
}

func (t *TLS) extension(typ Code) *Extension {
	for i := range t.Extensions {
		if t.Extensions[i].Type == typ {
			return &t.Extensions[i]
		}
	}
	return nil
}

// tls13 is TLS 1.3's supported_versions value; tls12 is the legacy_version
// a TLS 1.3 ClientHello carries.
const (
	tls13 Code = 0x0304
	tls12 Code = 0x0303
	// recordVersion is the record-layer version uTLS sends the first
	// ClientHello in.
	recordVersion Code = 0x0301
)

// sessionIDLength is the length of the session ID uTLS sends.
const sessionIDLength = 32

// keyLengths are the key share lengths of the groups uTLS generates keys
// for (RFC 8446, section 4.2.8.2; draft-ietf-tls-ecdhe-mlkem).
var keyLengths = map[Code]uint16{
	0x0017: 65,   // secp256r1, uncompressed
	0x0018: 97,   // secp384r1
	0x0019: 133,  // secp521r1
	0x001d: 32,   // x25519
	0x0202: 1568, // MLKEM1024
	0x11ec: 1216, // X25519MLKEM768
	0x6399: 1216, // X25519Kyber768Draft00
}

// HPKE identifiers uTLS can name in a GREASE ECH extension (RFC 9180,
// section 7), and the tag length of every one of those AEADs.
const (
	hpkeMaxKDF  = 0x0003 // HKDF-SHA256, -SHA384, -SHA512
	hpkeMaxAEAD = 0x0003 // AES-128-GCM, AES-256-GCM, ChaCha20Poly1305
	hpkeTagLen  = 16
	// echEncLength is the length of the X25519 key uTLS encapsulates.
	echEncLength = 32
)

// greaseBodies are the bodies uTLS gives the first and second GREASE
// extensions, as BoringSSL does; it fills in no more than two.
var greaseBodies = []Bytes{
	{},
	{
		0,
	},
}

// fixedBodies are the bodies of the data extensions uTLS writes itself
// rather than send the profile's bytes.
var fixedBodies = map[Code]Bytes{
	// OCSP, with no responder IDs or extensions.
	ExtStatusRequest: {
		1, 0, 0, 0, 0,
	},
	ExtSignedCertificateTimestamp: {},
	ExtExtendedMasterSecret:       {},
	// An empty renegotiated_connection: an initial handshake.
	ExtRenegotiationInfo: {
		0,
	},
}

// keptOnResumption are extensions a TLS 1.3 resumed ClientHello cannot
// leave out: the PSK needs its modes (RFC 8446, section 4.2.9), and TLS 1.3
// needs supported_versions and, for (EC)DHE modes, key_share.
var keptOnResumption = []Code{
	ExtSupportedVersions,
	ExtPSKKeyExchangeModes,
	ExtKeyShare,
}

func (c *checker) checkTLS(t *TLS) {
	c.checkHelloRecord(t)
	// uTLS caps legacy_version at TLS 1.2 and always sends a 32-byte
	// session ID; both are what a TLS 1.3 ClientHello carries.
	if t.LegacyVersion != tls12 {
		c.add("tls.legacy_version", "must be %s, the one the handshake sends, not %s", tls12, t.LegacyVersion)
	}
	if t.SessionIDLength != sessionIDLength {
		c.add("tls.session_id_length", "must be %d, the length the handshake sends, not %d", sessionIDLength, t.SessionIDLength)
	}
	if len(t.CipherSuites) == 0 {
		c.add("tls.cipher_suites", "is required")
	}
	if len(t.CompressionMethods) == 0 {
		c.add("tls.compression_methods", "is required")
	}
	if t.ExtensionOrder != ExtensionOrderFixed && t.ExtensionOrder != ExtensionOrderShuffled {
		c.add("tls.extension_order", "must be %q or %q", ExtensionOrderFixed, ExtensionOrderShuffled)
	}
	if len(t.Extensions) == 0 {
		c.add("tls.extensions", "is required")
	}
	seen := map[Code]int{}
	grease := 0
	for i := range t.Extensions {
		e := &t.Extensions[i]
		path := fmt.Sprintf("tls.extensions[%d]", i)
		if first, dup := seen[e.Type]; dup && e.Type != GREASE {
			c.add(path, "duplicates extension %s at tls.extensions[%d]", e.Type, first)
		} else if !dup {
			seen[e.Type] = i
		}
		if e.Type == ExtPreSharedKey && i != len(t.Extensions)-1 {
			c.add(path, "pre_shared_key must be the last extension (RFC 8446, section 4.2.11)")
		}
		if e.Type == GREASE {
			if grease < len(greaseBodies) && !bytes.Equal(e.Data, greaseBodies[grease]) {
				c.add(path, "GREASE extension %d's body must be %s, the one uTLS sends, not %s", grease+1, describe(greaseBodies[grease]), describe(e.Data))
			}
			grease++
			if grease == len(greaseBodies)+1 {
				c.add(path, "more than %d GREASE extensions are not supported", len(greaseBodies))
			}
		}
		c.checkExtension(path, e)
	}
	if sv := t.extension(ExtSupportedVersions); sv == nil || !slices.Contains(sv.Versions, tls13) {
		c.add("tls.extensions", "must offer TLS 1.3 (%s) in supported_versions: TLS 1.2-only profiles are not supported", tls13)
	}
	keyShare := t.extension(ExtKeyShare)
	groups := t.extension(ExtSupportedGroups)
	if keyShare != nil {
		for _, g := range keyShare.KeyShares {
			if groups == nil || !slices.Contains(groups.Groups, g) {
				c.add("tls.extensions.key_share", "group %s is not in supported_groups", g)
			}
		}
	}
	if r := t.Resumption; r != nil {
		if t.extension(ExtPreSharedKey) != nil {
			c.add("tls.extensions", "the full ClientHello cannot offer pre_shared_key; tls.resumption adds it")
		}
		if t.extension(ExtPSKKeyExchangeModes) == nil {
			c.add("tls.resumption", "needs psk_key_exchange_modes in the ClientHello (RFC 8446, section 4.2.9)")
		}
		c.checkUnique("tls.resumption.drop", r.Drop)
		for i, d := range r.Drop {
			path := fmt.Sprintf("tls.resumption.drop[%d]", i)
			if slices.Contains(keptOnResumption, d) {
				c.add(path, "a TLS 1.3 resumption cannot leave out extension %s", d)
			}
			if t.extension(d) == nil {
				c.add(path, "extension %s is not in the ClientHello", d)
			}
		}
		if r.TLS12 != "" && r.TLS12 != TLS12Reuse && r.TLS12 != TLS12SingleUse {
			c.add("tls.resumption.tls12", "must be empty, %q or %q", TLS12Reuse, TLS12SingleUse)
		}
		if r.TLS12 != "" && t.extension(ExtSessionTicket) == nil {
			c.add("tls.resumption.tls12", "needs session_ticket in the ClientHello")
		}
	}
}

// describe writes an extension body for a message.
func describe(b Bytes) string {
	if len(b) == 0 {
		return "empty"
	}
	return fmt.Sprintf("%x", []byte(b))
}

func (c *checker) checkExtension(path string, e *Extension) {
	set := e.setFields()
	switch {
	case generatedExtensions[e.Type]:
		if len(set) > 0 {
			c.add(path, "extension %s is generated per connection; remove %s", e.Type, strings.Join(set, ", "))
		}
	case typedFields[e.Type] != nil:
		want := typedFields[e.Type]
		for _, f := range want {
			if !slices.Contains(set, f) {
				c.add(path, "extension %s needs %s", e.Type, f)
			}
		}
		for _, f := range set {
			if !slices.Contains(want, f) {
				c.add(path, "extension %s takes %s, not %s", e.Type, strings.Join(want, " and "), f)
			}
		}
	default:
		for _, f := range set {
			if f != "data" {
				c.add(path, "extension %s takes data, not %s", e.Type, f)
			}
		}
		if want, fixed := fixedBodies[e.Type]; fixed && !bytes.Equal(e.Data, want) {
			c.add(path, "extension %s's body must be %s, the one uTLS sends, not %s", e.Type, describe(want), describe(e.Data))
		}
	}
	// RFC 8446, section 4.2.8: no two key shares for the same group.
	c.checkUnique(path+".key_shares", e.KeyShares)
	if e.Type == ExtKeyShare && len(e.KeyShareLengths) > 0 {
		c.checkKeyShareLengths(path, e)
	}
	if e.ECH != nil {
		c.checkECH(path+".ech", e.ECH)
	}
}

// checkKeyShareLengths checks that every key share has a length the
// handshake can send: its group's, or at least a byte for GREASE.
func (c *checker) checkKeyShareLengths(path string, e *Extension) {
	if len(e.KeyShareLengths) != len(e.KeyShares) {
		c.add(path+".key_share_lengths", "has %d lengths for %d key shares", len(e.KeyShareLengths), len(e.KeyShares))
		return
	}
	for i, g := range e.KeyShares {
		n := e.KeyShareLengths[i]
		p := fmt.Sprintf("%s.key_share_lengths[%d]", path, i)
		want, known := keyLengths[g]
		switch {
		case g == GREASE:
			if n == 0 {
				c.add(p, "a GREASE key share needs at least one byte")
			}
		case !known:
			c.add(p, "the handshake cannot generate a key for group %s", g)
		case n != want:
			c.add(p, "group %s's key is %d bytes, not %d", g, want, n)
		}
	}
}

// checkECH checks a GREASE ECH shape against what uTLS can send.
func (c *checker) checkECH(path string, ech *ECH) {
	if len(ech.CipherSuites) == 0 {
		c.add(path+".cipher_suites", "is required")
	}
	for i, s := range ech.CipherSuites {
		p := fmt.Sprintf("%s.cipher_suites[%d]", path, i)
		if s.KDF == 0 || s.KDF > hpkeMaxKDF {
			c.add(p, "KDF %s is not an HPKE KDF the handshake can name", s.KDF)
		}
		if s.AEAD == 0 || s.AEAD > hpkeMaxAEAD {
			c.add(p, "AEAD %s is not an HPKE AEAD the handshake can name", s.AEAD)
		}
		if i > 0 && ech.CipherSuites[i-1].Compare(s) >= 0 {
			c.add(p, "cipher suites must be sorted and unique")
		}
	}
	if ech.EncLength != echEncLength {
		c.add(path+".enc_length", "must be %d, the X25519 key the handshake encapsulates, not %d", echEncLength, ech.EncLength)
	}
	if len(ech.PayloadLengths) == 0 {
		c.add(path+".payload_lengths", "is required")
	}
	c.checkPayloadLengths(path+".payload_lengths", ech.PayloadLengths)
}

// checkPayloadLengths checks a set of GREASE ECH payload lengths: each longer
// than the AEAD tag, sorted and unique.
func (c *checker) checkPayloadLengths(path string, lengths []uint16) {
	for i, n := range lengths {
		p := fmt.Sprintf("%s[%d]", path, i)
		if n <= hpkeTagLen {
			c.add(p, "%d is no longer than the %d-byte AEAD tag", n, hpkeTagLen)
		}
		if i > 0 && lengths[i-1] >= n {
			c.add(p, "payload lengths must be sorted and unique")
		}
	}
}

// checkHelloRecord checks the record version the ClientHello travels in
// and the GREASE ECH payload lengths of a resumed ClientHello.
func (c *checker) checkHelloRecord(t *TLS) {
	// uTLS (as Go's crypto/tls) sends the first ClientHello in TLS 1.0
	// records.
	if t.RecordVersion != recordVersion {
		c.add("tls.record_version", "must be %s, the one the handshake sends, not %s", recordVersion, t.RecordVersion)
	}
	r := t.Resumption
	if r == nil {
		return
	}
	ech := t.extension(ExtEncryptedClientHello)
	if ech != nil && len(r.ECHPayloadLengths) == 0 {
		c.add("tls.resumption.ech_payload_lengths", "is required with encrypted_client_hello")
	}
	if ech == nil && len(r.ECHPayloadLengths) > 0 {
		c.add("tls.resumption.ech_payload_lengths", "needs encrypted_client_hello in the ClientHello")
	}
	c.checkPayloadLengths("tls.resumption.ech_payload_lengths", r.ECHPayloadLengths)
}

// checkUnique reports repeated values.
func (c *checker) checkUnique(path string, codes []Code) {
	seen := map[Code]bool{}
	for _, v := range codes {
		if seen[v] {
			c.add(path, "repeats %s", v)
		}
		seen[v] = true
	}
}

// HTTP/2 settings with value ranges (RFC 9113, section 6.5.2).
const (
	settingEnablePush        = 0x2
	settingInitialWindowSize = 0x4
	settingMaxFrameSize      = 0x5
)

// HTTP/2 limits (RFC 9113): the largest flow-control window and stream ID,
// the window every connection starts with, and the frame size range.
const (
	maxWindow       = 1<<31 - 1
	maxStreamID     = 1<<31 - 1
	initialWindow   = 65535
	minMaxFrameSize = 1 << 14
	maxMaxFrameSize = 1<<24 - 1
)

func (c *checker) checkHTTP2(h *HTTP2) {
	if len(h.Settings) == 0 {
		c.add("http2.settings", "is required")
	}
	ids := map[uint16]bool{}
	streamWindow := uint64(initialWindow)
	for i, s := range h.Settings {
		path := fmt.Sprintf("http2.settings[%d]", i)
		if s.ID == 0 {
			c.add(path, "id 0 is not a setting")
		}
		if ids[s.ID] {
			c.add(path, "repeats setting %d", s.ID)
		}
		ids[s.ID] = true
		switch s.ID {
		case settingEnablePush:
			if s.Value != 0 {
				c.add(path, "SETTINGS_ENABLE_PUSH must be 0: server push is not implemented")
			}
		case settingInitialWindowSize:
			if s.Value > maxWindow {
				c.add(path, "SETTINGS_INITIAL_WINDOW_SIZE exceeds the maximum window 2^31-1 (RFC 9113, section 6.5.2)")
			}
			streamWindow = uint64(s.Value)
		case settingMaxFrameSize:
			if s.Value < minMaxFrameSize || s.Value > maxMaxFrameSize {
				c.add(path, "SETTINGS_MAX_FRAME_SIZE must be from 2^14 to 2^24-1 (RFC 9113, section 6.5.2)")
			}
		}
	}
	if initialWindow+uint64(h.WindowUpdate) > maxWindow {
		c.add("http2.window_update", "with the initial 65535 exceeds the maximum window 2^31-1 (RFC 9113, section 6.9.1)")
	}
	if streamWindow+uint64(h.StreamWindowUpdate) > maxWindow {
		c.add("http2.stream_window_update", "with SETTINGS_INITIAL_WINDOW_SIZE exceeds the maximum window 2^31-1 (RFC 9113, section 6.9.1)")
	}
	for i, p := range h.Priorities {
		path := fmt.Sprintf("http2.priorities[%d]", i)
		if p.StreamID == 0 {
			c.add(path, "stream_id must not be 0")
		}
		if p.StreamID > maxStreamID {
			c.add(path, "stream_id must be below 2^31")
		}
		if p.DependsOn > maxStreamID {
			c.add(path, "depends_on must be below 2^31")
		}
		if p.DependsOn == p.StreamID {
			c.add(path, "a stream cannot depend on itself")
		}
	}
	if hp := h.HeadersPriority; hp != nil && hp.DependsOn > maxStreamID {
		c.add("http2.headers_priority", "depends_on must be below 2^31")
	}
	order := slices.Clone(h.PseudoHeaderOrder)
	slices.Sort(order)
	want := slices.Clone(pseudoHeaders)
	slices.Sort(want)
	if !slices.Equal(order, want) {
		c.add("http2.pseudo_header_order", "must list each of %s exactly once", strings.Join(pseudoHeaders, ", "))
	}
	if h.ChainPriorities && h.HeadersPriority == nil {
		c.add("http2.chain_priorities", "needs headers_priority")
	}
	if h.FirstStreamID != 0 && (h.FirstStreamID%2 == 0 || h.FirstStreamID > maxStreamID) {
		c.add("http2.first_stream_id", "must be an odd stream ID below 2^31")
	}
	for _, v := range []struct {
		path  string
		value uint32
	}{
		{
			path:  "http2.stream_update_threshold",
			value: h.StreamUpdateThreshold,
		},
		{
			path:  "http2.connection_update_threshold",
			value: h.ConnectionUpdateThreshold,
		},
	} {
		if v.value > maxWindow {
			c.add(v.path, "exceeds the maximum window 2^31-1")
		}
	}
	if h.MaxDataFrame > maxMaxFrameSize {
		c.add("http2.max_data_frame", "exceeds the largest frame 2^24-1")
	}
	if h.HPACK == nil {
		c.add("http2.hpack", "is required: how the browser encodes headers is not measured")
	} else {
		c.checkHPACK(h.HPACK)
	}
}

func (c *checker) checkHPACK(p *HPACK) {
	if p.NameMatch != NameMatchStaticFirst && p.NameMatch != NameMatchHighestIndex {
		c.add("http2.hpack.name_match", "must be %q or %q", NameMatchStaticFirst, NameMatchHighestIndex)
	}
	if p.Huffman != HuffmanShorter && p.Huffman != HuffmanAlways {
		c.add("http2.hpack.huffman", "must be %q or %q", HuffmanShorter, HuffmanAlways)
	}
	for i, n := range p.WithoutIndexing {
		path := fmt.Sprintf("http2.hpack.without_indexing[%d]", i)
		c.checkFieldName(path, n)
		if slices.Contains(p.NeverIndexed, n) {
			c.add(path, "%s is also in never_indexed", n)
		}
	}
	for i, n := range p.NeverIndexed {
		c.checkFieldName(fmt.Sprintf("http2.hpack.never_indexed[%d]", i), n)
	}
	for _, n := range slices.Sorted(maps.Keys(p.NeverIndexBelow)) {
		path := "http2.hpack.never_index_below[" + n + "]"
		c.checkFieldName(path, n)
		if p.NeverIndexBelow[n] <= 0 {
			c.add(path, "must be a positive length")
		}
	}
	if p.SizeUpdates != SizeUpdatesOnChange && p.SizeUpdates != SizeUpdatesAlways {
		c.add("http2.hpack.size_updates", "must be %q or %q", SizeUpdatesOnChange, SizeUpdatesAlways)
	}
}

// checkFieldName checks a name in the HPACK rules: a request pseudo-header or
// a lowercase header name.
func (c *checker) checkFieldName(path, name string) {
	if !slices.Contains(pseudoHeaders, name) && !slices.Contains(extendedPseudoHeaders, name) {
		c.checkHeaderName(path, name)
	}
}

// checkHeaderName checks a lowercase header name outside a header list.
func (c *checker) checkHeaderName(path, name string) {
	if !isToken(name) || name != strings.ToLower(name) {
		c.add(path, "name %q must be a lowercase header name", name)
	}
}

// isToken reports whether s is a token (RFC 9110, section 5.6.2), the form
// of a header name.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		b := s[i]
		alnum := 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
		if !alnum && strings.IndexByte("!#$%&'*+-.^_`|~", b) < 0 {
			return false
		}
	}
	return true
}

// checkHeaderValue rejects what would end or corrupt a header line (RFC
// 9110, section 5.5).
func (c *checker) checkHeaderValue(path, value string) {
	if strings.ContainsAny(value, "\r\n\x00") {
		c.add(path, "value %q must not contain CR, LF or NUL", value)
	}
}

// connectionSpecific are the headers HTTP/2 forbids (RFC 9113, section
// 8.2.2), Host included: :authority carries it.
var connectionSpecific = []string{
	"connection",
	"host",
	"keep-alive",
	"proxy-connection",
	"transfer-encoding",
	"upgrade",
}

// checkHeaderList checks an HTTP/2 header list: lowercase names, each once,
// no pseudo-headers or connection-specific ones; others allows the
// OtherHeaders marker.
func (c *checker) checkHeaderList(path string, headers []Header, others bool) {
	if len(headers) == 0 {
		c.add(path, "is required")
	}
	seen := map[string]bool{}
	for i, h := range headers {
		at := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case h.Name == OtherHeaders && others:
		case h.Name == OtherHeaders:
			c.add(at, "%s belongs only in a kind's lists", OtherHeaders)
		case h.Name == "":
			c.add(at, "name is required")
		case strings.HasPrefix(h.Name, ":"):
			c.add(at, "pseudo-header %s belongs in http2.pseudo_header_order", h.Name)
		case !isToken(h.Name):
			c.add(at, "name %q is not a header name", h.Name)
		case h.Name != strings.ToLower(h.Name):
			c.add(at, "name %q must be lowercase", h.Name)
		case slices.Contains(connectionSpecific, h.Name):
			c.add(at, "%s is connection-specific, which HTTP/2 forbids (RFC 9113, section 8.2.2)", h.Name)
		}
		c.checkHeaderValue(at, h.Value)
		if seen[h.Name] {
			c.add(at, "repeats header %s", h.Name)
		}
		seen[h.Name] = true
	}
}

// checkHTTP1Headers checks an HTTP/1.1 header list: any casing, but each
// name once whatever its case, and a place for Host, which every request
// carries; others allows the OtherHeaders marker.
func (c *checker) checkHTTP1Headers(path string, headers []Header, others bool) {
	seen := map[string]bool{}
	for i, hd := range headers {
		at := fmt.Sprintf("%s[%d]", path, i)
		lower := strings.ToLower(hd.Name)
		switch {
		case hd.Name == OtherHeaders && others:
		case hd.Name == OtherHeaders:
			c.add(at, "%s belongs only in a kind's lists", OtherHeaders)
		case !isToken(hd.Name):
			c.add(at, "name %q is not a header name", hd.Name)
		case seen[lower]:
			c.add(at, "repeats header %s", hd.Name)
		}
		c.checkHeaderValue(at, hd.Value)
		seen[lower] = true
	}
	if !seen["host"] {
		c.add(path, "must place Host")
	}
}

// checkProxy checks the proxy section: CONNECT header lists that place
// Host (and, with credentials, Proxy-Authorization) and take a user agent
// the navigation headers have, credential timing mimicry implements, and a
// SOCKS5 handshake mimicry can send.
func (c *checker) checkProxy(p *Proxy, headers []Header) {
	c.checkHTTP1Headers("proxy.connect", p.Connect, false)
	c.checkProxyUserAgent("proxy.connect", p.Connect, headers)
	if p.ConnectAuth == nil {
		if p.ConnectCredentials != "" || p.ForwardCredentials != "" || p.ForwardAuth != nil {
			c.add("proxy", "credential timing and placement need connect_auth")
		}
	} else {
		c.checkHTTP1Headers("proxy.connect_auth", p.ConnectAuth, false)
		c.checkProxyUserAgent("proxy.connect_auth", p.ConnectAuth, headers)
		if !slices.ContainsFunc(p.ConnectAuth, func(h Header) bool {
			return strings.EqualFold(h.Name, "proxy-authorization")
		}) {
			c.add("proxy.connect_auth", "must place Proxy-Authorization")
		}
		if p.ConnectCredentials != CredentialsAfterChallenge && p.ConnectCredentials != CredentialsOnChallenge {
			c.add("proxy.connect_credentials", "must be %q or %q", CredentialsAfterChallenge, CredentialsOnChallenge)
		}
		if p.ForwardCredentials != CredentialsAfterChallenge && p.ForwardCredentials != CredentialsAlways {
			c.add("proxy.forward_credentials", "must be %q or %q", CredentialsAfterChallenge, CredentialsAlways)
		}
	}
	if p.ForwardConnection != "" && !isToken(p.ForwardConnection) {
		c.add("proxy.forward_connection", "%q is not a header name", p.ForwardConnection)
	}
	if a := p.ForwardAuth; a != nil {
		if len(a.After) > 0 && a.Before != "" {
			c.add("proxy.forward_auth", "takes after or before, not both")
		}
		for i, n := range a.After {
			c.checkHeaderName(fmt.Sprintf("proxy.forward_auth.after[%d]", i), n)
		}
		if a.Before != "" {
			c.checkHeaderName("proxy.forward_auth.before", a.Before)
		}
	}
	if s := p.SOCKS5; s != nil {
		if len(s.Methods) != 1 || s.Methods[0] != 0 {
			c.add("proxy.socks5.methods", "must be [0]: other offers are not implemented")
		}
		if s.Address != SOCKS5Domain {
			c.add("proxy.socks5.address", "must be %q: other address types are not implemented", SOCKS5Domain)
		}
	}
}

// checkProxyUserAgent requires a user agent in the navigation headers when
// a CONNECT list places User-Agent, which takes its value from there.
func (c *checker) checkProxyUserAgent(path string, connect, headers []Header) {
	if !slices.ContainsFunc(connect, func(h Header) bool {
		return strings.EqualFold(h.Name, "user-agent")
	}) {
		return
	}
	if !slices.ContainsFunc(headers, func(h Header) bool {
		return h.Name == "user-agent" && h.Value != ""
	}) {
		c.add(path, "places User-Agent, but headers has no user-agent value to send")
	}
}
