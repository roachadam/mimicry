// Package weburl holds what mimicry shares about URLs as browsers send
// them (the WHATWG URL standard, as the urls capture measures it): the
// percent-encode sets every browser measured applies, and a URL's path
// serialized as mimicry's canonical form leaves it, where Go's own
// (url.URL.EscapedPath) would encode characters a browser sends as they
// are ('|' for Firefox and Safari).
package weburl

import (
	"net/url"
	"strings"
)

// InQuerySet reports whether c is in the URL standard's query
// percent-encode set: C0 controls, space, '"', '#', '<', '>' and every byte
// above '~'.
func InQuerySet(c byte) bool {
	return c <= ' ' || c == '"' || c == '#' || c == '<' || c == '>' || c > '~'
}

// InPathSet reports whether c is in the path percent-encode set as every
// browser measured applies it: the query set, '?', '^', '`', '{' and '}'.
// A browser may encode more (profile.URLs.PathEncodes: '|' for Chrome).
func InPathSet(c byte) bool {
	return InQuerySet(c) || c == '?' || c == '^' || c == '`' || c == '{' || c == '}'
}

// PercentEncode percent-encodes the bytes of s that in reports, keeping
// every other byte (a % included) as it is.
func PercentEncode(s string, in func(byte) bool) string {
	n := 0
	for i := range len(s) {
		if in(s[i]) {
			n++
		}
	}

	if n == 0 {
		return s
	}

	const hex = "0123456789ABCDEF"

	var b strings.Builder

	b.Grow(len(s) + 2*n)

	for i := range len(s) {
		c := s[i]
		if !in(c) {
			b.WriteByte(c)

			continue
		}

		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}

	return b.String()
}

// Path is u's path as sent: with Opaque, the caller's own (OpaquePath);
// else RawPath when set (mimicry's canonical form sets it to what the
// browser sends), else Go's EscapedPath; "/" for none.
func Path(u *url.URL) string {
	if u.Opaque != "" {
		return OpaquePath(u.Opaque)
	}

	p := u.RawPath
	if p == "" {
		p = u.EscapedPath()
	}

	if p == "" {
		return "/"
	}

	return p
}

// OpaquePath is the path an http(s) URL's Opaque names, as net/http and
// x/net/http2 read it: "/raw/path" itself, "//host/raw/path" without its
// authority ("/" for none).
func OpaquePath(opaque string) string {
	rest, ok := strings.CutPrefix(opaque, "//")
	if !ok {
		return opaque
	}

	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[i:]
	}

	return "/"
}

// RequestURI is u's request target in origin form: Path, then the query.
func RequestURI(u *url.URL) string {
	if u.ForceQuery || u.RawQuery != "" {
		return Path(u) + "?" + u.RawQuery
	}

	return Path(u)
}

// String is u.String() with its path as Path has it.
func String(u *url.URL) string {
	if u.RawPath == "" && u.Opaque == "" {
		return u.String()
	}

	c := *u

	c.Opaque, c.Path, c.RawPath, c.RawQuery, c.ForceQuery, c.Fragment, c.RawFragment = "", "", "", "", false, "", ""

	s := c.String() + Path(u)
	if u.ForceQuery || u.RawQuery != "" {
		s += "?" + u.RawQuery
	}

	if u.Fragment != "" {
		s += "#" + u.EscapedFragment()
	}

	return s
}
