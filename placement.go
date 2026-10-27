package mimicry

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/headerorder"
)

// Place moves one header directly after or before another, after the
// profile has ordered the request's headers: a deliberate deviation for a
// server that expects a header elsewhere. Set exactly one of After and
// Before; names match in any case.
//
// Placements apply in order to the lines the connection writes: over
// HTTP/1.1 including Host, Connection and framing headers (not
// Proxy-Authorization); over HTTP/2 and HTTP/3 only the fields after the
// pseudo-headers. A header set more than once moves as one run. If the
// request does not send both the header and its anchor, RoundTrip returns
// an error wrapping ErrPlacement without sending it.
type Place struct {
	// Header is the header to move.
	Header string
	// After puts Header directly after this header.
	After string
	// Before puts Header directly before this header.
	Before string
}

// ErrPlacement reports a header placement that cannot be applied: an
// invalid Place, a header or anchor the request does not send, or
// Request.HeaderPlacement on a request with its own order (Exact,
// HeaderOrderKey). Test for it with errors.Is.
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

// placementsFor returns Config.HeaderPlacement then the request's own. A
// request that sets its own order takes none, and is an error if it has
// placements of its own.
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
