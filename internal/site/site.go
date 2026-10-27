// Package site computes what browsers call a site: a scheme and a
// registrable domain (public suffix list), the unit of SameSite cookies,
// sec-fetch-site and cookie partitions.
//
// Registrable and Of are memoized per host: every request asks for them
// several times (sec-fetch-site, the cookie jar's SameSite context), and
// computing them allocates (the public suffix lookup, the IP parse).
package site

import (
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/publicsuffix"
)

// cacheSize bounds each cache: a full one is emptied and refilled.
const cacheSize = 1024

// cache memoizes a function of a key, bounded by cacheSize. It is safe for
// concurrent use; a hit allocates nothing.
type cache[K comparable] struct {
	m sync.Map
	n atomic.Int64
}

func (c *cache[K]) get(k K, compute func(K) string) string {
	if v, ok := c.m.Load(k); ok {
		s, _ := v.(string)
		return s
	}

	v := compute(k)

	if c.n.Add(1) > cacheSize {
		c.m.Clear()
		c.n.Store(1)
	}

	c.m.Store(k, v)

	return v
}

// schemeHost is Of's key: a URL's scheme and host as written.
type schemeHost struct {
	scheme, host string
}

var (
	registrables cache[string]
	sites        cache[schemeHost]
)

// Registrable returns host's registrable domain (eTLD+1), or the host itself
// for IP addresses and hosts without one.
func Registrable(host string) string {
	return registrables.get(host, registrable)
}

func registrable(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if net.ParseIP(host) != nil {
		return host
	}

	if s, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return s
	}

	return host
}

// Of returns u's schemeful site, "https://example.com".
func Of(u *url.URL) string {
	return sites.get(schemeHost{
		scheme: u.Scheme,
		host:   u.Hostname(),
	}, of)
}

func of(k schemeHost) string {
	return strings.ToLower(k.scheme) + "://" + Registrable(k.host)
}

// Same reports whether a and b are the same schemeful site.
func Same(a, b *url.URL) bool {
	return Of(a) == Of(b)
}

// IsPublicSuffix reports whether domain is a public suffix ("com",
// "co.uk"), where no cookie may be set for a whole domain.
func IsPublicSuffix(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if net.ParseIP(domain) != nil {
		return false
	}

	suffix, _ := publicsuffix.PublicSuffix(domain)

	return suffix == domain
}

// Loopback reports a loopback host (Secure Contexts): localhost, a name
// under .localhost, or a loopback IP address.
func Loopback(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// Trustworthy reports a potentially trustworthy URL (Secure Contexts):
// https or wss, or a loopback host.
func Trustworthy(u *url.URL) bool {
	return u.Scheme == "https" || u.Scheme == "wss" || Loopback(u.Hostname())
}
