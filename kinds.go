package mimicry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/profile"
)

// Kind is how a page makes a request, named after the HTML or JavaScript
// that makes it. Browsers send each kind with headers, an order and a
// priority of its own; a request's kind picks the profile's description of
// it.
type Kind int

// Request kinds. Navigate is the default: a request without a kind is a
// top-level navigation, sent exactly as the profile describes it.
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

// Priorities. Auto is the kind's own.
const (
	Auto Priority = iota
	High
	Low
)

// CrossOrigin is an element's crossorigin attribute, which makes its request
// a CORS request.
type CrossOrigin int

// CrossOrigin values. None is no attribute: the element's own mode.
const (
	None           CrossOrigin = iota
	Anonymous                  // crossorigin, crossorigin="anonymous"
	UseCredentials             // crossorigin="use-credentials"
)

// Credentials is a fetch() or XHR request's credentials mode: whether it
// carries cookies.
type Credentials int

// Credentials modes. SameOrigin is fetch()'s default and XHR's without
// withCredentials.
const (
	SameOrigin Credentials = iota
	Include                // credentials: "include", withCredentials = true
	Omit                   // credentials: "omit"
)

// Request describes how a page makes a request, for With. The zero value
// is a typed-URL navigation.
type Request struct {
	Kind Kind
	// From is the page making the request: it decides sec-fetch-site,
	// Origin and Referer. Nil: for a navigation, none (a typed URL); for
	// other kinds, the transport's last navigation, or else the request's
	// own origin.
	From *url.URL
	// Priority is the request's fetchpriority. High and Low need the
	// profile to have measured them for the kind; RoundTrip fails
	// otherwise.
	Priority Priority
	// CrossOrigin is an element kind's crossorigin attribute.
	CrossOrigin CrossOrigin
	// Credentials is a Fetch or XHR request's credentials mode.
	Credentials Credentials
	// NoCORS is fetch()'s mode: "no-cors".
	NoCORS bool
	// Stylesheet is the stylesheet loading the request, for the fonts and
	// images CSS loads. Browsers that refer such requests to their
	// stylesheet (Chrome, Firefox; profile.Context.StylesheetReferrer) send
	// its URL as Referer; sec-fetch-site and Origin stay From's.
	Stylesheet *url.URL
}

type requestKey struct{}

type exactKey struct{}

// As returns req, made as kind k by the page the transport last navigated
// to: With(req, Request{Kind: k}).
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

// errNotMeasured reports a request the profile cannot describe.
var errNotMeasured = errors.New("mimicry: not measured for this profile")

// resolve returns the plan for req as r describes it from page from (nil:
// none), and the headers to send with it; nil plan: the profile's
// navigation, unchanged.
func resolve(p *profile.Profile, req *http.Request, r Request, from *url.URL) (*headerorder.Plan, http.Header, error) {
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	var (
		list, h1 []profile.Header
		variants []profile.Variant
		weight   uint8
		kind     *profile.Kind
	)
	if r.Kind == Navigate {
		if r.Priority != Auto || r.CrossOrigin != None || r.Credentials != SameOrigin || r.NoCORS {
			return nil, nil, fmt.Errorf("mimicry: a navigation takes no priority, crossorigin, credentials or mode")
		}
		list, variants = p.Headers, p.Variants
		if p.HTTP1 != nil {
			h1 = p.HTTP1.Headers
		}
	} else {
		name, ok := profileKinds[r.Kind]
		if !ok {
			return nil, nil, fmt.Errorf("mimicry: unknown request kind %d", int(r.Kind))
		}
		if kind = p.Kinds[name]; kind == nil {
			return nil, nil, fmt.Errorf("%w: profile %s has no %s requests", errNotMeasured, p.Name, r.Kind)
		}
		if r.CrossOrigin != None && !slices.Contains(elements, r.Kind) {
			return nil, nil, fmt.Errorf("mimicry: %s requests take no crossorigin attribute", r.Kind)
		}
		if (r.Credentials != SameOrigin || r.NoCORS) && r.Kind != Fetch && (r.Kind != XHR || r.NoCORS) {
			return nil, nil, fmt.Errorf("mimicry: %s requests take no credentials mode or no-cors", r.Kind)
		}
		list, h1, variants, weight = kind.Headers, kind.HTTP1, kind.Variants, kind.Weight
	}

	ctx := p.Context
	if ctx == nil {
		ctx = &profile.Context{}
	}
	target := req.URL
	site, crossOrigin := "none", false
	if from != nil {
		site, crossOrigin = relation(from, target)
	}

	values := map[string]string{}
	mode := valueOf(list, "sec-fetch-mode")
	switch {
	case r.CrossOrigin != None:
		mode = "cors"
	case r.NoCORS:
		mode = "no-cors"
	}
	if r.Kind != Navigate {
		values["sec-fetch-mode"] = mode
	}
	if r.Kind != Navigate || from != nil {
		values["sec-fetch-site"] = site
	}

	// Credentials: fetch() and XHR follow their credentials mode
	// (same-origin by default, whatever the mode); elements carry them in
	// no-cors mode, and in CORS mode only same-origin unless
	// crossorigin="use-credentials"; navigations, frames and beacons always.
	var credentialed bool
	switch {
	case r.Kind == Fetch || r.Kind == XHR:
		credentialed = r.Credentials == Include || (r.Credentials == SameOrigin && !crossOrigin)
	case mode == "cors":
		credentialed = !crossOrigin || r.CrossOrigin == UseCredentials
	default:
		credentialed = true
	}
	if !credentialed {
		h.Del("Cookie")
	}

	// Origin: on every request but GET and HEAD, and on CORS requests to
	// another origin (or same-origin element ones, where the browser does).
	sendOrigin := false
	if from != nil {
		switch {
		case req.Method != http.MethodGet && req.Method != http.MethodHead:
			sendOrigin = true
		case mode == "cors" && crossOrigin:
			sendOrigin = true
		case mode == "cors" && ctx.SameOriginElementOrigin && slices.Contains(elements, r.Kind):
			sendOrigin = true
		}
	}
	if sendOrigin {
		values["origin"] = origin(from)
	}
	referrerURL := from
	if r.Stylesheet != nil && ctx.StylesheetReferrer {
		referrerURL = r.Stylesheet
	}
	if ref := referrer(referrerURL, target); ref != "" {
		values["referer"] = ref
	}
	if site == "cross-site" && credentialed && ctx.StorageAccess != "" {
		values["sec-fetch-storage-access"] = ctx.StorageAccess
	}

	switch r.Priority {
	case High, Low:
		var u *profile.Urgency
		if kind != nil {
			u = kind.High
			if r.Priority == Low {
				u = kind.Low
			}
		}
		if u == nil {
			return nil, nil, fmt.Errorf("%w: profile %s has no measured fetchpriority %s for %s requests", errNotMeasured, p.Name, map[Priority]string{High: "high", Low: "low"}[r.Priority], r.Kind)
		}
		values["priority"] = u.Priority
		if u.Priority == "" {
			// The browser sends none: suppress the kind's default.
			if _, set := h["Priority"]; !set {
				h["Priority"] = nil
			}
		}
		if u.Weight != 0 {
			weight = u.Weight
		}
	}

	// The order: the variant for this method, site relation and CORS
	// check, else the kind's own.
	cond := profile.Variant{
		Site:        map[string]string{"same-site": profile.SiteSameSite, "cross-site": profile.SiteCrossSite}[site],
		CORSChecked: sendOrigin || pageSet(h, list),
	}
	if req.Method != http.MethodGet {
		cond.Method = req.Method
	}
	for _, v := range variants {
		if v.Method == cond.Method && v.Site == cond.Site && v.CORSChecked == cond.CORSChecked {
			list, h1 = v.Headers, v.HTTP1
			break
		}
	}

	if r.Kind == Navigate && len(values) == 0 && cond.Method == "" {
		// A typed GET navigation: the profile's own lists.
		return nil, h, nil
	}
	plan := &headerorder.Plan{
		Headers: fill(list, values),
		Weight:  weight,
	}
	if len(h1) > 0 {
		plan.HTTP1 = fill(h1, values)
	}
	return plan, h, nil
}

// fill copies list with the computed values set, case-blind. A computed
// header the list does not place goes last, except priority: a list
// without it is a protocol the browser sends none over (Chrome's
// HTTP/1.1).
func fill(list []profile.Header, values map[string]string) []profile.Header {
	out := make([]profile.Header, 0, len(list)+len(values))
	placed := make(map[string]bool, len(values))
	for _, hd := range list {
		lower := strings.ToLower(hd.Name)
		if v, ok := values[lower]; ok {
			hd.Value = v
			placed[lower] = true
		}
		out = append(out, hd)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !placed[name] && values[name] != "" && name != "priority" {
			out = append(out, profile.Header{
				Name:  name,
				Value: values[name],
			})
		}
	}
	return out
}

func valueOf(list []profile.Header, name string) string {
	for _, h := range list {
		if h.Name == name {
			return h.Value
		}
	}
	return ""
}

// pageSet reports whether h carries a header list does not name: one the
// page set itself, which makes the browser CORS-check the request.
func pageSet(h http.Header, list []profile.Header) bool {
	for name := range h {
		lower := strings.ToLower(name)
		switch lower {
		case "cookie", "content-type", "content-length", strings.ToLower(HeaderOrderKey):
			continue
		}
		if !slices.ContainsFunc(list, func(hd profile.Header) bool {
			return hd.Name == lower
		}) {
			return true
		}
	}
	return false
}

// relation returns target's sec-fetch-site relation to page from, and
// whether it is another origin. Sites are schemeful: scheme plus registrable
// domain (public suffix list), or the host itself for IPs and hosts without
// one.
func relation(from, target *url.URL) (site string, crossOrigin bool) {
	if origin(from) == origin(target) {
		return "same-origin", false
	}
	if from.Scheme == target.Scheme && registrable(from.Hostname()) == registrable(target.Hostname()) {
		return "same-site", true
	}
	return "cross-site", true
}

func registrable(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if net.ParseIP(host) != nil {
		return host
	}
	if site, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return site
	}
	return host
}

// origin serializes u's origin: scheme://host[:port], the port only when it
// is not the scheme's default.
func origin(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return strings.ToLower(u.Scheme) + "://" + host
}

// referrer is the Referer for a request from page from to target under the
// browsers' default policy, strict-origin-when-cross-origin: the page's URL
// (without fragment or credentials) to its own origin, its origin to others,
// nothing from HTTPS to HTTP.
func referrer(from, target *url.URL) string {
	if from == nil || (from.Scheme == "https" && target.Scheme == "http") {
		return ""
	}
	if origin(from) != origin(target) {
		return origin(from) + "/"
	}
	u := *from
	u.User, u.Fragment, u.RawFragment = nil, "", ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}
