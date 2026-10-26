package mimicry

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/roachadam/mimicry/profile"
)

// maxRedirects is how many redirects a chain follows: every captured
// browser's fetch() requested the 20th redirect's URL and not the 21st
// (the cors-edge capture's chain.so, as the Fetch standard says). A
// navigation's limit is the profile's (redirects.navigation_limit).
const maxRedirects = 20

// navigationRedirects is how many redirects every captured browser's
// top-level navigation follows: Chrome stopped there, Firefox and Safari
// followed one more (the redirects2 capture's nav-chain.so).
const navigationRedirects = 19

// bodyHeaders are the request-body headers the Fetch standard drops with
// the body when a redirect turns a request into a GET, and http.Client
// drops whenever it drops the body.
var bodyHeaders = []string{"Content-Encoding", "Content-Language", "Content-Location", "Content-Type"}

// browserHop returns req, a hop http.Client built to follow a redirect, as
// the browser sends it: a POST answered 301 or 302, and anything but GET
// and HEAD answered 303, go on as a GET without their body (as http.Client
// sends them); every other request keeps its method, body and body
// headers, which http.Client drops for PUT, DELETE and others answered 301
// or 302. A chain longer than the browser follows is an error: the
// browser fails the request rather than send it. That is maxRedirects,
// but for a navigation (nav), whose limit is p's. The method and body
// come from the request the redirect answered as it was sent
// (req.Response.Request), so a chain keeps them hop after hop. Only to
// send (u) is that body opened (its GetBody) and req's own closed; a plan
// carries a body nothing reads (unread) in its place.
func browserHop(p *profile.Profile, req *http.Request, nav bool, u use) (*http.Request, error) {
	prev := req.Response.Request
	if prev == nil {
		return req, nil
	}
	n := len(redirectsBefore(req))
	limit := maxRedirects
	if nav {
		switch {
		case p.Redirects != nil:
			limit = p.Redirects.NavigationLimit
		case n > navigationRedirects:
			return nil, fmt.Errorf("%w: profile %s has no redirects rules, for a navigation's redirect %d (every browser follows %d)", ErrNotMeasured, p.Name, n, navigationRedirects)
		}
	}
	if n > limit {
		return nil, fmt.Errorf("mimicry: stopped after %d redirects", limit)
	}
	status := req.Response.StatusCode
	method := prev.Method
	if method == "" {
		method = http.MethodGet
	}
	switch {
	case (status == http.StatusMovedPermanently || status == http.StatusFound) && method == http.MethodPost:
		method = http.MethodGet
	case status == http.StatusSeeOther && method != http.MethodGet && method != http.MethodHead:
		method = http.MethodGet
	}
	if method != prev.Method && method == http.MethodGet {
		// http.Client changes the method and drops the body and its
		// headers alike.
		return req, nil
	}
	prevBody := prev.Body != nil && prev.Body != http.NoBody
	reqBody := req.Body != nil && req.Body != http.NoBody
	if req.Method == method && (reqBody || !prevBody) {
		return req, nil
	}
	// http.Client turned the request into a GET, or dropped its body on an
	// earlier hop for good; the browser keeps both.
	if prevBody && prev.GetBody == nil {
		return nil, fmt.Errorf("mimicry: cannot follow the %d redirect from %s %s as the browser does, with the body: the request has no GetBody", status, method, prev.URL.Redacted())
	}
	next := req.Clone(req.Context())
	next.Method = method
	if reqBody && u == toSend {
		_ = req.Body.Close()
	}
	next.Body, next.GetBody, next.ContentLength = nil, nil, 0
	switch {
	case prevBody && u == toPlan:
		next.Body, next.GetBody, next.ContentLength = unread{}, prev.GetBody, prev.ContentLength
	case prevBody:
		body, err := prev.GetBody()
		if err != nil {
			return nil, err
		}
		next.Body, next.GetBody, next.ContentLength = body, prev.GetBody, prev.ContentLength
	}
	for k, v := range prev.Header {
		for _, name := range bodyHeaders {
			if strings.EqualFold(k, name) && !hasHeader(next.Header, name) {
				next.Header[k] = v
			}
		}
	}
	return next, nil
}

// refusedRedirect returns the *CORSError for resp, the response to req
// (sent as s says), when resp is a redirect the browser does not follow: a
// request in CORS mode to another origin (fetch(), XHR, an element with
// crossorigin, a font) needs its redirects to pass the CORS check too
// (Access-Control-Allow-Origin, and -Credentials with credentials). Every
// captured browser stopped there: fetch() with or without a preflight
// before, with and without credentials (cors-edge, redirects2), XHR,
// <img crossorigin> and <script crossorigin> (redirects2). It closes
// resp's body then. Nil otherwise.
func refusedRedirect(req *http.Request, resp *http.Response, s *sending) error {
	if s.corsOrigin == "" || resp.Header.Get("Location") == "" {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return nil
	}
	reason := checkCORS(resp.Header, s.corsOrigin, s.credentialed)
	if reason == "" {
		return nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	return &CORSError{
		URL:      req.URL,
		Redirect: true,
		Reason:   reason,
		Status:   resp.StatusCode,
		Header:   resp.Header,
	}
}

// followRedirects is NewClient's CheckRedirect: the Transport stops a chain
// where the browser does (maxRedirects), not http.Client's default of 10.
func followRedirects(*http.Request, []*http.Request) error {
	return nil
}
