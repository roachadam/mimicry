// Package cors is the Fetch standard's CORS protocol as browsers apply it:
// when a cross-origin fetch() or XHR needs a preflight, whether a
// preflight's answer or a redirect allows the request, and the preflight
// cache. Sending a preflight is the root package's.
package cors

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/roachadam/mimicry/profile"
)

// Need is what a request's preflight asks for it.
type Need struct {
	key    cacheKey
	Method string
	// Headers are the request's CORS-unsafe header names, lowercase,
	// sorted, once each: Access-Control-Request-Headers.
	Headers []string
	// forced: only inheritance asks for it (the root's hopPreflight),
	// its method and headers need nothing allowed.
	forced bool
}

// Preflight returns the preflight a browser sends before a CORS request
// from origin (the root's preflightOrigin; "" for none, which is nil) with a
// non-safelisted method or header, or nil. With force it ignores method
// and headers (a hop inheriting its chain's preflight).
func Preflight(req *http.Request, origin string, credentialed bool, h http.Header, force bool) *Need {
	if origin == "" {
		return nil
	}

	headers := unsafeHeaders(h)
	if !force && SafelistedMethod(req.Method) && len(headers) == 0 {
		return nil
	}

	u := *req.URL

	u.Fragment, u.RawFragment = "", ""

	return &Need{
		key: cacheKey{
			origin:       origin,
			url:          u.String(),
			credentialed: credentialed,
		},
		Method:  req.Method,
		Headers: headers,
		forced:  SafelistedMethod(req.Method) && len(headers) == 0,
	}
}

// CheckPreflight returns why a preflight's response does not allow the
// request (the Fetch standard's CORS-preflight fetch), or "".
func CheckPreflight(resp *http.Response, need *Need) string {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Sprintf("status %d is not ok", resp.StatusCode)
	}

	cred := need.key.credentialed
	if reason := Check(resp.Header, need.key.origin, cred); reason != "" {
		return reason
	}

	methods := headerList(resp.Header, "Access-Control-Allow-Methods")
	if !SafelistedMethod(need.Method) && !slices.Contains(methods, need.Method) && (cred || !slices.Contains(methods, "*")) {
		return fmt.Sprintf("Access-Control-Allow-Methods does not allow %s", need.Method)
	}

	names := headerList(resp.Header, "Access-Control-Allow-Headers")
	for i := range names {
		names[i] = strings.ToLower(names[i])
	}

	wildcard := !cred && slices.Contains(names, "*")
	for _, name := range need.Headers {
		if !slices.Contains(names, name) && (!wildcard || name == "authorization") {
			return fmt.Sprintf("Access-Control-Allow-Headers does not allow %s", name)
		}
	}

	return ""
}

// Check returns why a response's headers fail the Fetch standard's CORS
// check for a request from origin, with credentials or not, or "".
func Check(h http.Header, origin string, credentialed bool) string {
	acao := h.Values("Access-Control-Allow-Origin")
	switch {
	case len(acao) != 1:
		return "no single Access-Control-Allow-Origin"
	case acao[0] == "*" && credentialed:
		return "Access-Control-Allow-Origin * on a request with credentials"
	case acao[0] != "*" && acao[0] != origin:
		return fmt.Sprintf("Access-Control-Allow-Origin %q is not %q", acao[0], origin)
	}

	if credentialed && h.Get("Access-Control-Allow-Credentials") != "true" {
		return "no Access-Control-Allow-Credentials: true on a request with credentials"
	}

	return ""
}

// headerList splits a comma-separated response header's values.
func headerList(h http.Header, name string) []string {
	var out []string
	for _, v := range h.Values(name) {
		for item := range strings.SplitSeq(v, ",") {
			if item = strings.Trim(item, " \t"); item != "" {
				out = append(out, item)
			}
		}
	}

	return out
}

// PreflightHeaders returns the Config.Header (config) a CORS preflight
// gets: those its kind's header lists name, such as a User-Agent or
// Accept-Language override, so it goes as the request does; never the
// page's own, such as an API key, which a preflight never carries.
func PreflightHeaders(p *profile.Profile, config http.Header) http.Header {
	kind := p.Kinds[profile.KindPreflight]
	if kind == nil {
		return nil
	}

	lists := make([][]profile.Header, 0, 2+2*len(kind.Variants))

	lists = append(lists, kind.Headers, kind.HTTP1)

	for _, v := range kind.Variants {
		lists = append(lists, v.Headers, v.HTTP1)
	}

	out := http.Header{}
	for k, v := range config {
		for _, list := range lists {
			if slices.ContainsFunc(list, func(hd profile.Header) bool {
				return strings.EqualFold(hd.Name, k)
			}) {
				out[k] = v
				break
			}
		}
	}

	return out
}
