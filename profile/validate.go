package profile

import (
	"fmt"
	"maps"
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

// Validate checks p and returns a *ValidationError listing every problem, or
// nil. It rejects only what is malformed or forbidden by the protocols, not
// what is merely unusual: real browsers do odd things, such as Safari listing
// a signature algorithm twice, and a profile must be able to reproduce them.
func (p *Profile) Validate() error {
	c := &checker{}
	if p.Name == "" {
		c.add("name", "is required")
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
	c.checkKinds(p.Kinds)
	if p.Cookies != nil {
		c.checkCookies(p.Cookies)
	}
	if len(c.problems) > 0 {
		return &ValidationError{
			Problems: c.problems,
		}
	}
	return nil
}

func (t *TLS) extension(typ Code) *Extension {
	for i := range t.Extensions {
		if t.Extensions[i].Type == typ {
			return &t.Extensions[i]
		}
	}
	return nil
}

func (c *checker) checkTLS(t *TLS) {
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
		c.checkExtension(path, e)
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
		for i, d := range r.Drop {
			if t.extension(d) == nil {
				c.add(fmt.Sprintf("tls.resumption.drop[%d]", i), "extension %s is not in the ClientHello", d)
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

func (c *checker) checkExtension(path string, e *Extension) {
	set := e.setFields()
	switch {
	case generatedExtensions[e.Type]:
		if len(set) > 0 {
			c.add(path, "extension %s is generated per connection; remove %s", e.Type, strings.Join(set, ", "))
		}
	case typedField[e.Type] != "":
		want := typedField[e.Type]
		if !slices.Contains(set, want) {
			c.add(path, "extension %s needs %s", e.Type, want)
		}
		for _, f := range set {
			if f != want {
				c.add(path, "extension %s takes %s, not %s", e.Type, want, f)
			}
		}
	default:
		for _, f := range set {
			if f != "data" {
				c.add(path, "extension %s takes data, not %s", e.Type, f)
			}
		}
	}
	// RFC 8446, section 4.2.8: no two key shares for the same group.
	c.checkUnique(path+".key_shares", e.KeyShares)
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

func (c *checker) checkHTTP2(h *HTTP2) {
	if len(h.Settings) == 0 {
		c.add("http2.settings", "is required")
	}
	ids := map[uint16]bool{}
	for i, s := range h.Settings {
		path := fmt.Sprintf("http2.settings[%d]", i)
		if s.ID == 0 {
			c.add(path, "id 0 is not a setting")
		}
		if ids[s.ID] {
			c.add(path, "repeats setting %d", s.ID)
		}
		ids[s.ID] = true
	}
	if h.WindowUpdate > 1<<31-1 {
		c.add("http2.window_update", "exceeds the maximum increment 2^31-1")
	}
	for i, p := range h.Priorities {
		path := fmt.Sprintf("http2.priorities[%d]", i)
		if p.StreamID == 0 {
			c.add(path, "stream_id must not be 0")
		}
		if p.DependsOn == p.StreamID {
			c.add(path, "a stream cannot depend on itself")
		}
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
	if h.FirstStreamID != 0 && (h.FirstStreamID%2 == 0 || h.FirstStreamID >= 1<<31) {
		c.add("http2.first_stream_id", "must be an odd stream ID below 2^31")
	}
	for _, v := range []struct {
		path  string
		value uint32
	}{
		{"http2.stream_window_update", h.StreamWindowUpdate},
		{"http2.stream_update_threshold", h.StreamUpdateThreshold},
		{"http2.connection_update_threshold", h.ConnectionUpdateThreshold},
	} {
		if v.value > 1<<31-1 {
			c.add(v.path, "exceeds the maximum window 2^31-1")
		}
	}
	if h.MaxDataFrame > 1<<24-1 {
		c.add("http2.max_data_frame", "exceeds the largest frame 2^24-1")
	}
	if h.HPACK != nil {
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
		c.checkHeaderName(path, n)
		if slices.Contains(p.NeverIndexed, n) {
			c.add(path, "%s is also in never_indexed", n)
		}
	}
	for i, n := range p.NeverIndexed {
		c.checkHeaderName(fmt.Sprintf("http2.hpack.never_indexed[%d]", i), n)
	}
	for _, n := range slices.Sorted(maps.Keys(p.NeverIndexBelow)) {
		path := "http2.hpack.never_index_below[" + n + "]"
		c.checkHeaderName(path, n)
		if p.NeverIndexBelow[n] <= 0 {
			c.add(path, "must be a positive length")
		}
	}
	if p.SizeUpdates != SizeUpdatesOnChange && p.SizeUpdates != SizeUpdatesAlways {
		c.add("http2.hpack.size_updates", "must be %q or %q", SizeUpdatesOnChange, SizeUpdatesAlways)
	}
}

func (c *checker) checkHeaderName(path, name string) {
	if name == "" || name != strings.ToLower(name) {
		c.add(path, "name %q must be lowercase and not empty", name)
	}
}

// checkHeaderList checks an HTTP/2 header list: lowercase names, each once,
// no pseudo-headers; others allows the OtherHeaders marker.
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
		case h.Name != strings.ToLower(h.Name):
			c.add(at, "name %q must be lowercase", h.Name)
		}
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
		case hd.Name == "" || strings.ContainsAny(hd.Name, " :\r\n"):
			c.add(at, "name %q is not a header name", hd.Name)
		case seen[lower]:
			c.add(at, "repeats header %s", hd.Name)
		}
		seen[lower] = true
	}
	if !seen["host"] {
		c.add(path, "must place Host")
	}
}
