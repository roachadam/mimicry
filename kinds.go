package mimicry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/site"
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

// kindPreflight is the CORS preflight the transport sends before a request
// that needs one; callers never make it.
const kindPreflight Kind = -1

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
	kindPreflight:  profile.KindPreflight,
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
	Kind Kind
	// From is the page making the request: it decides sec-fetch-site,
	// Origin and Referer. Nil: for a navigation, none (a typed URL); for
	// other kinds, the transport's last navigation, or else the request's
	// own origin. The last navigation is the transport's, shared by every
	// goroutine using it: a Transport is one browsing entity, like one tab.
	From *url.URL
	// Priority is the request's fetchpriority. PriorityHigh and PriorityLow need the
	// profile to have measured them for the kind; RoundTrip fails
	// otherwise.
	Priority Priority
	// CrossOrigin is an element kind's crossorigin attribute.
	CrossOrigin CrossOrigin
	// Credentials is a Fetch or XHR request's credentials mode.
	Credentials Credentials
	// NoCORS is fetch()'s mode: "no-cors".
	NoCORS bool
	// Stylesheet is the stylesheet loading the request, for the fonts
	// (Font) and images (Image) CSS loads; other kinds take none. Browsers
	// that refer such requests to their stylesheet (Chrome, Firefox;
	// profile.Context.StylesheetReferrer) send its URL as Referer;
	// sec-fetch-site and Origin stay From's.
	Stylesheet *url.URL
	// IgnoreCORS sends a request whose CORS preflight's answer does not
	// allow it, which a browser never does: RoundTrip otherwise returns a
	// *CORSError and sends nothing more. The preflight is still sent.
	IgnoreCORS bool
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

// ErrNotMeasured reports a request the profile cannot describe: one its
// browser was never captured making. RoundTrip returns it (wrapped, with
// what is missing) rather than guess. Test for it with errors.Is.
var ErrNotMeasured = headerorder.ErrNotMeasured

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
	// (redirects.hop_preflight).
	preflighted bool
}

// resolve returns how to send req as r describes it, from page from (nil:
// none), with config (Config.Headers, keys canonical) added.
func resolve(p *profile.Profile, req *http.Request, r Request, from *url.URL, config http.Header) (*sending, error) {
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	// The redirect chain this request ends, first URL first.
	target := req.URL
	chain := append(redirectsBefore(req), target)
	// Browsers send Sec-Fetch-*, client hints and their full
	// Accept-Encoding only to potentially trustworthy URLs.
	insecure := !site.Trustworthy(target)
	// insecureBefore: the request a redirect answered went to a non-secure
	// URL (http:// upgraded to https://).
	insecureBefore := len(chain) > 1 && !site.Trustworthy(chain[len(chain)-2])
	// Config.Headers count as the page's from here on: for the variant,
	// credentials and the preflight.
	addHeaders(h, config, chain)
	if r.Stylesheet != nil && r.Kind != Font && r.Kind != Image {
		return nil, fmt.Errorf("mimicry: %s requests take no stylesheet: CSS loads fonts and images", r.Kind)
	}
	var (
		list, h1 []profile.Header
		variants []profile.Variant
		weight   uint8
		kind     *profile.Kind
	)
	if r.Kind == Navigate {
		if r.Priority != PriorityAuto || r.CrossOrigin != CrossOriginNone || r.Credentials != CredentialsSameOrigin || r.NoCORS {
			return nil, errors.New("mimicry: a navigation takes no priority, crossorigin, credentials or mode")
		}
		if from == nil && req.Method != http.MethodGet && req.Method != http.MethodHead {
			// Only a page's form makes a navigation that is not a GET: no
			// browser types one into the address bar.
			return nil, fmt.Errorf("mimicry: a %s navigation needs Request.From, the page whose form sends it; an API call is a Fetch", req.Method)
		}
		list, variants = p.Headers, p.Variants
		if p.HTTP1 != nil {
			h1 = p.HTTP1.Headers
		}
	} else {
		name, ok := profileKinds[r.Kind]
		if !ok {
			return nil, fmt.Errorf("mimicry: unknown request kind %d", int(r.Kind))
		}
		if kind = p.Kinds[name]; kind == nil {
			return nil, fmt.Errorf("%w: profile %s has no %s requests", ErrNotMeasured, p.Name, r.Kind)
		}
		if r.CrossOrigin != CrossOriginNone && !slices.Contains(elements, r.Kind) {
			return nil, fmt.Errorf("mimicry: %s requests take no crossorigin attribute", r.Kind)
		}
		if (r.Credentials != CredentialsSameOrigin || r.NoCORS) && r.Kind != Fetch && (r.Kind != XHR || r.NoCORS) {
			return nil, fmt.Errorf("mimicry: %s requests take no credentials mode or no-cors", r.Kind)
		}
		// No browser sends these: the Fetch standard's Request constructor
		// refuses a no-cors one, and sendBeacon always POSTs.
		if r.NoCORS && !safelistedMethod(req.Method) {
			return nil, fmt.Errorf("mimicry: a no-cors fetch() cannot use method %s, only GET, HEAD or POST", req.Method)
		}
		if r.Kind == Beacon && req.Method != http.MethodPost {
			return nil, fmt.Errorf("mimicry: a beacon is a POST, not %s", req.Method)
		}
		list, h1, variants, weight = kind.Headers, kind.HTTP1, kind.Variants, kind.Weight
	}

	ctx := p.Context
	if ctx == nil {
		ctx = &profile.Context{}
	}
	redirected := len(chain) > 1
	if redirected {
		// http.Client sets Referer to the previous URL when it follows a
		// redirect; browsers keep the page's. A caller's own value stays.
		prev := *chain[len(chain)-2]
		prev.User = nil
		if h.Get("Referer") == prev.String() {
			h.Del("Referer")
		}
	}
	// site is sec-fetch-site: the most distant relation along the chain
	// (for a navigation under redirects.navigation_site restart, the
	// chain from its last redirect to another site on). targetSite is
	// this URL's own, which picks the order and decides
	// sec-fetch-storage-access. tainted is the Fetch standard's tainted
	// origin: the chain left another origin than the page's for a third,
	// so Origin is "null". sameOrigin is a chain that never left the
	// page's origin, which same-origin credentials need.
	site, targetSite := "none", "none"
	crossOrigin, tainted, sameOrigin := false, false, true
	if from != nil {
		targetSite, crossOrigin = relation(from, target)
		site = targetSite
		counted := 0
		if r.Kind == Navigate && p.Redirects != nil && p.Redirects.NavigationSite == profile.RedirectSiteRestart {
			for i := len(chain) - 1; i > 0; i-- {
				if rel, _ := relation(chain[i-1], chain[i]); rel == "cross-site" {
					counted = i
					break
				}
			}
		}
		for i, u := range chain {
			s, other := relation(from, u)
			if i >= counted && siteRank[s] > siteRank[site] {
				site = s
			}
			sameOrigin = sameOrigin && !other
		}
		tainted = isTainted(from, chain)
	}

	values := map[string]string{}
	mode := valueOf(list, "sec-fetch-mode")
	switch {
	case r.CrossOrigin != CrossOriginNone:
		mode = "cors"
	case r.NoCORS:
		mode = "no-cors"
	}
	if r.Kind != Navigate {
		values["sec-fetch-mode"] = mode
	}
	if r.Kind != Navigate || from != nil || redirected {
		// A typed navigation's redirect keeps sec-fetch-site none, which
		// its hop's list leaves to fill.
		values["sec-fetch-site"] = site
	}

	// Credentials: fetch() and XHR follow their credentials mode
	// (same-origin by default, whatever the mode); elements carry them in
	// no-cors mode, and in CORS mode only same-origin unless
	// crossorigin="use-credentials"; navigations, frames and beacons always.
	var credentialed bool
	switch {
	case r.Kind == kindPreflight:
		// A preflight never carries credentials (Fetch standard), as
		// every browser's captures show.
		credentialed = false
	case r.Kind == Fetch || r.Kind == XHR:
		credentialed = r.Credentials == CredentialsInclude || (r.Credentials == CredentialsSameOrigin && sameOrigin)
	case mode == "cors":
		credentialed = sameOrigin || r.CrossOrigin == CrossOriginUseCredentials
	default:
		credentialed = true
	}
	if !credentialed {
		delHeader(h, "Cookie")
	}

	// Origin: on every request but GET and HEAD, and on CORS requests to
	// another origin (or same-origin element ones, where the browser does);
	// "null" once the chain is tainted.
	sentOrigin, err := originOf(p, ctx, req, r, mode, from)
	if err != nil {
		return nil, err
	}
	sendOrigin := sentOrigin != ""
	if sendOrigin {
		values["origin"] = sentOrigin
	}
	referrerURL := from
	if r.Stylesheet != nil && ctx.StylesheetReferrer {
		referrerURL = r.Stylesheet
	}
	if ref := referrer(referrerURL, chain); ref != "" {
		values["referer"] = ref
	}
	if targetSite == "cross-site" && credentialed && ctx.StorageAccess != "" {
		values["sec-fetch-storage-access"] = ctx.StorageAccess
	}
	if r.Kind == kindPreflight {
		// Safari sends an empty preflight's length; lists without the
		// slot drop it.
		values["content-length"] = "0"
	}
	// Connections: Chrome keeps credential-less requests, and a cross-site
	// frame's navigation, on connections of their own.
	s := &sending{
		header:       h,
		credentialed: credentialed,
		cookies: cookies.Request{
			URL:        target,
			Method:     req.Method,
			Navigation: r.Kind == Navigate,
			Page:       from,
			Redirects:  chain[:len(chain)-1],
		},
	}
	if sendOrigin && mode == "cors" && (crossOrigin || tainted) && (r.Kind == Fetch || r.Kind == XHR || slices.Contains(elements, r.Kind)) {
		s.corsOrigin = values["origin"]
	}
	switch {
	case ctx.FrameConnections && r.Kind == Frame && targetSite == "cross-site":
		s.partition = framePartition(target)
	case ctx.AnonymousConnections && !credentialed:
		s.partition = "anonymous"
	}

	switch r.Priority {
	case PriorityHigh, PriorityLow:
		var u *profile.Urgency
		if kind != nil {
			u = kind.High
			if r.Priority == PriorityLow {
				u = kind.Low
			}
		}
		if u == nil {
			return nil, fmt.Errorf("%w: profile %s has no measured fetchpriority %s for %s requests", ErrNotMeasured, p.Name, map[Priority]string{PriorityHigh: "high", PriorityLow: "low"}[r.Priority], r.Kind)
		}
		values["priority"] = u.Priority
		if u.Priority == "" {
			// The browser sends none: suppress the kind's default.
			if !hasHeader(h, "Priority") {
				h["Priority"] = nil
			}
		}
		if u.Weight != 0 {
			weight = u.Weight
		}
	}

	// The order: the variant for this method, site relation, CORS check,
	// page-set headers, redirect (and whether it crossed origins, whether
	// the request before it carried Origin, and whether it changed the
	// method) and security, else the kind's own; a redirected request of a
	// kind with redirected variants takes only its exact one. A non-secure
	// request (plain http:// to anything but loopback) never takes the
	// secure lists: its exact variant, else the kind's non-secure one.
	set := pageSet(h, list)
	if r.Kind == kindPreflight {
		// A preflight carries the page's headers by name only
		// (Access-Control-Request-Headers): Safari orders one that does
		// otherwise (kinds and cors pages, against redirects2's PATCH).
		set = hasHeader(h, "Access-Control-Request-Headers")
	}
	var table *profile.HeaderTable
	if r.Kind == Fetch || r.Kind == XHR {
		table = ctx.HeaderTable
		combinePageHeaders(h, list)
	}
	cond := profile.Variant{
		Site:        map[string]string{"same-site": profile.SiteSameSite, "cross-site": profile.SiteCrossSite, "none": profile.SiteNone}[targetSite],
		CORSChecked: sendOrigin || set,
		PageSet:     set,
		// A preflight for a redirect's next URL is a request of its own.
		Redirected: redirected && r.Kind != kindPreflight,
		Insecure:   insecure,
	}
	if req.Method != http.MethodGet {
		cond.Method = req.Method
	}
	if cond.Redirected {
		prev := req.Response.Request
		cond.CrossOriginRedirect = origin(chain[len(chain)-2]) != origin(target)
		before, err := originOf(p, ctx, prev, r, mode, from)
		if err != nil {
			return nil, err
		}
		cond.OriginBeforeRedirect = before != ""
		cond.MethodChangedByRedirect = methodOf(prev) != methodOf(req)
		cond.InsecureBeforeRedirect = insecureBefore
		if r.Kind == Navigate {
			rel, _ := relation(chain[len(chain)-2], target)
			cond.CrossSiteRedirect = rel == "cross-site"
		}
	}
	i := slices.IndexFunc(variants, func(v profile.Variant) bool {
		return sameCondition(v, cond)
	})
	if i < 0 && set && table != nil {
		if cond.Redirected && !table.Redirected {
			// The captures do not show the table ordering a redirect's
			// request: only a capture of this condition says how.
			return nil, fmt.Errorf("%w: profile %s has no %s request after a redirect with headers the page set for method %q, site %q, non-secure %v", ErrNotMeasured, p.Name, r.Kind, cond.Method, cond.Site, cond.Insecure)
		}
		// The browser's header table places the page's headers among its
		// own: the order is the request's without them, the table's run
		// added (headerorder.TableOrder).
		base := cond
		base.CORSChecked, base.PageSet = sendOrigin, false
		i = slices.IndexFunc(variants, func(v profile.Variant) bool {
			return sameCondition(v, base)
		})
	}
	if i < 0 && cond.Redirected && slices.ContainsFunc(variants, func(v profile.Variant) bool {
		return v.Redirected
	}) {
		// The kind's redirects were captured, and browsers order a
		// redirect's request by how it was made (Safari rebuilds it
		// whole): no other list is this one.
		return nil, fmt.Errorf("%w: profile %s has no %s request after a redirect for method %q, site %q, CORS-checked %v, page-set headers %v, cross-origin redirect %v, cross-site redirect %v, Origin before the redirect %v, method changed by the redirect %v, non-secure before the redirect %v, non-secure %v", ErrNotMeasured, p.Name, r.Kind, cond.Method, cond.Site, cond.CORSChecked, cond.PageSet, cond.CrossOriginRedirect, cond.CrossSiteRedirect, cond.OriginBeforeRedirect, cond.MethodChangedByRedirect, cond.InsecureBeforeRedirect, cond.Insecure)
	}
	if r.Kind == Navigate && cond.Redirected && i >= 0 && sentFetchUser(p, req.Response.Request) && !hasHeader(h, "Sec-Fetch-User") && !slices.ContainsFunc(variants[i].Headers, func(hd profile.Header) bool {
		return strings.EqualFold(hd.Name, "sec-fetch-user")
	}) {
		// The request before sent Sec-Fetch-User (a user's navigation: a
		// typed URL, a link), and the hop's variant was measured on
		// navigations without it (a script's): where the browser keeps it
		// on the hop is not measured.
		return nil, fmt.Errorf("%w: profile %s has no redirect of a navigation sending Sec-Fetch-User (a typed URL or a link) for site %q, cross-origin redirect %v; a script's navigation suppresses it (a nil Sec-Fetch-User header)", ErrNotMeasured, p.Name, cond.Site, cond.CrossOriginRedirect)
	}
	measured := i >= 0
	if i < 0 && cond.Insecure {
		i = slices.IndexFunc(variants, func(v profile.Variant) bool {
			return v.Insecure && v.Method == "" && v.Site == "" && !v.CORSChecked && !v.PageSet && !v.Redirected
		})
		if i < 0 {
			return nil, fmt.Errorf("%w: profile %s has no %s request to a non-secure origin (http://) for method %q, site %q, CORS-checked %v, page-set headers %v, redirected %v", ErrNotMeasured, p.Name, r.Kind, cond.Method, cond.Site, cond.CORSChecked, cond.PageSet, cond.Redirected)
		}
	}
	if i >= 0 {
		list, h1 = variants[i].Headers, variants[i].HTTP1
	}
	// A variant measured over HTTP/2 only has no HTTP/1.1 list: the
	// connection refuses it if it turns out to be HTTP/1.1.
	noHTTP1 := i >= 0 && len(h1) == 0 && p.HTTP1 != nil
	if _, ok := values["origin"]; ok && !measured {
		// A fallback list (the kind's own, or its non-secure one) without
		// an Origin slot was measured only on requests without one: it
		// cannot say where this one sends it. Non-secure lists are
		// HTTP/1.1 only.
		placing := list
		if cond.Insecure {
			placing = h1
		}
		if !slices.ContainsFunc(placing, func(hd profile.Header) bool {
			return strings.EqualFold(hd.Name, "origin")
		}) {
			return nil, fmt.Errorf("%w: profile %s has no %s request with Origin for method %q, site %q, CORS-checked %v, page-set headers %v, redirected %v, non-secure %v", ErrNotMeasured, p.Name, r.Kind, cond.Method, cond.Site, cond.CORSChecked, cond.PageSet, cond.Redirected, cond.Insecure)
		}
	}

	if r.Kind == Navigate && i < 0 && len(values) == 0 && cond.Method == "" && !cond.Insecure {
		// A typed GET navigation: the profile's own lists.
		return s, nil
	}
	s.plan = &headerorder.Plan{
		Headers: fill(list, values),
		NoHTTP1: noHTTP1,
		Weight:  weight,
		Table:   table,
		// fetch()'s Headers object lowercases names; XHR keeps the
		// page's spelling (headers-order capture, over HTTP/1.1).
		LowerOwn: r.Kind == Fetch,
		// A body whose measured list has no content-length goes without
		// one (Safari's redirected PATCH and POST after a preflight).
		NoContentLength: measured && req.Body != nil && req.Body != http.NoBody && !slices.ContainsFunc(list, func(hd profile.Header) bool {
			return strings.EqualFold(hd.Name, "content-length")
		}),
	}
	if len(h1) > 0 {
		s.plan.HTTP1 = fill(h1, values)
	}
	return s, nil
}

// fill copies list with the computed values set, case-blind. A computed
// header the list does not place is not sent: the list is what the browser
// sends in this case (no priority over Chrome's HTTP/1.1, no sec-fetch-* on
// Safari's redirected fetches).
func fill(list []profile.Header, values map[string]string) []profile.Header {
	out := make([]profile.Header, len(list))
	for i, hd := range list {
		if v, ok := values[strings.ToLower(hd.Name)]; ok {
			hd.Value = v
		}
		out[i] = hd
	}
	return out
}

// siteRank orders sec-fetch-site values from nearest to most distant.
var siteRank = map[string]int{
	"none":        0,
	"same-origin": 1,
	"same-site":   2,
	"cross-site":  3,
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
// page set itself, which makes the browser CORS-check the request. A key
// with no values suppresses a default and sends nothing: not one.
func pageSet(h http.Header, list []profile.Header) bool {
	for name, values := range h {
		if len(values) > 0 && pageHeader(name, list) {
			return true
		}
	}
	return false
}

// pageHeader reports whether name is a header the page set itself: one
// list does not name, other than those any request may carry (cookie, the
// body's type and length) and HeaderOrderKey.
func pageHeader(name string, list []profile.Header) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "cookie", "content-type", "content-length", strings.ToLower(HeaderOrderKey):
		return false
	}
	return !slices.ContainsFunc(list, func(hd profile.Header) bool {
		return hd.Name == lower
	})
}

// combinePageHeaders gives each header the page set itself one line, as
// fetch() and XHR send a name set twice, in any case: its values joined by
// ", " (headers-order capture, every browser), under the page's first
// spelling, here the first of h's in sorted order.
func combinePageHeaders(h http.Header, list []profile.Header) {
	spellings := map[string][]string{}
	for name, values := range h {
		if len(values) > 0 && pageHeader(name, list) {
			key := strings.ToLower(name)
			spellings[key] = append(spellings[key], name)
		}
	}
	for _, names := range spellings {
		if len(names) == 1 && len(h[names[0]]) == 1 {
			continue
		}
		slices.Sort(names)
		var values []string
		for _, name := range names {
			values = append(values, h[name]...)
			delete(h, name)
		}
		h[names[0]] = []string{strings.Join(values, ", ")}
	}
}

// sameCondition reports whether variant v is for exactly the requests cond
// describes.
func sameCondition(v, cond profile.Variant) bool {
	return v.Method == cond.Method && v.Site == cond.Site && v.CORSChecked == cond.CORSChecked && v.PageSet == cond.PageSet && v.Redirected == cond.Redirected && v.CrossOriginRedirect == cond.CrossOriginRedirect && v.CrossSiteRedirect == cond.CrossSiteRedirect && v.OriginBeforeRedirect == cond.OriginBeforeRedirect && v.MethodChangedByRedirect == cond.MethodChangedByRedirect && v.InsecureBeforeRedirect == cond.InsecureBeforeRedirect && v.Insecure == cond.Insecure
}

// sentFetchUser reports whether navigation req, as sent, carried
// Sec-Fetch-User: its plan's list (none: the profile's navigation) has
// it with a value, and the caller did not suppress it.
func sentFetchUser(p *profile.Profile, req *http.Request) bool {
	list := p.Headers
	if plan := headerorder.PlanFrom(req.Context()); plan != nil {
		list = plan.Headers
	}
	return valueOf(list, "sec-fetch-user") != "" && !hasHeader(req.Header, "Sec-Fetch-User")
}

// methodOf is req's method, GET when empty.
func methodOf(req *http.Request) string {
	if req.Method == "" {
		return http.MethodGet
	}
	return req.Method
}

// originOf returns the Origin req sends, made as r in mode (sec-fetch-mode)
// from page from (nil: none sent): on every request but GET and HEAD, and
// on CORS requests to another origin (or same-origin element ones, where
// the browser does); "null" once its redirect chain is tainted; "" for
// none.
func originOf(p *profile.Profile, ctx *profile.Context, req *http.Request, r Request, mode string, from *url.URL) (string, error) {
	if from == nil {
		return "", nil
	}
	_, crossOrigin := relation(from, req.URL)
	tainted := isTainted(from, append(redirectsBefore(req), req.URL))
	send := false
	switch {
	case req.Method != http.MethodGet && req.Method != http.MethodHead:
		send = true
	case mode == "cors" && (crossOrigin || tainted):
		send = true
	case mode == "cors" && ctx.SameOriginElementOrigin && slices.Contains(elements, r.Kind):
		send = true
	}
	if !send && mode == "cors" && (r.Kind == Fetch || r.Kind == XHR) && redirectedToGet(req) {
		// A redirect turned the request into a GET: whether the Origin
		// it sent goes on is the browser's (Safari: it does). Measured
		// on fetch(); XHR, the same CORS-mode request, follows it.
		if p.Redirects == nil {
			return "", fmt.Errorf("%w: profile %s has no redirects rules, for a %s redirected to a GET", ErrNotMeasured, p.Name, r.Kind)
		}
		send = p.Redirects.MethodChangeOrigin == profile.RedirectOriginKeep
	}
	switch {
	case !send:
		return "", nil
	case tainted:
		return "null", nil
	}
	return origin(from), nil
}

// hasHeader reports whether h has name, in any case, even with no values
// (a suppressed default).
func hasHeader(h http.Header, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// delHeader deletes name from h in every case.
func delHeader(h http.Header, name string) {
	for k := range h {
		if strings.EqualFold(k, name) {
			delete(h, k)
		}
	}
}

// addHeaders adds config's headers (Config.Headers) that h does not set,
// in any case, to a request ending redirect chain chain. Once the chain
// has left an origin, Authorization and Cookie stay behind: browsers drop
// Authorization on a cross-origin redirect (Fetch standard), and
// http.Client both on one to another domain.
func addHeaders(h, config http.Header, chain []*url.URL) {
	crossed := false
	for i := 1; i < len(chain); i++ {
		crossed = crossed || origin(chain[i-1]) != origin(chain[i])
	}
	for k, v := range config {
		if crossed && (k == "Authorization" || k == "Cookie") {
			continue
		}
		if !hasHeader(h, k) {
			h[k] = v
		}
	}
}

// relation returns target's sec-fetch-site relation to page from, and
// whether it is another origin. Sites are schemeful (internal/site).
func relation(from, target *url.URL) (rel string, crossOrigin bool) {
	if origin(from) == origin(target) {
		return "same-origin", false
	}
	if site.Same(from, target) {
		return "same-site", true
	}
	return "cross-site", true
}

// isTainted reports whether a redirect chain from page from went from
// another origin than the page's to yet another one, which makes the
// request's origin opaque (Origin: null).
func isTainted(from *url.URL, chain []*url.URL) bool {
	for i := 1; i < len(chain); i++ {
		if origin(chain[i-1]) != origin(chain[i]) && origin(chain[i-1]) != origin(from) {
			return true
		}
	}
	return false
}

// framePartition is the connection partition of a cross-site frame's
// navigation: its own site.
func framePartition(target *url.URL) string {
	return "frame " + site.Of(target)
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

// referrer is the Referer for a request from page from along chain (its
// URLs, the request's last) under the browsers' default policy,
// strict-origin-when-cross-origin: the page's URL (without fragment or
// credentials) to its own origin, its origin to others, nothing from
// HTTPS to HTTP. Each redirect's request applies the policy to the
// referrer the request before it sent (the Fetch standard's main fetch),
// so a chain that left the page's origin sends only the origin even back
// on it, and none at all after an http:// URL (every browser:
// nav-redirect-page's nav-302.cs-so.hop1 and nav-301.ss-so.hop1).
func referrer(from *url.URL, chain []*url.URL) string {
	if from == nil {
		return ""
	}
	stripped := false
	for _, target := range chain {
		if from.Scheme == "https" && target.Scheme == "http" {
			return ""
		}
		stripped = stripped || origin(from) != origin(target)
	}
	if stripped {
		return origin(from) + "/"
	}
	u := *from
	u.User, u.Fragment, u.RawFragment = nil, "", ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

// redirectsBefore returns the URLs that redirected to req, first first:
// http.Client sets Response on each request it makes by following a
// redirect.
func redirectsBefore(req *http.Request) []*url.URL {
	var urls []*url.URL
	for prev := req; prev.Response != nil && prev.Response.Request != nil; {
		prev = prev.Response.Request
		urls = append([]*url.URL{prev.URL}, urls...)
	}
	return urls
}

// redirectedToGet reports a GET that a redirect made of a request with
// another method than GET or HEAD (browserHop's rules).
func redirectedToGet(req *http.Request) bool {
	if (req.Method != "" && req.Method != http.MethodGet) || req.Response == nil || req.Response.Request == nil {
		return false
	}
	prev := req.Response.Request.Method
	return prev != "" && prev != http.MethodGet && prev != http.MethodHead
}
