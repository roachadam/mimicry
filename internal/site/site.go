// Package site computes what browsers call a site: a scheme and a
// registrable domain (public suffix list), the unit of SameSite cookies,
// sec-fetch-site and cookie partitions.
package site

import (
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Registrable returns host's registrable domain (eTLD+1), or the host itself
// for IP addresses and hosts without one.
func Registrable(host string) string {
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
	return strings.ToLower(u.Scheme) + "://" + Registrable(u.Hostname())
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
