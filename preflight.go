package mimicry

import (
	"fmt"
	"io"
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

// CORSError is a request whose CORS preflight's answer does not allow it:
// the browser would not send it, and RoundTrip did not
// (Request.IgnoreCORS sends it anyway).
type CORSError struct {
	URL *url.URL
	// Reason is the CORS rule the answer breaks.
	Reason string
	// Status and Header are the preflight's response.
	Status int
	Header http.Header
}

func (e *CORSError) Error() string {
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
}

// needsPreflight returns the preflight a browser sends before req, header
// set h (the page's headers), from page from, or nil when it sends none:
// a fetch() in CORS mode or an XHR to another origin than the request's,
// with a method other than GET, HEAD and POST or a header the Fetch
// standard does not safelist.
func needsPreflight(req *http.Request, r Request, from *url.URL, credentialed bool, h http.Header) *preflightNeed {
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
	if safelistedMethod(req.Method) && len(headers) == 0 {
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
	}
}

// preflight sends req's CORS preflight when the profile's browser would
// (needsPreflight) and its answer is not cached, and checks the answer: a
// *CORSError when it does not allow req, unless r.IgnoreCORS.
func (t *Transport) preflight(req *http.Request, r Request, from *url.URL, credentialed bool, h http.Header) error {
	need := needsPreflight(req, r, from, credentialed, h)
	if need == nil {
		return nil
	}
	var rule string
	if t.profile.Context != nil {
		rule = t.profile.Context.PreflightCache
	}
	if rule == "" {
		return fmt.Errorf("%w: profile %s has no CORS preflight cache rule", ErrNotMeasured, t.profile.Name)
	}
	if t.preflights.allows(need, t.clock.Now()) {
		return nil
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
		return err
	}
	pctx := pool.WithPartition(ctx, s.partition)
	if s.plan != nil {
		pctx = headerorder.WithPlan(pctx, s.plan)
	}
	pre = pre.WithContext(pctx)
	pre.Header = s.header
	resp, err := t.engine.RoundTrip(pre)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if reason := checkPreflight(resp, need); reason != "" {
		if r.IgnoreCORS {
			return nil
		}
		return &CORSError{
			URL:    req.URL,
			Reason: reason,
			Status: resp.StatusCode,
			Header: resp.Header,
		}
	}
	t.preflights.store(need, resp.Header, rule == profile.PreflightCacheReplace, t.clock.Now())
	return nil
}

// checkPreflight returns why a preflight's response does not allow the
// request (the Fetch standard's CORS-preflight fetch), or "".
func checkPreflight(resp *http.Response, need *preflightNeed) string {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Sprintf("status %d is not ok", resp.StatusCode)
	}
	cred := need.key.credentialed
	acao := resp.Header.Values("Access-Control-Allow-Origin")
	switch {
	case len(acao) != 1:
		return "no single Access-Control-Allow-Origin"
	case acao[0] == "*" && cred:
		return "Access-Control-Allow-Origin * on a request with credentials"
	case acao[0] != "*" && acao[0] != need.key.origin:
		return fmt.Sprintf("Access-Control-Allow-Origin %q is not %q", acao[0], need.key.origin)
	}
	if cred && resp.Header.Get("Access-Control-Allow-Credentials") != "true" {
		return "no Access-Control-Allow-Credentials: true on a request with credentials"
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
	// expired; scanned counts the entries pruning has looked at.
	pruneAt int
	scanned int
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
			c.scanned++
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

// unsafeHeaders returns h's CORS-unsafe request-header names (Fetch
// standard), lowercase, sorted, once each: headers the page set that a
// preflight must allow. Forbidden headers, which a page cannot set, are
// never among them, nor is User-Agent, which Chrome still forbids. (The
// standard's limit of 1024 bytes on safelisted values cannot be reached:
// five names of at most 128 bytes each.)
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
