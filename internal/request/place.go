package request

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/internal/headerorder"
)

// Place moves one header directly after or before another: the root's
// Place, which converts to it.
type Place struct {
	Header string
	After  string
	Before string
}

// Placements checks ps, named field in errors, and returns them for the
// connections, lowercase.
func Placements(field string, ps []Place) ([]headerorder.Placement, error) {
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
			return nil, fmt.Errorf("%w: %s[%d] (Header %q, After %q, Before %q): %s", headerorder.ErrPlacement, field, i, p.Header, p.After, p.Before, reason)
		}

		out = append(out, headerorder.Placement{
			Header: strings.ToLower(p.Header),
			Anchor: strings.ToLower(anchor),
			After:  after,
		})
	}

	return out, nil
}

// SetsOrder reports whether h sets HeaderOrderKey (headerorder.Key).
func SetsOrder(h http.Header) bool {
	for k, v := range h {
		if len(v) > 0 && strings.EqualFold(k, headerorder.Key) {
			return true
		}
	}

	return false
}
