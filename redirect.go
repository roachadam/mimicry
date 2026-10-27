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

// browserHop returns the redirect hop req as the browser sends it. POST on
// 301/302 and non-GET/HEAD on 303 become a body-less GET, as http.Client
// does; any other request keeps the method, body and body headers that
// http.Client drops. Method and body come from req.Response.Request, so
// they carry across hops. A chain past the browser's limit (p's, for a
// navigation) is an error. Only when sending is the body reopened; a plan
// gets an unread body.
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

// refusedRedirect returns a *CORSError, closing resp's body, when resp is
// a redirect a cross-origin CORS-mode request may not follow because it
// fails the CORS check. Every captured browser stopped there (cors-edge,
// redirects2). Nil otherwise.
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
