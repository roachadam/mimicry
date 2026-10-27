package profile

// Redirects is how a browser follows redirects, where browsers differ;
// what they share is mimicry's own. See decision 0022.
type Redirects struct {
	// MethodChangeOrigin is what a redirect that turns a fetch() or XHR
	// into a GET does with the Origin it sent: RedirectOriginKeep sends it
	// on; RedirectOriginDrop leaves it to the GET's own rules.
	MethodChangeOrigin string `json:"method_change_origin"`
	// NavigationLimit is how many redirects a top-level navigation
	// follows; the request after the last is never sent (Chrome: 19).
	// Required.
	NavigationLimit int `json:"navigation_limit"`
	// HopPreflight is whether a CORS request's cross-origin hop is
	// preflighted because the request before it was:
	// RedirectPreflightOwn decides each hop alone; RedirectPreflightInherit
	// preflights it. Required.
	HopPreflight string `json:"hop_preflight"`
	// NavigationSite is the Sec-Fetch-Site of a navigation hop after a
	// redirect to another site: RedirectSiteChain uses the whole chain;
	// RedirectSiteRestart counts only URLs from that redirect on.
	// Required.
	NavigationSite string `json:"navigation_site"`
	// NavigationOrigin is when a navigation's Origin (a form POST kept by
	// 307 or 308) becomes "null": RedirectNavOriginTaint once the chain is
	// tainted per Fetch; RedirectNavOriginCrossOrigin once any redirect
	// changed origin. Required.
	NavigationOrigin string `json:"navigation_origin"`
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

// Values of Redirects.NavigationOrigin.
const (
	RedirectNavOriginTaint       = "taint"
	RedirectNavOriginCrossOrigin = "cross_origin"
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
	if r.NavigationOrigin != RedirectNavOriginTaint && r.NavigationOrigin != RedirectNavOriginCrossOrigin {
		c.add("redirects.navigation_origin", "must be %q or %q", RedirectNavOriginTaint, RedirectNavOriginCrossOrigin)
	}
}
