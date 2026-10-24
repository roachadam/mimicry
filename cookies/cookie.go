// Package cookies is a cookie jar that keeps and sends cookies as a given
// browser does: SameSite, third-party and partitioned cookies, redirects,
// order, and byte-for-byte values, by the rules in a profile's cookies
// section (profile.Cookies) on top of RFC 6265bis.
//
// mimicry's transport drives the jar itself, because the rules need what an
// http.CookieJar never sees: the page a request comes from, whether it is a
// top-level navigation, its redirect chain, its credentials mode. Use
// Jar.CookieJar for code that needs an http.CookieJar; it cannot apply those
// rules and loses values net/http alters.
//
// Imports: the standard library, clockwork, profile and internal/site.
package cookies

import (
	"strings"
	"time"
)

// SameSite is a cookie's SameSite attribute.
type SameSite int

// SameSite values. Unset behaves as the profile says
// (profile.Cookies.UnsetSameSite).
const (
	SameSiteUnset SameSite = iota
	SameSiteLax
	SameSiteStrict
	SameSiteNone
)

func (s SameSite) String() string {
	switch s {
	case SameSiteLax:
		return "Lax"
	case SameSiteStrict:
		return "Strict"
	case SameSiteNone:
		return "None"
	}
	return ""
}

// MarshalText writes the attribute value ("Lax"; empty when unset).
func (s SameSite) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText reads an attribute value, case-blind; anything else is
// unset, as in a Set-Cookie line.
func (s *SameSite) UnmarshalText(b []byte) error {
	switch strings.ToLower(string(b)) {
	case "lax":
		*s = SameSiteLax
	case "strict":
		*s = SameSiteStrict
	case "none":
		*s = SameSiteNone
	default:
		*s = SameSiteUnset
	}
	return nil
}

// Cookie is a stored cookie.
type Cookie struct {
	// Name and Value as stored: exactly as received, except where the
	// browser changes them (Safari's Latin-1 re-encoding). Name is empty
	// for a pair without "=" in browsers that keep it as a value.
	Name  string `json:"name"`
	Value string `json:"value"`
	// Domain is the host for a host-only cookie, else the Domain
	// attribute without its leading dot.
	Domain   string `json:"domain"`
	HostOnly bool   `json:"host_only,omitempty"`
	Path     string `json:"path"`
	// Expires is when the cookie expires; zero for a session cookie.
	Expires  time.Time `json:"expires,omitzero"`
	Secure   bool      `json:"secure,omitempty"`
	HTTPOnly bool      `json:"http_only,omitempty"`
	SameSite SameSite  `json:"same_site,omitempty"`
	// Partitioned is the attribute; Partition is the top-level site the
	// cookie is kept under ("https://example.com"), set for Partitioned
	// cookies and, in Firefox, for every cookie of a cross-site context.
	// Empty: the unpartitioned jar.
	Partitioned bool      `json:"partitioned,omitempty"`
	Partition   string    `json:"partition,omitempty"`
	Created     time.Time `json:"created"`
	LastAccess  time.Time `json:"last_access,omitzero"`
	// Raw is the Set-Cookie line that set it; empty for one set with
	// Jar.Set.
	Raw string `json:"raw,omitempty"`
}

// sameKey reports whether a and b are the same cookie: a later one
// replaces an earlier one with the same key.
func sameKey(a, b *Cookie) bool {
	return a.Name == b.Name && a.Domain == b.Domain && a.HostOnly == b.HostOnly && a.Path == b.Path && a.Partition == b.Partition
}

// pair is the cookie as sent: "name=value", or the value alone when the
// name is empty.
func (c *Cookie) pair() string {
	if c.Name == "" {
		return c.Value
	}
	return c.Name + "=" + c.Value
}
