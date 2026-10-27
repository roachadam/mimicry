package cors

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/roachadam/mimicry/internal/headerorder"
)

// SafelistedMethod reports a CORS-safelisted method: one a request may use
// without a preflight.
func SafelistedMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodPost
}

// unsafeHeaders returns h's CORS-unsafe header names (Fetch standard),
// lowercase, sorted and unique. Forbidden headers and User-Agent (which
// Chrome still forbids) are never among them. The 1024-byte safelist
// limit is unreachable here.
func unsafeHeaders(h http.Header) []string {
	var unsafe []string
	for name, values := range h {
		if len(values) == 0 {
			continue // a suppressed profile header
		}

		lower := strings.ToLower(name)
		if forbiddenHeader(lower) || lower == strings.ToLower(headerorder.Key) {
			continue
		}

		if !safelistedHeader(lower, strings.Join(values, ", ")) {
			unsafe = append(unsafe, lower)
		}
	}

	slices.Sort(unsafe)

	return slices.Compact(unsafe)
}

// forbiddenHeader reports a forbidden request-header name (Fetch
// standard), plus user-agent: headers only the browser sets.
func forbiddenHeader(lower string) bool {
	switch lower {
	case "accept-charset", "accept-encoding", "access-control-request-headers",
		"access-control-request-method", "connection", "content-length", "cookie",
		"cookie2", "date", "dnt", "expect", "host", "keep-alive", "origin", "referer",
		"set-cookie", "te", "trailer", "transfer-encoding", "upgrade", "via", "user-agent":
		return true
	}

	return strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "sec-")
}

// safelistedHeader reports a CORS-safelisted request-header (Fetch
// standard): a name and value a page may send without a preflight.
func safelistedHeader(lower, value string) bool {
	if len(value) > 128 {
		return false
	}

	switch lower {
	case "accept":
		return !strings.ContainsFunc(value, corsUnsafeByte)
	case "accept-language", "content-language":
		return !strings.ContainsFunc(value, func(c rune) bool {
			return !languageByte(c)
		})

	case "content-type":
		if strings.ContainsFunc(value, corsUnsafeByte) {
			return false
		}

		switch mimeEssence(value) {
		case "application/x-www-form-urlencoded", "multipart/form-data", "text/plain":
			return true
		}

		return false

	case "range":
		return simpleRange(value)
	}

	return false
}

// corsUnsafeByte reports a CORS-unsafe request-header byte.
func corsUnsafeByte(c rune) bool {
	return c < 0x20 && c != '\t' || c == 0x7f || strings.ContainsRune("\"():<>?@[\\]{}", c)
}

// mimeEssence returns a MIME type's essence (type/subtype, lowercase), as
// the Fetch standard's "parse a MIME type" does, or "" when it fails.
// Parameters cannot make it fail: the standard skips invalid ones.
func mimeEssence(value string) string {
	value = strings.Trim(value, " \t\r\n")

	typ, rest, ok := strings.Cut(value, "/")

	if !ok {
		return ""
	}

	sub, _, _ := strings.Cut(rest, ";")

	sub = strings.TrimRight(sub, " \t\r\n")

	if !token(typ) || !token(sub) {
		return ""
	}

	return strings.ToLower(typ + "/" + sub)
}

// token reports a non-empty string of HTTP token code points.
func token(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(c rune) bool {
		return (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && !strings.ContainsRune("!#$%&'*+-.^_`|~", c)
	})
}

// languageByte reports a byte the Fetch standard allows in a safelisted
// Accept-Language or Content-Language value.
func languageByte(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || strings.ContainsRune(" *,-.;=", c)
}

// simpleRange reports a Range value of the form bytes=N- or bytes=N-M
// (N <= M): the only ones the Fetch standard safelists.
func simpleRange(value string) bool {
	spec, ok := strings.CutPrefix(value, "bytes=")
	if !ok {
		return false
	}

	start, end, ok := strings.Cut(spec, "-")
	if !ok {
		return false
	}

	// ParseUint takes digits only: no sign, no space, not empty.
	a, err := strconv.ParseUint(start, 10, 64)
	if err != nil {
		return false
	}

	if end == "" {
		return true
	}

	b, err := strconv.ParseUint(end, 10, 64)

	return err == nil && a <= b
}
