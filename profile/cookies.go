package profile

import "slices"

// Cookies are a browser's cookie rules where browsers differ; the rules
// they share (RFC 6265bis) are the jar's own. See docs/decisions/0012.
type Cookies struct {
	// UnsetSameSite is how a cookie without SameSite behaves: SameSiteLax
	// (Chrome) or SameSiteNone (Firefox, Safari).
	UnsetSameSite string `json:"unsetSamesite"`
	// LaxAllowsUnsafeSeconds lets an unset-SameSite cookie younger than
	// this go on a cross-site top-level navigation with an unsafe method
	// (a form POST): Chrome's exception, 120. Zero: none.
	LaxAllowsUnsafeSeconds int `json:"laxAllowsUnsafeSeconds,omitempty"`
	// NoneRequiresSecure rejects SameSite=None without Secure (Chrome,
	// Firefox).
	NoneRequiresSecure bool `json:"noneRequiresSecure,omitempty"`
	// ThirdParty is the policy for cookies of a cross-site context: a
	// request whose site is not its top-level page's.
	ThirdParty string `json:"thirdParty"`
	// WebSocketUnpartitioned also sends a cross-site WebSocket handshake its
	// host's unpartitioned Lax and None cookies, whatever ThirdParty says
	// (Safari 26, not 27). Strict ones stay behind (not measured).
	WebSocketUnpartitioned bool `json:"websocketUnpartitioned,omitempty"`
	// Redirects is how a redirect chain affects which cookies go.
	Redirects string `json:"redirects"`
	// Nameless is what a Set-Cookie pair without "=" sets.
	Nameless string `json:"nameless"`
	// EmptyName is what a pair with nothing before its "=" ("=x") sets.
	EmptyName string `json:"emptyName"`
	// Latin1Values re-encodes non-ASCII value bytes as if each were a
	// Latin-1 character (Safari: "café" in UTF-8 becomes "cafÃ©").
	Latin1Values bool `json:"latin1Values,omitempty"`
	// Order is the order of cookies in the Cookie header.
	Order string `json:"order"`
	// NewValueRenews gives a cookie replaced with a different value a new
	// creation time, moving it later in the order (Chrome); otherwise it
	// keeps the old one's. Only with OrderRFC.
	NewValueRenews bool `json:"newValueRenews,omitempty"`
	// PartitionedFirst sends the Partitioned cookies of a request before
	// its unpartitioned ones, each group in Order (Safari). Off, they
	// take their place in Order with the rest.
	PartitionedFirst bool `json:"partitionedFirst,omitempty"`
	// SameSiteScheme is whether a site, for SameSite, includes the scheme:
	// SameSiteSchemeful makes http:// and https:// of one host cross-site
	// (Chrome; RFC 6265bis), SameSiteSchemeless does not (Firefox,
	// Safari).
	SameSiteScheme string `json:"samesiteScheme"`
	// HostPrefixPath is what the __Host- prefix's Path=/ rule checks.
	HostPrefixPath string `json:"hostPrefixPath"`
	// HTTPPrefix enforces the __Http- and __Host-Http- prefixes: Secure
	// and HttpOnly, and __Host-'s rules for the latter (Chrome, Firefox).
	// Off, they are ordinary names, __Host-Http- still a __Host- name
	// (Safari).
	HTTPPrefix bool `json:"httpPrefix,omitempty"`
	// SecureLoopback treats plain http:// to a loopback host (localhost,
	// *.localhost, 127.0.0.0/8, ::1) as secure for cookies (not Safari 26).
	SecureLoopback bool `json:"secureLoopback,omitempty"`
	// MaxPerSite is how many cookies a registrable domain keeps per
	// partition: past it, the least recently used are evicted down to
	// EvictTo (Chrome, Firefox: 180 and 150). Zero: no limit.
	MaxPerSite int `json:"maxPerSite,omitempty"`
	EvictTo    int `json:"evictTo,omitempty"`
}

// Cookie policy values.
const (
	SameSiteLax  = "lax"
	SameSiteNone = "none"

	// ThirdPartySameSite sends unpartitioned cookies cross-site by their
	// SameSite (None only) and keys Partitioned cookies by top-level site
	// (Chrome).
	ThirdPartySameSite = "samesite"
	// ThirdPartyPartitionAll keeps every cookie of a cross-site context in
	// a jar for its top-level site, Partitioned or not (Firefox's Total
	// Cookie Protection).
	ThirdPartyPartitionAll = "partition_all"
	// ThirdPartyPartitionedOnly allows only Partitioned cookies in a
	// cross-site context (Safari).
	ThirdPartyPartitionedOnly = "partitioned_only"

	// RedirectsIgnore judges SameSite by the request URL alone (Chrome).
	RedirectsIgnore = "ignore"
	// RedirectsChain treats a request as cross-site when any URL of its
	// redirect chain is (Firefox).
	RedirectsChain = "chain"
	// RedirectsBlockAfterCrossSite sends no cookies on a request reached
	// through a cross-site hop (Safari).
	RedirectsBlockAfterCrossSite = "block_after_cross_site"

	// NamelessValue makes "knoeq" a cookie with an empty name and value
	// "knoeq", sent as "knoeq" (Chrome; RFC 6265bis).
	NamelessValue = "value"
	// NamelessName makes it a cookie named "knoeq" with an empty value,
	// sent as "knoeq=" (Firefox).
	NamelessName = "name"
	// NamelessDrop ignores it (Safari).
	NamelessDrop = "drop"

	// EmptyNameKeep makes "=x" a cookie with an empty name and value "x",
	// sent as "x" (Chrome; RFC 6265bis). The value must not start with a
	// cookie prefix, in any case and after whitespace, nor hold "=".
	EmptyNameKeep = "keep"
	// EmptyNameDrop ignores it (Firefox, Safari).
	EmptyNameDrop = "drop"

	// OrderRFC sends longer paths first, then older cookies first (RFC
	// 6265; Chrome, Firefox).
	OrderRFC = "rfc"
	// OrderDomainName sends longer paths first, then by stored domain
	// (Domain cookies, stored with a leading dot, first), then by name
	// (Safari).
	OrderDomainName = "domain_name"

	// SameSiteScheme values.
	SameSiteSchemeful  = "schemeful"
	SameSiteSchemeless = "schemeless"

	// HostPrefixPathAttribute requires a Path=/ attribute as sent: a
	// __Host- cookie without Path is refused (RFC 6265bis; Chrome,
	// Firefox).
	HostPrefixPathAttribute = "attribute"
	// HostPrefixPathStored requires the stored path to be "/": a __Host-
	// cookie without Path whose default path is "/" is kept (Safari).
	HostPrefixPathStored = "stored"
)

// DefaultCookies are RFC 6265bis's rules, for a jar used without a profile
// (a profile must state its browser's).
func DefaultCookies() *Cookies {
	return &Cookies{
		UnsetSameSite:      SameSiteLax,
		NoneRequiresSecure: true,
		ThirdParty:         ThirdPartySameSite,
		Redirects:          RedirectsIgnore,
		Nameless:           NamelessValue,
		EmptyName:          EmptyNameKeep,
		Order:              OrderRFC,
		SameSiteScheme:     SameSiteSchemeful,
		HostPrefixPath:     HostPrefixPathAttribute,
		HTTPPrefix:         true,
		SecureLoopback:     true,
	}
}

// Validate checks the rules as Profile.Validate checks a profile's cookies
// section, and returns a *ValidationError listing every problem, or nil. Nil
// rules are an error.
func (k *Cookies) Validate() error {
	c := &checker{}
	c.checkCookies(k)

	return c.err()
}

// checkCookies requires k: a profile without cookie rules has not measured
// its browser's (decision 0008).
func (c *checker) checkCookies(k *Cookies) {
	if k == nil {
		c.add("cookies", "is required")

		return
	}

	oneOf := func(field, v string, allowed ...string) {
		if slices.Contains(allowed, v) {
			return
		}

		c.add("cookies."+field, "must be one of %q", allowed)
	}
	oneOf("unsetSamesite", k.UnsetSameSite, SameSiteLax, SameSiteNone)
	oneOf("thirdParty", k.ThirdParty, ThirdPartySameSite, ThirdPartyPartitionAll, ThirdPartyPartitionedOnly)
	oneOf("redirects", k.Redirects, RedirectsIgnore, RedirectsChain, RedirectsBlockAfterCrossSite)
	oneOf("nameless", k.Nameless, NamelessValue, NamelessName, NamelessDrop)
	oneOf("emptyName", k.EmptyName, EmptyNameKeep, EmptyNameDrop)
	oneOf("order", k.Order, OrderRFC, OrderDomainName)
	oneOf("samesiteScheme", k.SameSiteScheme, SameSiteSchemeful, SameSiteSchemeless)
	oneOf("hostPrefixPath", k.HostPrefixPath, HostPrefixPathAttribute, HostPrefixPathStored)

	if k.LaxAllowsUnsafeSeconds < 0 {
		c.add("cookies.laxAllowsUnsafeSeconds", "must not be negative")
	}

	if k.LaxAllowsUnsafeSeconds > 0 && k.UnsetSameSite != SameSiteLax {
		c.add("cookies.laxAllowsUnsafeSeconds", "applies only when unsetSamesite is %q", SameSiteLax)
	}

	if k.NewValueRenews && k.Order != OrderRFC {
		c.add("cookies.newValueRenews", "applies only when order is %q", OrderRFC)
	}

	switch {
	case k.MaxPerSite < 0:
		c.add("cookies.maxPerSite", "must not be negative")
	case k.MaxPerSite == 0 && k.EvictTo != 0:
		c.add("cookies.evictTo", "applies only with maxPerSite")
	case k.MaxPerSite > 0 && (k.EvictTo <= 0 || k.EvictTo >= k.MaxPerSite):
		c.add("cookies.evictTo", "must be between 1 and maxPerSite - 1")
	}
}
