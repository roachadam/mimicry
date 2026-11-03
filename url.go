package mimicry

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/internal/site"
)

// canonicalRequest returns req with its URL as canonicalURL makes it, req
// itself when that changes nothing: the caller's request is never
// changed. A Host of the caller's own, not the URL's, stays as it is.
func canonicalRequest(req *http.Request) (*http.Request, error) {
	u, err := canonicalURL(req.URL)
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

// canonicalURL returns u as a browser's URL parser (the WHATWG URL
// standard) leaves it, which everything after it sees (SNI, the pool,
// Host and :authority, cookies, Origin, Referer): the host lowercase,
// through UTS #46 ToASCII when it is not ASCII; no port when it is the
// scheme's default; the query percent-encoded (canonicalQuery). It returns
// u itself, nil included, when that changes nothing, and an error for a
// host ToASCII refuses.
func canonicalURL(u *url.URL) (*url.URL, error) {
	if u == nil || u.Host == "" {
		return u, nil
	}

	host, err := canonicalHost(u)
	if err != nil {
		return nil, err
	}

	query := canonicalQuery(u.RawQuery)
	if host == u.Host && query == u.RawQuery {
		return u, nil
	}

	c := *u

	c.Host, c.RawQuery = host, query

	return &c, nil
}

// canonicalHost returns u's host and port as the WHATWG URL parser
// serializes them. An IPv6 literal is kept as written.
func canonicalHost(u *url.URL) (string, error) {
	name, port := u.Hostname(), u.Port()

	if strings.Contains(name, ":") {
		name = "[" + name + "]"
	} else {
		ascii, err := site.ASCII(name)
		if err != nil {
			return "", fmt.Errorf("mimicry: invalid host %q: %w", name, err)
		}

		name = ascii
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

// canonicalQuery percent-encodes the bytes of query in the WHATWG query
// percent-encode set (C0 controls, space, '"', '#', '<', '>' and every
// byte above '~'), keeping any %XX already there. The special-query set's
// "'" is left as written: no capture shows what each browser sends for it
// (profiles/EVIDENCE.md, Not covered).
func canonicalQuery(query string) string {
	n := 0
	for i := range len(query) {
		if inQuerySet(query[i]) {
			n++
		}
	}

	if n == 0 {
		return query
	}

	const hex = "0123456789ABCDEF"

	var b strings.Builder

	b.Grow(len(query) + 2*n)

	for i := range len(query) {
		c := query[i]
		if !inQuerySet(c) {
			b.WriteByte(c)

			continue
		}

		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}

	return b.String()
}

// inQuerySet reports whether c is in the WHATWG query percent-encode set.
func inQuerySet(c byte) bool {
	return c <= ' ' || c == '"' || c == '#' || c == '<' || c == '>' || c > '~'
}
