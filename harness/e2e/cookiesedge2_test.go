package e2e

import (
	"testing"

	"github.com/roachadam/mimicry/harness/internal/server"
)

// The cookies-edge2 page, walked through mimicry as the browser walked it
// (its navigations and same-origin fetches; the http:// ones on the
// scenario's plain port, which refuses TLS, so every browser, Safari after
// its HTTPS upgrade failed, sent them over plain HTTP), sends every step
// the Cookie header each of the browser's samples sent: SameSite across
// https:// and http:// of one host (samesite_scheme), nameless "=x"
// cookies and the prefix rule for their values (empty_name), and __Http-
// and __Host-Http- without Secure (http_prefix).
func TestCookiesEdge2MatchesBrowser(t *testing.T) {
	matchCookiesEdgePage(t, server.CookiesEdge2, server.CookiesEdge2Steps())
}
