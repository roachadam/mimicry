package profile

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Request kinds a profile describes besides the navigation (Profile.Headers,
// HTTP1 and HTTP2.HeadersPriority). Each is one way a page makes a request,
// named after the HTML or JavaScript that makes it.
const (
	KindFrame          = "frame"           // <iframe src>
	KindScript         = "script"          // <script src> in the HTML: blocking
	KindAsyncScript    = "async_script"    // <script async src>
	KindDeferScript    = "defer_script"    // <script defer src>
	KindInjectedScript = "injected_script" // a script element created by script
	KindPreloadScript  = "preload_script"  // <link rel=preload as=script>
	KindStyle          = "style"           // <link rel=stylesheet>
	KindImage          = "image"           // <img src> in the HTML
	KindInjectedImage  = "injected_image"  // new Image()
	KindFont           = "font"            // a web font from CSS @font-face
	KindPreloadFont    = "preload_font"    // <link rel=preload as=font crossorigin>
	KindFetch          = "fetch"           // fetch()
	KindXHR            = "xhr"             // XMLHttpRequest
	KindBeacon         = "beacon"          // navigator.sendBeacon
	// KindPreflight is the CORS preflight (OPTIONS) the browser sends
	// itself before a fetch() or XHR that needs one; callers never ask for
	// it.
	KindPreflight = "preflight"
)

// Kinds returns every kind a profile may describe, in a stable order.
func Kinds() []string {
	return slices.Clone(kinds)
}

var kinds = []string{
	KindFrame,
	KindScript,
	KindAsyncScript,
	KindDeferScript,
	KindInjectedScript,
	KindPreloadScript,
	KindStyle,
	KindImage,
	KindInjectedImage,
	KindFont,
	KindPreloadFont,
	KindFetch,
	KindXHR,
	KindBeacon,
	KindPreflight,
}

// OtherHeaders is a header name that, in a kind's lists, marks where headers
// the list does not name go: those the page sets itself (a fetch's custom
// headers). Without it they follow the listed ones.
const OtherHeaders = "*"

// Kind is how a browser sends one kind of request.
type Kind struct {
	// Headers over HTTP/2, in order, as Profile.Headers: default values,
	// and slots (empty values) for headers that depend on the request
	// (referer, origin, cookie, sec-fetch-site, content-type, ...). This
	// is the order of a same-origin GET that is not CORS-checked.
	Headers []Header `json:"headers"`
	// HTTP1 is the same over HTTP/1.1, named exactly as sent, with Host and
	// Connection placed. Required when the profile has HTTP1.
	HTTP1 []Header `json:"http1,omitempty"`
	// Variants are the orders the browser uses instead for other requests
	// of the kind, with values as Headers'.
	Variants []Variant `json:"variants,omitempty"`
	// Weight is the HEADERS frame's priority weight; the dependency and
	// exclusivity are HTTP2.HeadersPriority's. Zero keeps
	// HTTP2.HeadersPriority's weight.
	Weight uint8 `json:"weight,omitempty"`
	// High and Low are the priority header and weight the kind gets with
	// fetchpriority high or low. Nil: not measured, so not offered.
	High *Urgency `json:"high,omitempty"`
	Low  *Urgency `json:"low,omitempty"`
}

// Variant is the header order a browser uses for some requests of a kind:
// browsers build headers along different paths, so a POST, a CORS-checked
// request or one after a redirect can order them otherwise, and send
// fewer. A request takes the variant whose condition it matches exactly,
// else the kind's own order. A variant's lists are exactly what the browser
// sent: a header they lack is not sent.
type Variant struct {
	// Method is the request's method; empty is GET.
	Method string `json:"method,omitempty"`
	// Site is the request URL's own relation to its page (after a
	// redirect too, where sec-fetch-site reports the whole chain's):
	// SiteSameSite or SiteCrossSite; empty is same-origin.
	Site string `json:"site,omitempty"`
	// CORSChecked is a request that carries Origin or headers the page
	// set.
	CORSChecked bool `json:"cors_checked,omitempty"`
	// PageSet is a request that carries a header the page set itself (not
	// one the kind's list names). Such a request is CORS-checked, so
	// PageSet needs CORSChecked.
	PageSet bool `json:"page_set,omitempty"`
	// Redirected is a request made by following a redirect.
	Redirected bool `json:"redirected,omitempty"`
	// Insecure is a request to a URL that is not potentially trustworthy
	// (plain http:// to anything but loopback): browsers send no
	// Sec-Fetch-* or client hints and fewer encodings, over HTTP/1.1 only,
	// so such a variant has HTTP1 and no Headers. A non-secure request with
	// no variant for its exact condition takes the kind's non-secure one
	// (Insecure alone); a kind with none is not measured for it.
	Insecure bool     `json:"insecure,omitempty"`
	Headers  []Header `json:"headers,omitempty"`
	// HTTP1 is the variant over HTTP/1.1. Empty on a secure variant: not
	// measured over HTTP/1.1 (its capture was HTTP/2 only), so a request
	// matching it there is an error.
	HTTP1 []Header `json:"http1,omitempty"`
}

// Site relations of a request to its page, as sec-fetch-site names them.
const (
	SiteSameOrigin = ""
	SiteSameSite   = "same-site"
	SiteCrossSite  = "cross-site"
)

// Urgency is a request's priority signals.
type Urgency struct {
	// Priority is the priority header's value; empty sends none.
	Priority string `json:"priority,omitempty"`
	Weight   uint8  `json:"weight,omitempty"`
}

// Context is how a browser fills the headers and connections that depend on
// which page makes a request and how.
type Context struct {
	// StorageAccess is the sec-fetch-storage-access value on cross-site
	// requests that carry credentials ("active" in Chrome, "none" in
	// Firefox); empty sends none (Safari).
	StorageAccess string `json:"storage_access,omitempty"`
	// SameOriginElementOrigin sends Origin on same-origin requests that
	// elements make in CORS mode (a crossorigin attribute, fonts), as
	// Chrome and Safari do; Firefox sends it on cross-origin ones only.
	SameOriginElementOrigin bool `json:"same_origin_element_origin,omitempty"`
	// StylesheetReferrer sends a stylesheet's URL as the Referer of the
	// fonts and images it loads (Chrome, Firefox); otherwise the page's
	// (Safari).
	StylesheetReferrer bool `json:"stylesheet_referrer,omitempty"`
	// AnonymousConnections keeps requests sent without credentials on
	// connections of their own, separate from those that carry cookies
	// (Chrome).
	AnonymousConnections bool `json:"anonymous_connections,omitempty"`
	// FrameConnections keeps a cross-site frame's navigation on connections
	// of its own (Chrome's network partitioning by frame site).
	FrameConnections bool `json:"frame_connections,omitempty"`
	// PreflightCache is how successful CORS preflights are remembered, per
	// page origin, URL and credentials mode: PreflightCacheReplace (Chrome,
	// Safari: the latest answer replaces the URL's entry) or
	// PreflightCacheMerge (Firefox: what every answer allowed). Empty: not
	// measured, so requests that need a preflight are an error.
	PreflightCache string `json:"preflight_cache,omitempty"`
}

// Values of Context.PreflightCache.
const (
	PreflightCacheReplace = "replace"
	PreflightCacheMerge   = "merge"
)

// checkKinds checks every kind's lists and that only known kinds appear.
// Once the profile has an HTTP/1.1 navigation, every kind needs its HTTP/1.1
// list: without one, its requests would go out in a guessed order (decision
// 0008).
func (c *checker) checkKinds(p *Profile) {
	for _, name := range slices.Sorted(maps.Keys(p.Kinds)) {
		k := p.Kinds[name]
		path := "kinds." + name
		if !slices.Contains(kinds, name) {
			c.add(path, "is not a request kind (have %s)", strings.Join(kinds, ", "))
		}
		if k == nil {
			c.add(path, "is empty")
			continue
		}
		c.checkHeaderList(path+".headers", k.Headers, true)
		if len(k.HTTP1) > 0 {
			c.checkHTTP1Headers(path+".http1", k.HTTP1, true)
		} else if p.HTTP1 != nil {
			c.add(path+".http1", "is required because the profile has http1")
		}
		c.checkVariants(path+".variants", k.Variants, true)
		for which, u := range map[string]*Urgency{"high": k.High, "low": k.Low} {
			if u != nil && u.Priority == "" && u.Weight == 0 {
				c.add(fmt.Sprintf("%s.%s", path, which), "changes nothing; leave it out")
			}
		}
	}
}

// checkVariants checks each variant's lists, and that no two share a
// condition and none repeats the default one.
func (c *checker) checkVariants(path string, vs []Variant, others bool) {
	type condition struct {
		method, site                        string
		cors, pageSet, redirected, insecure bool
	}
	seen := map[condition]bool{}
	for i, v := range vs {
		at := fmt.Sprintf("%s[%d]", path, i)
		if v.Insecure {
			if len(v.Headers) > 0 {
				c.add(at+".headers", "must be empty: browsers send non-secure requests over HTTP/1.1 only")
			}
			if len(v.HTTP1) == 0 {
				c.add(at+".http1", "is required for a non-secure variant")
			}
		} else {
			c.checkHeaderList(at+".headers", v.Headers, others)
		}
		if len(v.HTTP1) > 0 {
			c.checkHTTP1Headers(at+".http1", v.HTTP1, others)
		}
		if v.Method == "GET" || strings.ContainsAny(v.Method, " \r\n") {
			c.add(at+".method", "must be empty for GET, or a method")
		}
		if v.Site != SiteSameOrigin && v.Site != SiteSameSite && v.Site != SiteCrossSite {
			c.add(at+".site", "must be empty (same-origin), %q or %q", SiteSameSite, SiteCrossSite)
		}
		if v.PageSet && !v.CORSChecked {
			c.add(at+".page_set", "needs cors_checked: a request carrying headers the page set is CORS-checked")
		}
		cond := condition{
			method:     v.Method,
			site:       v.Site,
			cors:       v.CORSChecked,
			pageSet:    v.PageSet,
			redirected: v.Redirected,
			insecure:   v.Insecure,
		}
		switch {
		case cond == condition{}:
			c.add(at, "repeats the default condition")
		case seen[cond]:
			c.add(at, "repeats an earlier variant's condition")
		}
		seen[cond] = true
	}
}
