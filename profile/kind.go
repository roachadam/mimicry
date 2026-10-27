package profile

import (
	"fmt"
	"maps"
	"net/http"
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
	// itself; callers never ask for it.
	KindPreflight = "preflight"
	// KindWebSocket is a WebSocket's opening handshake: an HTTP/1.1
	// Upgrade or an RFC 8441 extended CONNECT. Its lists have slots for
	// Sec-WebSocket-Key, -Extensions and -Protocol.
	KindWebSocket = "websocket"
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
	KindWebSocket,
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

// Variant is the header order a browser uses for some requests of a kind
// (a POST, a CORS-checked request, a redirect). A request takes the variant
// whose condition it matches exactly, else the kind's own order; once a
// kind has a redirected variant, a redirected request takes only an exact
// one. A header the variant's lists lack is not sent.
type Variant struct {
	// Method is the request's method; empty is GET.
	Method string `json:"method,omitempty"`
	// Site is the request URL's own relation to its page (after a
	// redirect too, where sec-fetch-site reports the whole chain's):
	// SiteSameSite or SiteCrossSite; empty is same-origin; SiteNone a
	// navigation no page made (a typed URL), whatever its URL.
	Site string `json:"site,omitempty"`
	// CORSChecked is a request that carries Origin or headers the page
	// set.
	CORSChecked bool `json:"corsChecked,omitempty"`
	// PageSet is a request that carries a header the page set itself (not
	// one the kind's list names). Such a request is CORS-checked, so
	// PageSet needs CORSChecked.
	PageSet bool `json:"pageSet,omitempty"`
	// Redirected is a request made by following a redirect.
	Redirected bool `json:"redirected,omitempty"`
	// CrossOriginRedirect is a redirected request to another origin than
	// the URL whose redirect it follows (Safari then sends no
	// Sec-Fetch-*). Needs Redirected.
	CrossOriginRedirect bool `json:"crossOriginRedirect,omitempty"`
	// OriginBeforeRedirect is a redirected request whose previous request,
	// the one the redirect answered, carried Origin (Chrome keeps that
	// Origin's place; one the redirect adds goes after Sec-Fetch-*).
	// Needs Redirected.
	OriginBeforeRedirect bool `json:"originBeforeRedirect,omitempty"`
	// MethodChangedByRedirect is a redirected request whose method the
	// redirect changed (a PATCH answered 303 goes on as a GET): Chrome
	// drops the Origin the previous request carried and adds its own
	// after Sec-Fetch-*. Needs Redirected.
	MethodChangedByRedirect bool `json:"methodChangedByRedirect,omitempty"`
	// InsecureBeforeRedirect is a redirected request whose previous
	// request went to a non-secure URL (a typed http:// answered with a
	// redirect to https://): Safari carries Upgrade-Insecure-Requests over
	// and orders the hop otherwise. Needs Redirected.
	InsecureBeforeRedirect bool `json:"insecureBeforeRedirect,omitempty"`
	// CrossSiteRedirect is a redirected navigation to another site than
	// the URL whose redirect it follows (schemeful sites): Safari sends
	// Referer before Sec-Fetch-Dest on such a hop, after it on one that
	// stays on its site (nav-redirect-page). Navigations only; needs
	// CrossOriginRedirect.
	CrossSiteRedirect bool `json:"crossSiteRedirect,omitempty"`
	// Insecure is a request to a URL that is not potentially trustworthy
	// (plain http:// to non-loopback). Such a variant has HTTP1 and no
	// Headers. A non-secure request with no exact match takes the kind's
	// Insecure-only variant.
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
	// SiteNone is a navigation's without a page (a typed URL): Safari
	// orders its redirects otherwise than a page's (nav-redirect and
	// redirects2 captures).
	SiteNone = "none"
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
	StorageAccess string `json:"storageAccess,omitempty"`
	// SameOriginElementOrigin sends Origin on same-origin requests that
	// elements make in CORS mode (a crossorigin attribute, fonts), as
	// Chrome and Safari do; Firefox sends it on cross-origin ones only.
	SameOriginElementOrigin bool `json:"sameOriginElementOrigin,omitempty"`
	// StylesheetReferrer sends a stylesheet's URL as the Referer of the
	// fonts and images it loads (Chrome, Firefox); otherwise the page's
	// (Safari).
	StylesheetReferrer bool `json:"stylesheetReferrer,omitempty"`
	// AnonymousConnections keeps requests sent without credentials on
	// connections of their own, separate from those that carry cookies
	// (Chrome).
	AnonymousConnections bool `json:"anonymousConnections,omitempty"`
	// FrameConnections keeps a cross-site frame's navigation on connections
	// of its own (Chrome's network partitioning by frame site).
	FrameConnections bool `json:"frameConnections,omitempty"`
	// PreflightCache is how successful CORS preflights are remembered, per
	// page origin, URL and credentials mode: PreflightCacheReplace (Chrome,
	// Safari: the latest answer replaces the URL's entry) or
	// PreflightCacheMerge (Firefox: what every answer allowed). Empty: not
	// measured, so requests that need a preflight are an error.
	PreflightCache string `json:"preflightCache,omitempty"`
	// HeaderTable is how the browser orders the headers a page sets on a
	// fetch() or XHR among its own, where the kind's list has no
	// OtherHeaders slot. Nil: those lists place them after the rest,
	// sorted by name (Firefox's lists all have the slot).
	HeaderTable *HeaderTable `json:"headerTable,omitempty"`
}

// HeaderTable models a browser's request header hash table: a fetch()'s or
// XHR's headers go out in the table's order, so a page header's place
// depends on every name in the table. The table's names go out as one run,
// where the list puts its first one.
type HeaderTable struct {
	// Hash is the table: HashBlink or HashCF.
	Hash string `json:"hash"`
	// Own are the browser's own headers in the table, lowercase. They
	// enter it in the list's order.
	Own []string `json:"own"`
	// PageFirst: the page's headers enter the table first, then the
	// browser's own (Chrome). Otherwise the browser's own are in the table
	// as the list has them, and the page's enter after (Safari).
	PageFirst bool `json:"pageFirst,omitempty"`
	// AsPage are list headers that enter the table with the page's, sorted
	// among them (Chrome's content-type, which a page sets in its fetch()
	// headers).
	AsPage []string `json:"asPage,omitempty"`
	// Late are list headers that enter the table after the page's
	// (Safari's content-length, set once the body is).
	Late []string `json:"late,omitempty"`
	// Redirected: the table orders a request made by following a redirect
	// as any other (Chrome). Otherwise such a request with the page's
	// headers takes only a variant captured with them (Safari, whose
	// redirects reorder every header).
	Redirected bool `json:"redirected,omitempty"`
}

// Values of HeaderTable.Hash.
const (
	// HashBlink is Blink's HTTPHeaderMap: a WTF::HashMap keyed by the
	// case-folded name (rapidhash), open addressing with triangular
	// probing, 8 buckets doubling at half full.
	HashBlink = "blink"
	// HashCF is CFNetwork's header dictionary: a CFDictionary keyed by
	// the lowercased name (CFString's hash), linear probing, prime
	// bucket counts.
	HashCF = "cf"
)

// Members returns the table's list headers: Own, AsPage and Late.
func (t *HeaderTable) Members() []string {
	return slices.Concat(t.Own, t.AsPage, t.Late)
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
	c.checkHeaderTable(p)
}

// checkHeaderTable checks the context's header table: a known hash, names
// once each, and in every fetch() and XHR list it orders (one without an
// OtherHeaders slot) its names as one run, the table's place.
func (c *checker) checkHeaderTable(p *Profile) {
	if p.Context == nil || p.Context.HeaderTable == nil {
		return
	}
	t := p.Context.HeaderTable
	const path = "context.headerTable"
	if t.Hash != HashBlink && t.Hash != HashCF {
		c.add(path+".hash", "must be %q or %q", HashBlink, HashCF)
	}
	if len(t.Own) == 0 {
		c.add(path+".own", "is required")
	}
	seen := map[string]bool{}
	for _, name := range t.Members() {
		switch {
		case name != strings.ToLower(name) || !isToken(name):
			c.add(path, "%q is not a lowercase header name", name)
		case seen[name]:
			c.add(path, "names %s twice", name)
		}
		seen[name] = true
	}
	for _, kind := range []string{KindFetch, KindXHR} {
		k := p.Kinds[kind]
		if k == nil {
			continue
		}
		lists := map[string][]Header{
			"headers": k.Headers,
			"http1":   k.HTTP1,
		}
		for i, v := range k.Variants {
			if v.Redirected && !t.Redirected {
				// The table does not order these.
				continue
			}
			lists[fmt.Sprintf("variants[%d].headers", i)] = v.Headers
			lists[fmt.Sprintf("variants[%d].http1", i)] = v.HTTP1
		}
		for _, at := range slices.Sorted(maps.Keys(lists)) {
			if !tableRun(lists[at], seen) {
				c.add("kinds."+kind+"."+at, "splits the header table's names %v: they go out as one run", t.Members())
			}
		}
	}
}

// tableRun reports whether list, unless it has an OtherHeaders slot, names
// the table's members (lowercase) next to each other.
func tableRun(list []Header, members map[string]bool) bool {
	first, last, n := -1, -1, 0
	for i, h := range list {
		if h.Name == OtherHeaders {
			return true
		}
		if members[strings.ToLower(h.Name)] {
			if first < 0 {
				first = i
			}
			last = i
			n++
		}
	}

	return n == 0 || last-first+1 == n
}

// checkVariants checks each variant's lists, and that no two share a
// condition and none repeats the default one. others marks a kind's
// variants (a navigation's are the profile's own).
func (c *checker) checkVariants(path string, vs []Variant, others bool) {
	type condition struct {
		method, site                                     string
		cors, pageSet, redirected, insecure              bool
		crossOriginRedirect, originBefore, methodChanged bool
		insecureBefore, crossSiteRedirect                bool
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
		if v.Method == http.MethodGet || strings.ContainsAny(v.Method, " \r\n") {
			c.add(at+".method", "must be empty for GET, or a method")
		}
		if v.Site != SiteSameOrigin && v.Site != SiteSameSite && v.Site != SiteCrossSite && v.Site != SiteNone {
			c.add(at+".site", "must be empty (same-origin), %q, %q or %q", SiteSameSite, SiteCrossSite, SiteNone)
		}
		if v.PageSet && !v.CORSChecked {
			c.add(at+".pageSet", "needs corsChecked: a request carrying headers the page set is CORS-checked")
		}
		if (v.CrossOriginRedirect || v.OriginBeforeRedirect || v.MethodChangedByRedirect || v.InsecureBeforeRedirect) && !v.Redirected {
			c.add(at, "crossOriginRedirect, originBeforeRedirect, methodChangedByRedirect and insecureBeforeRedirect need redirected")
		}
		if v.CrossSiteRedirect && !v.CrossOriginRedirect {
			c.add(at+".crossSiteRedirect", "needs crossOriginRedirect: a redirect to another site goes to another origin")
		}
		if v.CrossSiteRedirect && others {
			c.add(at+".crossSiteRedirect", "is a navigation's condition, not a request kind's")
		}
		cond := condition{
			method:              v.Method,
			site:                v.Site,
			cors:                v.CORSChecked,
			pageSet:             v.PageSet,
			redirected:          v.Redirected,
			insecure:            v.Insecure,
			crossOriginRedirect: v.CrossOriginRedirect,
			originBefore:        v.OriginBeforeRedirect,
			methodChanged:       v.MethodChangedByRedirect,
			insecureBefore:      v.InsecureBeforeRedirect,
			crossSiteRedirect:   v.CrossSiteRedirect,
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
