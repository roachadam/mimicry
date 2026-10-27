// Package request works out how the root package sends a request as its
// browser does: the profile's lists for its kind and condition (the
// variant), the headers the page decides (sec-fetch-*, Origin, Referer,
// priority), credentials and the connection partition, and each redirect
// hop as the browser makes it. It never imports the root package, whose
// public types convert to the inputs here (Made, Place).
package request

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// Kind is how a page makes a request: the root package's Kind, with the
// same values, plus the transport's own Preflight and WebSocket.
type Kind int

// Kinds, in the root package's order: it converts its Kind to this one.
const (
	Navigate Kind = iota
	Frame
	Script
	AsyncScript
	DeferScript
	InjectedScript
	PreloadScript
	Style
	Image
	InjectedImage
	Font
	PreloadFont
	Fetch
	XHR
	Beacon
	// KindCount is the number of kinds above (untyped: not a Kind), which
	// the root checks its own against.
	KindCount = iota
)

// Preflight is the CORS preflight the transport sends before a request
// that needs one; callers never make it.
const Preflight Kind = -1

// WebSocket is a WebSocket's opening handshake, which DialWebSocket
// makes; callers never make it as a request.
const WebSocket Kind = -2

// names are the kinds' names in profile.Profile.Kinds.
var names = map[Kind]string{
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
	WebSocket:      profile.KindWebSocket,
}

func (k Kind) String() string {
	if k == Navigate {
		return "navigate"
	}

	if name, ok := names[k]; ok {
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

// Priority is the fetchpriority a page gives a request (the root's
// Priority, same values).
type Priority int

// Priorities. PriorityAuto is the kind's own.
const (
	PriorityAuto Priority = iota
	PriorityHigh
	PriorityLow
	// PriorityCount is the number of priorities (untyped: not one).
	PriorityCount = iota
)

// CrossOrigin is an element's crossorigin attribute (the root's
// CrossOrigin, same values).
type CrossOrigin int

// CrossOrigin values. CrossOriginNone is no attribute: the element's own mode.
const (
	CrossOriginNone CrossOrigin = iota
	CrossOriginAnonymous
	CrossOriginUseCredentials
	// CrossOriginCount is the number of CrossOrigin values (untyped: not one).
	CrossOriginCount = iota
)

// Credentials is a fetch() or XHR request's credentials mode (the root's
// Credentials, same values).
type Credentials int

// Credentials modes. CredentialsSameOrigin is fetch()'s default and XHR's
// without withCredentials.
const (
	CredentialsSameOrigin Credentials = iota
	CredentialsInclude
	CredentialsOmit
	// CredentialsCount is the number of modes (untyped: not one).
	CredentialsCount = iota
)

// Made is how a page makes a request: the root's Request, less what the
// transport handles itself (the page, CORS refusals, placements).
type Made struct {
	Kind        Kind
	Priority    Priority
	CrossOrigin CrossOrigin
	Credentials Credentials
	// NoCORS is fetch()'s mode: "no-cors".
	NoCORS bool
	// Stylesheet is the stylesheet loading a Font or Image.
	Stylesheet *url.URL
}

// Sending is how to send one request: the plan (nil: the profile's
// navigation, unchanged), its headers, the connection partition, and how
// the cookie jar sees it.
type Sending struct {
	Plan      *headerorder.Plan
	Header    http.Header
	Partition string
	// Credentialed: the request sends and keeps cookies.
	Credentialed bool
	Cookies      cookies.Request
	// CORSOrigin is the Origin a request in CORS mode to another origin
	// (fetch(), XHR, an element with crossorigin, a font) sends, which its
	// redirects must allow (Access-Control-Allow-Origin) for the browser
	// to follow them; "" for other requests.
	CORSOrigin string
	// Preflighted: the request needed a CORS preflight (sent or cached),
	// which a profile's later hops may inherit
	// (redirects.hopPreflight).
	Preflighted bool
	// Cond is the request's condition, which picks its variant; Variant
	// is the index of the one it takes in the navigation's or kind's
	// variants, -1 for their own lists (mimicry.Plan.Variant).
	Cond    profile.Variant
	Variant int
}

// Exact returns how to send req as the root's Exact does: exactly its own
// headers plus config (Config.Headers, keys canonical), in its own order,
// keeping cookies as a navigation from page.
func Exact(req *http.Request, page *url.URL, config http.Header) *Sending {
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}

	chain := append(redirectsBefore(req), req.URL)
	addHeaders(h, config, chain)

	return &Sending{
		Plan:   &headerorder.Plan{},
		Header: h,
		// Exact still keeps cookies, as a navigation from its page.
		Credentialed: true,
		Cookies: cookies.Request{
			URL:        req.URL,
			Method:     req.Method,
			Navigation: true,
			Page:       page,
			Redirects:  chain[:len(chain)-1],
		},
		Variant: -1,
	}
}

// Use is why a request is worked out: to send it (RoundTrip) or only to
// plan it (Plan). They differ only where sending has effects: a hop's body
// and the cookie jar's access times.
type Use int

// Uses.
const (
	ToSend Use = iota
	ToPlan
)
