package mimicry

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/internal/site"
	"github.com/roachadam/mimicry/internal/weburl"
	"github.com/roachadam/mimicry/profile"
)

// canonicalRequest returns req with its URL as canonicalURL makes it for
// p, req itself when that changes nothing: the caller's request is never
// changed. A Host of the caller's own, not the URL's, stays as it is.
func canonicalRequest(req *http.Request, p *profile.Spec) (*http.Request, error) {
	u, err := canonicalURL(req.URL, p)
	if err != nil || u == req.URL {
		return req, err
	}

	c := *req

	c.URL = u
	if req.Host == req.URL.Host {
		// http.NewRequest's copy of the URL's.
		c.Host = u.Host
	}

	return &c, nil
}

// canonicalURL returns u as p's browser's URL parser (the WHATWG URL
// standard, as the urls capture measures it) leaves it, which everything
// after it sees (SNI, the pool, Host and :authority, cookies, Origin,
// Referer): the host lowercase, through UTS #46 ToASCII when it is not
// ASCII (or has an "xn--" label, with profile.URLs.CheckPunycode), an IPv4
// address in any of its forms dotted decimal; no port when it is the
// scheme's default; the path's dot segments resolved and its characters
// percent-encoded (canonicalPath); the query percent-encoded
// (canonicalQuery). A URL with Opaque set keeps its request target, path
// and query, exactly as the caller wrote it (weburl.OpaquePath): only its
// host is made canonical. It returns u itself, nil included, when that
// changes nothing; an error for a host the parser refuses, and
// ErrNotMeasured for a URL where browsers differ when p has no URLs.
func canonicalURL(u *url.URL, p *profile.Spec) (*url.URL, error) {
	if u == nil || u.Host == "" {
		return u, nil
	}

	host, err := canonicalHost(u, p)
	if err != nil {
		return nil, err
	}

	special := defaultPort(u.Scheme) != 0

	query := u.RawQuery
	if u.Opaque == "" {
		query = canonicalQuery(query, special)
	}

	raw := ""
	if u.Opaque == "" && (u.RawPath != "" || u.Path != "") {
		raw = weburl.Path(u)
	}

	path := raw
	if special && raw != "" {
		if path, err = canonicalPath(raw, p); err != nil {
			return nil, err
		}
	}

	if host == u.Host && query == u.RawQuery && path == raw {
		return u, nil
	}

	c := *u

	c.Host, c.RawQuery = host, query
	if path != raw {
		c.RawPath = path
		if c.Path, err = url.PathUnescape(path); err != nil {
			// A lone % kept as written: no decoded form.
			c.Path = path
		}
	}

	return &c, nil
}

// canonicalHost returns u's host and port as p's browser's URL parser
// serializes them. An IPv6 literal is kept as written.
func canonicalHost(u *url.URL, p *profile.Spec) (string, error) {
	name, port := u.Hostname(), u.Port()

	if strings.Contains(name, ":") {
		name = "[" + name + "]"
	} else {
		ascii, err := site.ASCII(name)
		if err != nil {
			return "", fmt.Errorf("mimicry: invalid host %q: %w", name, err)
		}

		// A non-ASCII host's labels are checked by ToASCII; an ASCII
		// one's "xn--" labels only by browsers that check them. A valid
		// label goes out as written everywhere.
		if ascii == strings.ToLower(name) && hasPunycodeLabel(ascii) {
			if err := site.CheckLabels(ascii); err != nil {
				switch {
				case p.URLs == nil:
					return "", fmt.Errorf("%w: profile %s has no URL rules for a host with an invalid \"xn--\" label (%s)", ErrNotMeasured, p.Name, name)
				case p.URLs.CheckPunycode:
					return "", fmt.Errorf("mimicry: invalid host %q: %w", name, err)
				}
			}
		}

		name = ascii
		if endsInNumber(name) {
			if name, err = ipv4(name); err != nil {
				return "", fmt.Errorf("mimicry: invalid host %q: %w", u.Hostname(), err)
			}
		}
	}

	if port == "" {
		return name, nil
	}

	// url.Parse lets only digits through.
	n, err := strconv.Atoi(port)
	if err != nil || n > 65535 {
		return "", fmt.Errorf("mimicry: invalid port %q", port)
	}

	if n == defaultPort(u.Scheme) {
		return name, nil
	}

	return name + ":" + strconv.Itoa(n), nil
}

// hasPunycodeLabel reports whether host, lowercase, has a label starting
// "xn--".
func hasPunycodeLabel(host string) bool {
	return strings.HasPrefix(host, "xn--") || strings.Contains(host, ".xn--")
}

// endsInNumber is the URL standard's ends-in-a-number checker: whether
// host, an ASCII domain, is to be parsed as an IPv4 address, its last
// label (a trailing empty one dropped) a number.
func endsInNumber(host string) bool {
	host = strings.TrimSuffix(host, ".")

	last := host[strings.LastIndexByte(host, '.')+1:]

	if last == "" {
		return false
	}

	if strings.Trim(last, "0123456789") == "" {
		return true
	}

	_, ok := ipv4Number(last)

	return ok
}

// ipv4 is the URL standard's IPv4 parser: host's labels (one to four, a
// trailing empty one dropped) as decimal, octal (leading 0) or hex (0x)
// numbers, the last filling the bytes the others leave ("127.1" is
// 127.0.0.1), serialized dotted decimal. A browser refuses a URL whose
// host fails it.
func ipv4(host string) (string, error) {
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(parts) > 4 {
		return "", errors.New("more than four parts in an IPv4 address")
	}

	var addr uint64

	for i, part := range parts {
		n, ok := ipv4Number(part)
		if !ok {
			return "", fmt.Errorf("IPv4 part %q is not a number", part)
		}

		if i < len(parts)-1 {
			if n > 255 {
				return "", fmt.Errorf("IPv4 part %q is over 255", part)
			}

			addr |= n << (8 * (3 - i))

			continue
		}

		if n >= 1<<(8*(5-len(parts))) {
			return "", fmt.Errorf("IPv4 part %q is too large", part)
		}

		addr |= n
	}

	return fmt.Sprintf("%d.%d.%d.%d", addr>>24, addr>>16&0xff, addr>>8&0xff, addr&0xff), nil
}

// ipv4Number is the URL standard's IPv4 number parser: s decimal, octal
// after a leading 0, or hex after 0x; ok is false for anything else or a
// value past 32 bits.
func ipv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}

	base := 10

	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, base = s[2:], 16
	case len(s) >= 2 && s[0] == '0':
		s, base = s[1:], 8
	}

	if s == "" {
		return 0, true
	}

	n, err := strconv.ParseUint(s, base, 64)
	if err != nil || n > 1<<32-1 {
		return 0, false
	}

	return n, true
}

// defaultPort is the WHATWG URL standard's default port of a special
// scheme mimicry sends to; 0 for any other.
func defaultPort(scheme string) int {
	switch scheme {
	case "http", "ws":
		return 80
	case "https", "wss":
		return 443
	}

	return 0
}

// canonicalQuery percent-encodes the bytes of query in the URL standard's
// query percent-encode set, with "'" too for a special scheme (every
// browser measured sends %27), keeping any %XX already there.
func canonicalQuery(query string, special bool) string {
	return weburl.PercentEncode(query, func(c byte) bool {
		return weburl.InQuerySet(c) || special && c == '\''
	})
}

// canonicalPath returns raw, a special-scheme URL's path as written, as
// p's browser's path parsing leaves it: '\' a separator as '/' is, each
// segment's bytes in the path set (weburl.InPathSet and
// profile.URLs.PathEncodes) percent-encoded, any %XX already there and a
// lone % kept, "." and ".." segments (as %2e too) resolved. Without
// p.URLs, a '|' is ErrNotMeasured: browsers differ on it.
func canonicalPath(raw string, p *profile.Spec) (string, error) {
	var extra string

	switch {
	case p.URLs != nil:
		extra = p.URLs.PathEncodes
	case strings.IndexByte(raw, '|') >= 0:
		return "", fmt.Errorf("%w: profile %s has no URL rules for a '|' in a path", ErrNotMeasured, p.Name)
	}

	in := func(c byte) bool {
		return weburl.InPathSet(c) || strings.IndexByte(extra, c) >= 0
	}

	// The path's first separator starts it; each segment ends at the
	// next one, the last at the end.
	if raw[0] == '/' || raw[0] == '\\' {
		raw = raw[1:]
	}

	var out []string

	for i := 0; ; {
		end := strings.IndexAny(raw[i:], `/\`)
		last := end < 0

		seg := raw[i:]
		if !last {
			seg = raw[i : i+end]
		}

		switch {
		case isDoubleDot(seg):
			if len(out) > 0 {
				out = out[:len(out)-1]
			}

			if last {
				out = append(out, "")
			}

		case isSingleDot(seg):
			if last {
				out = append(out, "")
			}

		default:
			out = append(out, weburl.PercentEncode(seg, in))
		}

		if last {
			break
		}

		i += end + 1
	}

	return "/" + strings.Join(out, "/"), nil
}

// isSingleDot is the URL standard's single-dot segment: "." or "%2e".
func isSingleDot(s string) bool {
	return s == "." || strings.EqualFold(s, "%2e")
}

// isDoubleDot is the URL standard's double-dot segment: "..", or either
// dot as %2e.
func isDoubleDot(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}

	return false
}
