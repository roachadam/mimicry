package profile

// Redirects is how a browser follows redirects, where browsers differ.
// What every captured browser does alike is mimicry's own: a CORS
// request's redirect must pass the CORS check, a POST answered 301 or 302
// and anything but GET and HEAD answered 303 go on as a GET without their
// body, other methods keep it, and a chain stops after 20 redirects.
type Redirects struct {
	// MethodChangeOrigin is what a redirect that turns a request into a
	// GET does with the Origin header the request sent:
	// RedirectOriginKeep sends it on (Safari);
	// RedirectOriginDrop leaves it to the GET's own rules (Chrome,
	// Firefox: none to the page's origin).
	MethodChangeOrigin string `json:"method_change_origin"`
}

// Values of Redirects.MethodChangeOrigin.
const (
	RedirectOriginKeep = "keep"
	RedirectOriginDrop = "drop"
)

// checkRedirects checks the redirects section. Nil is valid: not measured,
// so a redirect that changes a request's method is an error.
func (c *checker) checkRedirects(r *Redirects) {
	if r == nil {
		return
	}
	if r.MethodChangeOrigin != RedirectOriginKeep && r.MethodChangeOrigin != RedirectOriginDrop {
		c.add("redirects.method_change_origin", "must be %q or %q", RedirectOriginKeep, RedirectOriginDrop)
	}
}
