package mimicry

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/headerorder"
	"github.com/roachadam/mimicry/internal/pool"
	"github.com/roachadam/mimicry/profile"
)

// CORSError is a request the browser would not send: its CORS preflight's
// answer does not allow it, or it follows a redirect that fails the CORS
// check (Redirect). RoundTrip did not send it (Request.IgnoreCORS sends it
// anyway).
type CORSError struct {
	// URL is the refused request's: the preflighted URL, or the one that
	// answered with the redirect.
	URL *url.URL
	// Redirect is true when the refused response is URL's redirect, not
	// its preflight's answer.
	Redirect bool
	// Reason is the CORS rule the response breaks.
	Reason string
	// Status and Header are the refused response's.
	Status int
	Header http.Header
}

func (e *CORSError) Error() string {
	if e.Redirect {
		return fmt.Sprintf("mimicry: CORS check of the %d redirect from %s refused it: %s", e.Status, e.URL.Redacted(), e.Reason)
	}

	return fmt.Sprintf("mimicry: CORS preflight for %s refused the request: %s", e.URL.Redacted(), e.Reason)
}

// defaultPreflightAge is how long a preflight answered without
// Access-Control-Max-Age is remembered: the Fetch standard's 5 seconds.
// Every browser's cors capture agrees: the same request at once is not
// preflighted, after 6 seconds it is.
const defaultPreflightAge = 5 * time.Second

// preflightNeed is what a request's preflight asks for it.
type preflightNeed struct {
	key    preflightKey
	method string
	// headers are the request's CORS-unsafe header names, lowercase,
	// sorted, once each: Access-Control-Request-Headers.
	headers []string
	// forced: only inheritance asks for it (hopPreflight), its method
	// and headers need nothing allowed.
	forced bool
}

// needsPreflight returns the preflight a browser sends before a
// cross-origin CORS fetch() or XHR with a non-safelisted method or header,
// or nil. With force it ignores method and headers (a hop inheriting its
// chain's preflight).
func needsPreflight(req *http.Request, r Request, from *url.URL, credentialed bool, h http.Header, force bool) *preflightNeed {
	if from == nil || (r.Kind != Fetch && r.Kind != XHR) || r.NoCORS {
		return nil
	}

	o := origin(from)
	if isTainted(from, append(redirectsBefore(req), req.URL)) {
		o = "null"
	}

	if o == origin(req.URL) {
		return nil
	}

	headers := unsafeHeaders(h)
	if !force && safelistedMethod(req.Method) && len(headers) == 0 {
		return nil
	}

	u := *req.URL

	u.Fragment, u.RawFragment = "", ""

	return &preflightNeed{
		key: preflightKey{
			origin:       o,
			url:          u.String(),
			credentialed: credentialed,
		},
		method:  req.Method,
		headers: headers,
		forced:  safelistedMethod(req.Method) && len(headers) == 0,
	}
}

// preflightSend is a CORS preflight to send before a request: what it
// asks for, the preflight request ready for the engine, how it was
// resolved, and the profile's cache rule.
type preflightSend struct {
	need *preflightNeed
	req  *http.Request
	s    *sending
	rule string
}

// preflightFor returns req's CORS preflight unless none is needed or a
// cached answer allows req, and whether one was needed at all. It changes
// nothing; RoundTrip sends pf, Plan only plans it.
func (t *Transport) preflightFor(req *http.Request, r Request, from *url.URL, credentialed bool, h http.Header) (needed bool, pf *preflightSend, err error) {
	need := needsPreflight(req, r, from, credentialed, h, false)
	if need == nil {
		forced := needsPreflight(req, r, from, credentialed, h, true)
		inherit, err := t.hopPreflight(req, forced != nil)

		if err != nil || !inherit {
			return false, nil, err
		}

		need = forced
	}

	var rule string
	if t.profile.Context != nil {
		rule = t.profile.Context.PreflightCache
	}

	if rule == "" {
		return true, nil, fmt.Errorf("%w: profile %s has no CORS preflight cache rule", ErrNotMeasured, t.profile.Name)
	}

	if t.preflights.allows(need, t.clock.Now()) {
		return true, nil, nil
	}

	ctx := req.Context()
	// The same URL and redirect chain (Response, which decides the
	// Origin), with nothing of the request's own.
	pre := req.Clone(ctx)

	pre.Method = http.MethodOptions
	pre.Header = http.Header{}
	pre.Body, pre.GetBody, pre.ContentLength = http.NoBody, nil, 0
	pre.TransferEncoding, pre.Trailer = nil, nil
	pre.Header.Set("Access-Control-Request-Method", need.method)

	if len(need.headers) > 0 {
		pre.Header.Set("Access-Control-Request-Headers", strings.Join(need.headers, ","))
	}

	s, err := resolve(t.profile, pre, Request{
		Kind: kindPreflight,
		From: from,
	}, from, t.preflightHeaders)
	if err != nil {
		return true, nil, err
	}

	pctx := pool.WithPartition(ctx, s.partition)
	if s.plan != nil {
		pctx = headerorder.WithPlan(pctx, s.plan)
	}

	pre = pre.WithContext(pctx)
	pre.Header = s.header

	return true, &preflightSend{
		need: need,
		req:  pre,
		s:    s,
		rule: rule,
	}, nil
}

// sendPreflight sends pf and checks its answer: a *CORSError when it
// does not allow the request, unless ignore (Request.IgnoreCORS); a
// successful answer is cached.
func (t *Transport) sendPreflight(pf *preflightSend, ignore bool) error {
	resp, err := t.engine.RoundTrip(pf.req)
	if err != nil {
		return publicError(err)
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()

	if reason := checkPreflight(resp, pf.need); reason != "" {
		if ignore {
			return nil
		}

		return &CORSError{
			URL:    pf.req.URL,
			Reason: reason,
			Status: resp.StatusCode,
			Header: resp.Header,
		}
	}

	t.preflights.store(pf.need, resp.Header, pf.rule == profile.PreflightCacheReplace, t.clock.Now())

	return nil
}

// hopPreflight reports whether a redirect hop that could be preflighted
// is, because the request before it was (redirects.hopPreflight inherit:
// Firefox and Safari, not Chrome). Without the rule it is not measured.
func (t *Transport) hopPreflight(req *http.Request, could bool) (bool, error) {
	if !could || req.Response == nil || req.Response.Request == nil {
		return false, nil
	}

	if before, _ := req.Response.Request.Context().Value(preflightedKey{}).(bool); !before {
		return false, nil
	}

	if t.profile.Redirects == nil {
		return false, fmt.Errorf("%w: profile %s has no redirects rules, for a hop after a CORS preflight", ErrNotMeasured, t.profile.Name)
	}

	return t.profile.Redirects.HopPreflight == profile.RedirectPreflightInherit, nil
}

// preflightedKey marks a request sent after a CORS preflight it needed, in
// its context, which a redirect's next hop sees as req.Response.Request.
type preflightedKey struct{}

// checkPreflight returns why a preflight's response does not allow the
// request (the Fetch standard's CORS-preflight fetch), or "".
func checkPreflight(resp *http.Response, need *preflightNeed) string {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Sprintf("status %d is not ok", resp.StatusCode)
	}

	cred := need.key.credentialed
	if reason := checkCORS(resp.Header, need.key.origin, cred); reason != "" {
		return reason
	}

	methods := headerList(resp.Header, "Access-Control-Allow-Methods")
	if !safelistedMethod(need.method) && !slices.Contains(methods, need.method) && (cred || !slices.Contains(methods, "*")) {
		return fmt.Sprintf("Access-Control-Allow-Methods does not allow %s", need.method)
	}

	names := headerList(resp.Header, "Access-Control-Allow-Headers")
	for i := range names {
		names[i] = strings.ToLower(names[i])
	}

	wildcard := !cred && slices.Contains(names, "*")
	for _, name := range need.headers {
		if !slices.Contains(names, name) && (!wildcard || name == "authorization") {
			return fmt.Sprintf("Access-Control-Allow-Headers does not allow %s", name)
		}
	}

	return ""
}

// checkCORS returns why a response's headers fail the Fetch standard's CORS
// check for a request from origin, with credentials or not, or "".
func checkCORS(h http.Header, origin string, credentialed bool) string {
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

// preflightKey is what a preflight cache entry is for: the Fetch
// standard's key (the request's origin, URL and credentials mode).
type preflightKey struct {
	origin, url  string
	credentialed bool
}

// preflightCache remembers successful preflights: per key, until when each
// method ("m:PUT") and header name ("h:x-a") is allowed.
type preflightCache struct {
	mu      sync.Mutex
	entries map[preflightKey]map[string]time.Time
	// pruneAt is the number of entries at which store next drops what has
	// expired.
	pruneAt int
}

// minPrune is the fewest entries pruning waits for.
const minPrune = 64

// allows reports whether a cached answer allows need's request at now.
func (c *preflightCache) allows(need *preflightNeed, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	items := c.entries[need.key]
	has := func(item string) bool {
		until, ok := items[item]
		return ok && now.Before(until)
	}
	cred := need.key.credentialed

	if need.forced && !slices.ContainsFunc(slices.Collect(maps.Keys(items)), has) {
		// Nothing in the method and headers needs allowing: only an
		// earlier answer for the URL, still live, spares this one.
		return false
	}

	if !safelistedMethod(need.method) && !has("m:"+need.method) && (cred || !has("m:*")) {
		return false
	}

	for _, name := range need.headers {
		if !has("h:"+name) && (cred || name == "authorization" || !has("h:*")) {
			return false
		}
	}

	return true
}

// store remembers what a successful preflight's response h allows, for its
// Access-Control-Max-Age (else defaultPreflightAge): replacing the key's
// entry (Chrome, Safari) or adding to it (Firefox).
func (c *preflightCache) store(need *preflightNeed, h http.Header, replace bool, now time.Time) {
	age := defaultPreflightAge
	if v := strings.TrimSpace(h.Get("Access-Control-Max-Age")); v != "" {
		if secs, err := strconv.ParseUint(v, 10, 32); err == nil {
			age = time.Duration(secs) * time.Second
		}
	}

	until := now.Add(age)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = map[preflightKey]map[string]time.Time{}
	}

	// Drop what has expired once the cache has doubled since it last did,
	// so it stays within twice its live entries at an amortised constant
	// cost per store. (Browsers cap their caches too; by how much is not
	// measured.)
	if len(c.entries) >= c.pruneAt {
		for key, items := range c.entries {
			for item, at := range items {
				if !now.Before(at) {
					delete(items, item)
				}
			}

			if len(items) == 0 {
				delete(c.entries, key)
			}
		}

		c.pruneAt = max(2*len(c.entries), minPrune)
	}

	items := c.entries[need.key]
	if items == nil || replace {
		items = map[string]time.Time{}
	}

	for _, m := range headerList(h, "Access-Control-Allow-Methods") {
		items["m:"+m] = until
	}

	for _, name := range headerList(h, "Access-Control-Allow-Headers") {
		items["h:"+strings.ToLower(name)] = until
	}

	// Max-Age 0 stores items already expired: under replace, the answer
	// still displaces the URL's earlier entry.
	c.entries[need.key] = items
}

// safelistedMethod reports a CORS-safelisted method: one a request may use
// without a preflight.
func safelistedMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodPost
}

// unsafeHeaders returns h's CORS-unsafe header names (Fetch standard),
// lowercase, sorted and unique. Forbidden headers and User-Agent (which
// Chrome still forbids) are never among them. The 1024-byte safelist
// limit is unreachable here.
func unsafeHeaders(h http.Header) []string {
	var unsafe []string
	for name, values := range h {
		if len(values) == 0 {
			continue // a suppressed profile header
		}

		lower := strings.ToLower(name)
		if forbiddenHeader(lower) || lower == strings.ToLower(HeaderOrderKey) {
			continue
		}

		if !safelistedHeader(lower, strings.Join(values, ", ")) {
			unsafe = append(unsafe, lower)
		}
	}

	slices.Sort(unsafe)

	return slices.Compact(unsafe)
}

// forbiddenHeader reports a forbidden request-header name (Fetch
// standard), plus user-agent: headers only the browser sets.
func forbiddenHeader(lower string) bool {
	switch lower {
	case "accept-charset", "accept-encoding", "access-control-request-headers",
		"access-control-request-method", "connection", "content-length", "cookie",
		"cookie2", "date", "dnt", "expect", "host", "keep-alive", "origin", "referer",
		"set-cookie", "te", "trailer", "transfer-encoding", "upgrade", "via", "user-agent":
		return true
	}

	return strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "sec-")
}

// safelistedHeader reports a CORS-safelisted request-header (Fetch
// standard): a name and value a page may send without a preflight.
func safelistedHeader(lower, value string) bool {
	if len(value) > 128 {
		return false
	}

	switch lower {
	case "accept":
		return !strings.ContainsFunc(value, corsUnsafeByte)
	case "accept-language", "content-language":
		return !strings.ContainsFunc(value, func(c rune) bool {
			return !languageByte(c)
		})

	case "content-type":
		if strings.ContainsFunc(value, corsUnsafeByte) {
			return false
		}

		switch mimeEssence(value) {
		case "application/x-www-form-urlencoded", "multipart/form-data", "text/plain":
			return true
		}

		return false

	case "range":
		return simpleRange(value)
	}

	return false
}

// preflightConfig returns the Config.Headers (config) a CORS preflight
// gets: those its kind's header lists name, such as a User-Agent or
// Accept-Language override, so it goes as the request does; never the
// page's own, such as an API key, which a preflight never carries.
func preflightConfig(p *profile.Profile, config http.Header) http.Header {
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

// corsUnsafeByte reports a CORS-unsafe request-header byte.
func corsUnsafeByte(c rune) bool {
	return c < 0x20 && c != '\t' || c == 0x7f || strings.ContainsRune("\"():<>?@[\\]{}", c)
}

// mimeEssence returns a MIME type's essence (type/subtype, lowercase), as
// the Fetch standard's "parse a MIME type" does, or "" when it fails.
// Parameters cannot make it fail: the standard skips invalid ones.
func mimeEssence(value string) string {
	value = strings.Trim(value, " \t\r\n")

	typ, rest, ok := strings.Cut(value, "/")

	if !ok {
		return ""
	}

	sub, _, _ := strings.Cut(rest, ";")

	sub = strings.TrimRight(sub, " \t\r\n")

	if !token(typ) || !token(sub) {
		return ""
	}

	return strings.ToLower(typ + "/" + sub)
}

// token reports a non-empty string of HTTP token code points.
func token(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(c rune) bool {
		return (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && !strings.ContainsRune("!#$%&'*+-.^_`|~", c)
	})
}

// languageByte reports a byte the Fetch standard allows in a safelisted
// Accept-Language or Content-Language value.
func languageByte(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || strings.ContainsRune(" *,-.;=", c)
}

// simpleRange reports a Range value of the form bytes=N- or bytes=N-M
// (N <= M): the only ones the Fetch standard safelists.
func simpleRange(value string) bool {
	spec, ok := strings.CutPrefix(value, "bytes=")
	if !ok {
		return false
	}

	start, end, ok := strings.Cut(spec, "-")
	if !ok {
		return false
	}

	// ParseUint takes digits only: no sign, no space, not empty.
	a, err := strconv.ParseUint(start, 10, 64)
	if err != nil {
		return false
	}

	if end == "" {
		return true
	}

	b, err := strconv.ParseUint(end, 10, 64)

	return err == nil && a <= b
}
