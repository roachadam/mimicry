package profile

// Redirects is how a browser follows redirects, where browsers differ.
// What every captured browser does alike is mimicry's own: a CORS
// request's redirect must pass the CORS check, a POST answered 301 or 302
// and anything but GET and HEAD answered 303 go on as a GET without their
// body, other methods keep it, a navigation follows at least 19 redirects
// and any other request 20.
type Redirects struct {
	// MethodChangeOrigin is what a redirect that turns a fetch() or XHR
	// into a GET does with the Origin header the request sent:
	// RedirectOriginKeep sends it on (Safari);
	// RedirectOriginDrop leaves it to the GET's own rules (Chrome,
	// Firefox: none to the page's origin). A navigation's GET never
	// carries one (every browser).
	MethodChangeOrigin string `json:"method_change_origin"`
	// NavigationLimit is how many redirects a top-level navigation
	// follows: the request after the last one is never sent (Chrome 19,
	// Firefox and Safari 20; redirects2 capture). Required.
	NavigationLimit int `json:"navigation_limit"`
	// HopPreflight is whether a CORS request's hop to another origin is
	// preflighted because the request before it was, even when its own
	// method and headers need none: RedirectPreflightOwn decides each hop
	// by itself (Chrome); RedirectPreflightInherit preflights it
	// (Firefox, Safari: the GET a cross-site PATCH answered 303 became).
	// Required.
	HopPreflight string `json:"hop_preflight"`
	// NavigationSite is the Sec-Fetch-Site of a navigation's hop after a
	// redirect to another site: RedirectSiteChain is the whole chain's
	// most distant relation to the page, as on every other redirect
	// (Chrome, Firefox, Safari 26); RedirectSiteRestart counts only the
	// URLs from that redirect on (Safari 27: a page's navigation sent to
	// another site and back to the page's origin is same-origin;
	// nav-redirect-page, two samples of three). Required.
	NavigationSite string `json:"navigation_site"`
}

// Values of Redirects.MethodChangeOrigin.
const (
	RedirectOriginKeep = "keep"
	RedirectOriginDrop = "drop"
)

// Values of Redirects.HopPreflight.
const (
	RedirectPreflightOwn     = "own"
	RedirectPreflightInherit = "inherit"
)

// Values of Redirects.NavigationSite.
const (
	RedirectSiteChain   = "chain"
	RedirectSiteRestart = "restart"
)

// checkRedirects checks the redirects section. Nil is valid: not measured,
// so a redirect that changes a fetch()'s method, a navigation's 20th
// redirect and a hop after a preflight are errors.
func (c *checker) checkRedirects(r *Redirects) {
	if r == nil {
		return
	}
	if r.MethodChangeOrigin != RedirectOriginKeep && r.MethodChangeOrigin != RedirectOriginDrop {
		c.add("redirects.method_change_origin", "must be %q or %q", RedirectOriginKeep, RedirectOriginDrop)
	}
	if r.NavigationLimit < 1 {
		c.add("redirects.navigation_limit", "must be positive")
	}
	if r.HopPreflight != RedirectPreflightOwn && r.HopPreflight != RedirectPreflightInherit {
		c.add("redirects.hop_preflight", "must be %q or %q", RedirectPreflightOwn, RedirectPreflightInherit)
	}
	if r.NavigationSite != RedirectSiteChain && r.NavigationSite != RedirectSiteRestart {
		c.add("redirects.navigation_site", "must be %q or %q", RedirectSiteChain, RedirectSiteRestart)
	}
}
