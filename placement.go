package mimicry

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/headerorder"
)

// Place moves one header to directly after or before another, once the
// profile has put the request's headers in its browser's order (page-set
// headers included): a deliberate deviation from the browser, for a server
// that expects a header where the browser would not put it. Set exactly one
// of After and Before. Names match in any case.
//
// Placements apply, in order, to the header lines the connection writes:
// over HTTP/1.1 Host, Connection, Content-Length and Transfer-Encoding
// too (not Proxy-Authorization, which goes where the browser puts it for
// the proxy); over HTTP/2 and HTTP/3 the fields after the pseudo-headers,
// whose order stays the profile's. A header that moves is sent with all
// its values; one set more than once moves as one run, and an anchor set
// more than once is the last of its run for After, the first for Before.
//
// Both the header and its anchor must be sent on the request, after the
// profile's defaults, suppressions, Config.Headers and cookies: otherwise
// RoundTrip returns an error wrapping ErrPlacement and the request is not
// sent (its CORS preflight may have been). A connection-level header
// HTTP/2 and HTTP/3 never send (Connection, Host) cannot be placed there.
type Place struct {
	// Header is the header to move.
	Header string
	// After puts Header directly after this header.
	After string
	// Before puts Header directly before this header.
	Before string
}

// ErrPlacement reports a header placement (Config.HeaderPlacement,
// Request.HeaderPlacement) that cannot be applied: a Place that is not
// valid (NewTransport, RoundTrip), a header or anchor the request does not
// send (RoundTrip; nothing of the request is sent), or a request's
// placements on a request that sets its own order (Exact, HeaderOrderKey).
// Test for it with errors.Is.
var ErrPlacement = headerorder.ErrPlacement

// placements checks ps, named field in errors, and returns them for the
// connections, lowercase.
func placements(field string, ps []Place) ([]headerorder.Placement, error) {
	out := make([]headerorder.Placement, 0, len(ps))
	for i, p := range ps {
		anchor, after := p.After, true
		if p.Before != "" {
			anchor, after = p.Before, false
		}
		var reason string
		switch {
		case (p.After == "") == (p.Before == ""):
			reason = "set exactly one of After and Before"
		case strings.HasPrefix(p.Header, ":") || strings.HasPrefix(anchor, ":"):
			reason = "pseudo-headers are in the profile's order and cannot be placed or anchored"
		case !httpguts.ValidHeaderFieldName(p.Header) || !httpguts.ValidHeaderFieldName(anchor):
			reason = "not a header name"
		case strings.EqualFold(p.Header, anchor):
			reason = "a header cannot be its own anchor"
		}
		if reason != "" {
			return nil, fmt.Errorf("%w: %s[%d] (Header %q, After %q, Before %q): %s", ErrPlacement, field, i, p.Header, p.After, p.Before, reason)
		}
		out = append(out, headerorder.Placement{
			Header: strings.ToLower(p.Header),
			Anchor: strings.ToLower(anchor),
			After:  after,
		})
	}
	return out, nil
}

// placementsFor returns the placements req, made as r describes (or
// Exact), is sent with: Config.HeaderPlacement, then the request's own. A
// request that sets its own order (Exact, HeaderOrderKey, on the request
// or in Config.Headers) takes none, and with placements of its own is an
// error.
func (t *Transport) placementsFor(req *http.Request, r Request, exact bool) ([]headerorder.Placement, error) {
	if exact || setsOrder(req.Header) || setsOrder(t.headers) {
		if len(r.HeaderPlacement) > 0 {
			return nil, fmt.Errorf("%w: Request.HeaderPlacement on a request that sets its own order (Exact or HeaderOrderKey)", ErrPlacement)
		}
		return nil, nil
	}
	if len(r.HeaderPlacement) == 0 {
		return t.placements, nil
	}
	own, err := placements("Request.HeaderPlacement", r.HeaderPlacement)
	if err != nil {
		return nil, err
	}
	return append(slices.Clip(t.placements), own...), nil
}

// setsOrder reports whether h sets HeaderOrderKey.
func setsOrder(h http.Header) bool {
	for k, v := range h {
		if len(v) > 0 && strings.EqualFold(k, HeaderOrderKey) {
			return true
		}
	}
	return false
}
