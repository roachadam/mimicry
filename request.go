package mimicry

import (
	"context"
	"net/http"
	"net/url"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/request"
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

// kindPreflight is the CORS preflight the transport sends before a request
// that needs one; callers never make it.
const kindPreflight = Kind(request.Preflight)

// kindWebSocket is a WebSocket's opening handshake, which DialWebSocket
// makes; callers never make it as a request.
const kindWebSocket = Kind(request.WebSocket)

func (k Kind) String() string {
	return request.Kind(k).String()
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

// internal/request mirrors these enums: each line fails to compile (a negative constant converted to uint) unless the pair, or the last value and the count, agree.
const (
	_ = uint(Navigate-Kind(request.Navigate)) + uint(Kind(request.Navigate)-Navigate)
	_ = uint(Frame-Kind(request.Frame)) + uint(Kind(request.Frame)-Frame)
	_ = uint(Script-Kind(request.Script)) + uint(Kind(request.Script)-Script)
	_ = uint(AsyncScript-Kind(request.AsyncScript)) + uint(Kind(request.AsyncScript)-AsyncScript)
	_ = uint(DeferScript-Kind(request.DeferScript)) + uint(Kind(request.DeferScript)-DeferScript)
	_ = uint(InjectedScript-Kind(request.InjectedScript)) + uint(Kind(request.InjectedScript)-InjectedScript)
	_ = uint(PreloadScript-Kind(request.PreloadScript)) + uint(Kind(request.PreloadScript)-PreloadScript)
	_ = uint(Style-Kind(request.Style)) + uint(Kind(request.Style)-Style)
	_ = uint(Image-Kind(request.Image)) + uint(Kind(request.Image)-Image)
	_ = uint(InjectedImage-Kind(request.InjectedImage)) + uint(Kind(request.InjectedImage)-InjectedImage)
	_ = uint(Font-Kind(request.Font)) + uint(Kind(request.Font)-Font)
	_ = uint(PreloadFont-Kind(request.PreloadFont)) + uint(Kind(request.PreloadFont)-PreloadFont)
	_ = uint(Fetch-Kind(request.Fetch)) + uint(Kind(request.Fetch)-Fetch)
	_ = uint(XHR-Kind(request.XHR)) + uint(Kind(request.XHR)-XHR)
	_ = uint(Beacon-Kind(request.Beacon)) + uint(Kind(request.Beacon)-Beacon)
	_ = uint(int(Beacon)+1-request.KindCount) + uint(request.KindCount-int(Beacon)-1)
	_ = uint(PriorityAuto-Priority(request.PriorityAuto)) + uint(Priority(request.PriorityAuto)-PriorityAuto)
	_ = uint(PriorityHigh-Priority(request.PriorityHigh)) + uint(Priority(request.PriorityHigh)-PriorityHigh)
	_ = uint(PriorityLow-Priority(request.PriorityLow)) + uint(Priority(request.PriorityLow)-PriorityLow)
	_ = uint(int(PriorityLow)+1-request.PriorityCount) + uint(request.PriorityCount-int(PriorityLow)-1)
	_ = uint(CrossOriginNone-CrossOrigin(request.CrossOriginNone)) + uint(CrossOrigin(request.CrossOriginNone)-CrossOriginNone)
	_ = uint(CrossOriginAnonymous-CrossOrigin(request.CrossOriginAnonymous)) + uint(CrossOrigin(request.CrossOriginAnonymous)-CrossOriginAnonymous)
	_ = uint(CrossOriginUseCredentials-CrossOrigin(request.CrossOriginUseCredentials)) + uint(CrossOrigin(request.CrossOriginUseCredentials)-CrossOriginUseCredentials)
	_ = uint(int(CrossOriginUseCredentials)+1-request.CrossOriginCount) + uint(request.CrossOriginCount-int(CrossOriginUseCredentials)-1)
	_ = uint(CredentialsSameOrigin-Credentials(request.CredentialsSameOrigin)) + uint(Credentials(request.CredentialsSameOrigin)-CredentialsSameOrigin)
	_ = uint(CredentialsInclude-Credentials(request.CredentialsInclude)) + uint(Credentials(request.CredentialsInclude)-CredentialsInclude)
	_ = uint(CredentialsOmit-Credentials(request.CredentialsOmit)) + uint(Credentials(request.CredentialsOmit)-CredentialsOmit)
	_ = uint(int(CredentialsOmit)+1-request.CredentialsCount) + uint(request.CredentialsCount-int(CredentialsOmit)-1)
)

// made is r as internal/request takes it.
func (r Request) made() request.Made {
	return request.Made{
		Kind:        request.Kind(r.Kind),
		Priority:    request.Priority(r.Priority),
		CrossOrigin: request.CrossOrigin(r.CrossOrigin),
		Credentials: request.Credentials(r.Credentials),
		NoCORS:      r.NoCORS,
		Stylesheet:  r.Stylesheet,
	}
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
// in its own order (HeaderOrderKey) or sorted by name, plus Config.Headers:
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
// connections, lowercase (request.Placements).
func placements(field string, ps []Place) ([]headerorder.Placement, error) {
	in := make([]request.Place, len(ps))
	for i, p := range ps {
		in[i] = request.Place(p)
	}

	return request.Placements(field, in)
}
