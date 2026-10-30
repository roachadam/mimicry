package mimicry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// Kind is how a page makes a request, named after the HTML or JavaScript
// that makes it. Browsers send each kind with headers, an order and a
// priority of its own; a request's kind picks the profile's description of
// it.
type Kind int

// Request kinds. Navigate, the default, is a user's top-level navigation
// (typed URL, link, submit button) with the profile's Sec-Fetch-User; a
// script's navigation suppresses it with a nil Sec-Fetch-User header.
const (
	Navigate       Kind = iota // a top-level navigation: typed URL, link, form
	Frame                      // <iframe src>
	Script                     // <script src> in the HTML: blocking
	AsyncScript                // <script async src>
	DeferScript                // <script defer src>
	InjectedScript             // a script element created by script
	PreloadScript              // <link rel=preload as=script>
	Style                      // <link rel=stylesheet>
	Image                      // <img src> in the HTML
	InjectedImage              // new Image()
	Font                       // a web font from CSS @font-face
	PreloadFont                // <link rel=preload as=font crossorigin>
	Fetch                      // fetch()
	XHR                        // XMLHttpRequest
	Beacon                     // navigator.sendBeacon
)

// Preflight is the kind of a CORS preflight, the OPTIONS request the
// transport sends before a request that needs one. It appears only in
// plans (the Kind of Plan.Preflight): the transport makes preflights
// itself, and a request sent As or With Preflight fails.
const Preflight Kind = -1

// kindWebSocket is a WebSocket's opening handshake, which DialWebSocket
// makes; callers never make it as a request.
const kindWebSocket Kind = -2

// profileKinds names each kind in profile.Profile.Kinds.
var profileKinds = map[Kind]string{
	Frame:          profile.KindFrame,
	Script:         profile.KindScript,
	AsyncScript:    profile.KindAsyncScript,
	DeferScript:    profile.KindDeferScript,
	InjectedScript: profile.KindInjectedScript,
	PreloadScript:  profile.KindPreloadScript,
	Style:          profile.KindStyle,
	Image:          profile.KindImage,
	InjectedImage:  profile.KindInjectedImage,
	Font:           profile.KindFont,
	PreloadFont:    profile.KindPreloadFont,
	Fetch:          profile.KindFetch,
	XHR:            profile.KindXHR,
	Beacon:         profile.KindBeacon,
	Preflight:      profile.KindPreflight,
	kindWebSocket:  profile.KindWebSocket,
}

func (k Kind) String() string {
	if k == Navigate {
		return "navigate"
	}

	if name, ok := profileKinds[k]; ok {
		return name
	}

	return fmt.Sprintf("Kind(%d)", int(k))
}

// elements are the kinds an HTML element makes: those that take a
// crossorigin attribute, and fonts, which load in CORS mode without one.
var elements = []Kind{
	Script, AsyncScript, DeferScript, InjectedScript, PreloadScript,
	Style, Image, InjectedImage, Font, PreloadFont,
}

// Priority is the fetchpriority a page gives a request.
type Priority int

// Priorities. PriorityAuto is the kind's own.
const (
	PriorityAuto Priority = iota
	PriorityHigh
	PriorityLow
)

// CrossOrigin is an element's crossorigin attribute, which makes its request
// a CORS request.
type CrossOrigin int

// CrossOrigin values. CrossOriginNone is no attribute: the element's own mode.
const (
	CrossOriginNone           CrossOrigin = iota
	CrossOriginAnonymous                  // crossorigin, crossorigin="anonymous"
	CrossOriginUseCredentials             // crossorigin="use-credentials"
)

// Credentials is a fetch() or XHR request's credentials mode: whether it
// carries cookies.
type Credentials int

// Credentials modes. CredentialsSameOrigin is fetch()'s default and XHR's without
// withCredentials.
const (
	CredentialsSameOrigin Credentials = iota
	CredentialsInclude                // credentials: "include", withCredentials = true
	CredentialsOmit                   // credentials: "omit"
)

// Request describes how a page makes a request, for With. The zero value
// is a typed-URL navigation.
type Request struct {
	// Kind is the kind of request the page makes (Navigate, Fetch, Image,
	// ...): it picks the profile's headers, their order and values.
	Kind Kind
	// From is the page making the request: it decides sec-fetch-site,
	// Origin and Referer. Nil means none for a navigation (a typed URL),
	// and for other kinds the transport's last navigation, else the
	// request's own origin.
	From *url.URL
	// Priority is the request's fetchpriority. RoundTrip fails if the
	// profile never measured PriorityHigh or PriorityLow for the kind.
	Priority Priority
	// CrossOrigin is an element kind's crossorigin attribute.
	CrossOrigin CrossOrigin
	// Credentials is a Fetch or XHR request's credentials mode.
	Credentials Credentials
	// NoCORS is fetch()'s mode: "no-cors".
	NoCORS bool
	// Stylesheet is the stylesheet loading a Font or Image. Browsers that
	// refer such requests to it (profile.Context.StylesheetReferrer) send
	// its URL as Referer; sec-fetch-site and Origin stay From's.
	Stylesheet *url.URL
	// IgnoreCORS sends a request whose CORS preflight answer forbids it,
	// which a browser never does; otherwise RoundTrip returns a
	// *CORSError. The preflight is still sent.
	IgnoreCORS bool
	// HeaderPlacement moves this request's headers, on every redirect hop
	// but not its preflight, after Config.HeaderPlacement. An invalid Place,
	// or any Place on a request with its own order (Exact, HeaderOrderKey),
	// is an error wrapping ErrPlacement.
	HeaderPlacement []Place
}

type requestKey struct{}

type exactKey struct{}

// As returns req, made as kind k by the page the transport last navigated
// to: With(req, Request{Kind: k}). The last navigation is the transport's,
// whichever goroutine made it; parallel tasks browsing different pages
// use a Transport each, or set Request.From.
func As(req *http.Request, k Kind) *http.Request {
	return With(req, Request{
		Kind: k,
	})
}

// With returns a shallow copy of req made as r describes.
func With(req *http.Request, r Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), requestKey{}, r))
}

// Exact returns a shallow copy of req that sends exactly its own headers,
// in its own order (HeaderOrderKey) or sorted by name, plus Config.Header:
// no profile headers and nothing computed. TLS, HTTP/2 and the cookie jar
// still follow the profile.
func Exact(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), exactKey{}, true))
}

// madeAs returns how req is made: as With's Request describes (a
// navigation by default), or Exact.
func madeAs(req *http.Request) (r Request, exact bool) {
	ctx := req.Context()

	r, _ = ctx.Value(requestKey{}).(Request)
	exact, _ = ctx.Value(exactKey{}).(bool)

	return r, exact
}

// sending is how to send one request: the plan (nil: the profile's
// navigation, unchanged), its headers, the connection partition, and how
// the cookie jar sees it.
type sending struct {
	plan      *headerorder.Plan
	header    http.Header
	partition string
	// credentialed: the request sends and keeps cookies.
	credentialed bool
	cookies      cookies.Request
	// corsOrigin is the Origin a request in CORS mode to another origin
	// (fetch(), XHR, an element with crossorigin, a font) sends, which its
	// redirects must allow (Access-Control-Allow-Origin) for the browser
	// to follow them; "" for other requests.
	corsOrigin string
	// preflighted: the request needed a CORS preflight (sent or cached),
	// which a profile's later hops may inherit
	// (redirects.hopPreflight).
	preflighted bool
	// cond is the request's condition, which picks its variant; variant
	// is the index of the one it takes in the navigation's or kind's
	// variants, -1 for their own lists (Plan.Variant).
	cond    profile.Variant
	variant int
}

// exactSending returns how to send req as Exact does: exactly its own
// headers plus config (Config.Header, keys canonical), in its own order,
// keeping cookies as a navigation from page.
func exactSending(req *http.Request, page *url.URL, config http.Header) *sending {
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}

	chain := append(redirectsBefore(req), req.URL)
	addHeaders(h, config, chain)

	return &sending{
		plan:   &headerorder.Plan{},
		header: h,
		// Exact still keeps cookies, as a navigation from its page.
		credentialed: true,
		cookies: cookies.Request{
			URL:        req.URL,
			Method:     req.Method,
			Navigation: true,
			Page:       page,
			Redirects:  chain[:len(chain)-1],
		},
		variant: -1,
	}
}

// use is why a request is worked out: to send it (RoundTrip) or only to
// plan it (Plan). They differ only where sending has effects: a hop's body
// and the cookie jar's access times.
type use int

// Uses.
const (
	toSend use = iota
	toPlan
)

// HeaderOrderKey is a pseudo request header whose values are header names,
// in the order to send them; it is never sent itself. It replaces the
// profile's order, so profile defaults it does not name are not sent; the
// request's own unnamed headers follow the named ones. It overrides
// Config.HeaderPlacement; use a Place to move one header instead.
const HeaderOrderKey = headerorder.Key

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

// setsOrder reports whether h sets HeaderOrderKey (headerorder.Key).
func setsOrder(h http.Header) bool {
	for k, v := range h {
		if len(v) > 0 && strings.EqualFold(k, headerorder.Key) {
			return true
		}
	}

	return false
}
