package profile

// Cookies are a browser's cookie rules where browsers differ; the rules
// they share (RFC 6265bis) are the jar's own. See docs/decisions/0012.
type Cookies struct {
	// UnsetSameSite is how a cookie without SameSite behaves: SameSiteLax
	// (Chrome) or SameSiteNone (Firefox, Safari).
	UnsetSameSite string `json:"unset_samesite"`
	// LaxAllowsUnsafeSeconds lets an unset-SameSite cookie younger than
	// this go on a cross-site top-level navigation with an unsafe method
	// (a form POST): Chrome's exception, 120. Zero: none.
	LaxAllowsUnsafeSeconds int `json:"lax_allows_unsafe_seconds,omitempty"`
	// NoneRequiresSecure rejects SameSite=None without Secure (Chrome,
	// Firefox).
	NoneRequiresSecure bool `json:"none_requires_secure,omitempty"`
	// ThirdParty is the policy for cookies of a cross-site context: a
	// request whose site is not its top-level page's.
	ThirdParty string `json:"third_party"`
	// Redirects is how a redirect chain affects which cookies go.
	Redirects string `json:"redirects"`
	// Nameless is what a Set-Cookie pair without "=" sets.
	Nameless string `json:"nameless"`
	// Latin1Values re-encodes non-ASCII value bytes as if each were a
	// Latin-1 character (Safari: "café" in UTF-8 becomes "cafÃ©").
	Latin1Values bool `json:"latin1_values,omitempty"`
	// Order is the order of cookies in the Cookie header.
	Order string `json:"order"`
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

	// OrderRFC sends longer paths first, then older cookies first (RFC
	// 6265; Chrome, Firefox).
	OrderRFC = "rfc"
	// OrderDomainName sends longer paths first, then by stored domain
	// (Domain cookies, stored with a leading dot, first), then by name
	// (Safari).
	OrderDomainName = "domain_name"
)

// DefaultCookies are RFC 6265bis's rules, for a jar used without a profile
// (a profile must state its browser's): Lax by default, None needs Secure,
// SameSite judged by the request URL, Partitioned cookies keyed by top-level
// site, a nameless pair is a value, RFC order.
func DefaultCookies() *Cookies {
	return &Cookies{
		UnsetSameSite:      SameSiteLax,
		NoneRequiresSecure: true,
		ThirdParty:         ThirdPartySameSite,
		Redirects:          RedirectsIgnore,
		Nameless:           NamelessValue,
		Order:              OrderRFC,
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
		for _, a := range allowed {
			if v == a {
				return
			}
		}
		c.add("cookies."+field, "must be one of %q", allowed)
	}
	oneOf("unset_samesite", k.UnsetSameSite, SameSiteLax, SameSiteNone)
	oneOf("third_party", k.ThirdParty, ThirdPartySameSite, ThirdPartyPartitionAll, ThirdPartyPartitionedOnly)
	oneOf("redirects", k.Redirects, RedirectsIgnore, RedirectsChain, RedirectsBlockAfterCrossSite)
	oneOf("nameless", k.Nameless, NamelessValue, NamelessName, NamelessDrop)
	oneOf("order", k.Order, OrderRFC, OrderDomainName)
	if k.LaxAllowsUnsafeSeconds < 0 {
		c.add("cookies.lax_allows_unsafe_seconds", "must not be negative")
	}
	if k.LaxAllowsUnsafeSeconds > 0 && k.UnsetSameSite != SameSiteLax {
		c.add("cookies.lax_allows_unsafe_seconds", "applies only when unset_samesite is %q", SameSiteLax)
	}
}
