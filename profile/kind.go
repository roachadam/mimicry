package profile

import (
	"fmt"
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
)

// Kinds lists every kind a profile may describe, in a stable order.
var Kinds = []string{
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
	// Connection placed.
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
// browsers build headers along different paths, so a POST or a CORS-checked
// request can order them otherwise. A request takes the variant whose
// condition it matches exactly, else the kind's own order.
type Variant struct {
	// Method is the request's method; empty is GET.
	Method string `json:"method,omitempty"`
	// Site is the request's sec-fetch-site relation to its page:
	// SiteSameSite or SiteCrossSite; empty is same-origin.
	Site string `json:"site,omitempty"`
	// CORSChecked is a request that carries Origin or headers the page
	// set.
	CORSChecked bool     `json:"cors_checked,omitempty"`
	Headers     []Header `json:"headers"`
	HTTP1       []Header `json:"http1,omitempty"`
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
	// FetchRedirectDrops are the headers a fetch() or XHR request loses
	// after a redirect (Safari: its sec-fetch-* headers, accept-language
	// and priority).
	FetchRedirectDrops []string `json:"fetch_redirect_drops,omitempty"`
	// AnonymousConnections keeps requests sent without credentials on
	// connections of their own, separate from those that carry cookies
	// (Chrome).
	AnonymousConnections bool `json:"anonymous_connections,omitempty"`
	// FrameConnections keeps a cross-site frame's requests on connections of
	// their own (Chrome's network partitioning by frame site).
	FrameConnections bool `json:"frame_connections,omitempty"`
}

// checkKinds checks every kind's lists and that only known kinds appear.
func (c *checker) checkKinds(kinds map[string]*Kind) {
	names := make([]string, 0, len(kinds))
	for name := range kinds {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		k := kinds[name]
		path := "kinds." + name
		if !slices.Contains(Kinds, name) {
			c.add(path, "is not a request kind (have %s)", strings.Join(Kinds, ", "))
		}
		if k == nil {
			c.add(path, "is empty")
			continue
		}
		c.checkHeaderList(path+".headers", k.Headers, true)
		if len(k.HTTP1) > 0 {
			c.checkHTTP1Headers(path+".http1", k.HTTP1, true)
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
		method, site string
		cors         bool
	}
	seen := map[condition]bool{}
	for i, v := range vs {
		at := fmt.Sprintf("%s[%d]", path, i)
		c.checkHeaderList(at+".headers", v.Headers, others)
		if len(v.HTTP1) > 0 {
			c.checkHTTP1Headers(at+".http1", v.HTTP1, others)
		}
		if v.Method == "GET" || strings.ContainsAny(v.Method, " \r\n") {
			c.add(at+".method", "must be empty for GET, or a method")
		}
		if v.Site != SiteSameOrigin && v.Site != SiteSameSite && v.Site != SiteCrossSite {
			c.add(at+".site", "must be empty (same-origin), %q or %q", SiteSameSite, SiteCrossSite)
		}
		cond := condition{
			method: v.Method,
			site:   v.Site,
			cors:   v.CORSChecked,
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
