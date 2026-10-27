package e2e

import (
	"testing"

	"github.com/roachadam/mimicry/internal/harness/internal/server"
)

// Every step of the cookies-edge2 page through mimicry sends each sample's
// Cookie header: SameSite across schemes of one host, nameless cookies and
// __Http- prefixes. Its http:// steps go to a plain-only port, as the
// browsers sent them.
func TestCookiesEdge2MatchesBrowser(t *testing.T) {
	matchCookiesEdgePage(t, server.CookiesEdge2, server.CookiesEdge2Steps())
}
