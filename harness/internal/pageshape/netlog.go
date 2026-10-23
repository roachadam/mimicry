package pageshape

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// netLog is the part of a Chrome net log (--log-net-log, capture mode
// IncludeSensitive for cookie sizes) that FromNetLog reads.
type netLog struct {
	Constants struct {
		LogEventTypes map[string]int `json:"logEventTypes"`
	} `json:"constants"`
	Events []struct {
		Time   string `json:"time"`
		Type   int    `json:"type"`
		Source struct {
			ID int `json:"id"`
		} `json:"source"`
		Params json.RawMessage `json:"params"`
	} `json:"events"`
}

// parseNetLog decodes a net log. Chrome writes one event per line and closes
// the array only on a clean exit; a log cut off by a killed browser is
// repaired by dropping the partial last event.
func parseNetLog(data []byte) (*netLog, error) {
	var l netLog
	err := json.Unmarshal(data, &l)
	if err == nil {
		return &l, nil
	}
	end := bytes.LastIndex(data, []byte("},\n"))
	if end < 0 {
		return nil, err
	}
	repaired := slices.Concat(data[:end+1], []byte("]}"))
	if err := json.Unmarshal(repaired, &l); err != nil {
		return nil, fmt.Errorf("net log: %w", err)
	}
	return &l, nil
}

// pending is a request being assembled from its events.
type pending struct {
	Request
	url       *url.URL
	authority string
	path      string
	session   int
	// from is the request this one continues after a redirect.
	from *pending
}

// FromNetLog extracts the shape of the page load of site (a registrable
// domain) from a Chrome net log: requests whose top frame is site, which
// leaves out Chrome's own traffic.
func FromNetLog(data []byte, site, browser, recorded string) (*Page, error) {
	l, err := parseNetLog(data)
	if err != nil {
		return nil, err
	}
	name := make(map[int]string, len(l.Constants.LogEventTypes))
	for k, v := range l.Constants.LogEventTypes {
		name[v] = k
	}
	b := &builder{
		page: &Page{
			Site:     site,
			Recorded: recorded,
			Browser:  browser,
		},
		hostIndex:   map[string]int{},
		current:     map[int]*pending{},
		sessionHost: map[int]string{},
		streams:     map[string][]int{},
		cookieIDs:   map[string]int{},
		lastResp:    map[string]*pending{},
	}
	topFrame := "https://" + site
	var navAt int64 = -1
	for _, e := range l.Events {
		var p map[string]json.RawMessage
		if len(e.Params) > 0 {
			if err := json.Unmarshal(e.Params, &p); err != nil {
				return nil, err
			}
		}
		at, _ := strconv.ParseInt(e.Time, 10, 64)
		sid := e.Source.ID
		switch name[e.Type] {
		case "HTTP2_SESSION", "QUIC_SESSION":
			if h := str(p["host"]); h != "" {
				host, _, _ := strings.Cut(h, ":")
				b.sessionHost[sid] = host
			}
		case "HTTP2_SESSION_SEND_HEADERS", "QUIC_CHROMIUM_CLIENT_STREAM_SEND_REQUEST_HEADERS":
			h := headerMap(lines(p["headers"]))
			key := h[":method"] + " " + h[":authority"] + h[":path"]
			b.streams[key] = append(b.streams[key], sid)
		case "URL_REQUEST_START_JOB":
			if p == nil {
				continue
			}
			nik := strings.Fields(str(p["network_isolation_key"]))
			u, err := url.Parse(str(p["url"]))
			// Chrome's own requests made for the page (autofill, for one)
			// have no initiating origin; navigations have none either but
			// are frame loads.
			kind := str(p["request_type"])
			byPage := str(p["initiator"]) != "not an origin" || kind == "main frame" || kind == "sub frame"
			if len(nik) == 0 || nik[0] != topFrame || !byPage || err != nil || (u.Scheme != "https" && u.Scheme != "http") {
				delete(b.current, sid)
				continue
			}
			if navAt < 0 {
				navAt = at
			}
			r := &pending{
				url: u,
				// A job restarting on the same request source follows a
				// redirect.
				from: b.current[sid],
			}
			r.Method = str(p["method"])
			r.At = int(at - navAt)
			r.Host = b.host(u.Hostname())
			r.RedirectTo = -1
			r.Initiator = -1
			if o, err := url.Parse(str(p["initiator"])); err == nil && o.Hostname() != "" {
				r.Initiator = b.host(o.Hostname())
			}
			r.PathLen = len(u.EscapedPath())
			if u.RawQuery != "" {
				r.QueryLen = len(u.RawQuery)
			}
			b.current[sid] = r
			b.requests = append(b.requests, r)
		case "HTTP_TRANSACTION_HTTP2_SEND_REQUEST_HEADERS", "HTTP_TRANSACTION_QUIC_SEND_REQUEST_HEADERS", "HTTP_TRANSACTION_SEND_REQUEST_HEADERS":
			r := b.current[sid]
			if r == nil || p == nil {
				continue
			}
			r.Protocol = map[string]string{
				"HTTP_TRANSACTION_HTTP2_SEND_REQUEST_HEADERS": "h2",
				"HTTP_TRANSACTION_QUIC_SEND_REQUEST_HEADERS":  "h3",
			}[name[e.Type]]
			if r.Protocol == "" {
				r.Protocol = "h1"
			}
			b.sent(r, lines(p["headers"]))
		case "HTTP_TRANSACTION_READ_RESPONSE_HEADERS":
			if r := b.current[sid]; r != nil && p != nil {
				b.received(r, lines(p["headers"]))
			}
		case "URL_REQUEST_JOB_FILTERED_BYTES_READ":
			if r := b.current[sid]; r != nil {
				n, _ := strconv.Atoi(string(p["byte_count"]))
				r.Size += n
			}
		case "URL_REQUEST_REDIRECTED":
			if r := b.current[sid]; r != nil {
				if u, err := url.Parse(str(p["location"])); err == nil {
					r.RedirectTo = b.host(r.url.ResolveReference(u).Hostname())
				}
			}
		}
	}
	return b.finish(), nil
}

type builder struct {
	page        *Page
	hostIndex   map[string]int
	requests    []*pending
	current     map[int]*pending
	sessionHost map[int]string
	// streams queues the session of each HTTP/2 or HTTP/3 request sent, by
	// "METHOD authority/path".
	streams map[string][]int
	// cookieIDs numbers cookies by "site name"; lastResp is each site's
	// latest response, where script cookies are set in replay.
	cookieIDs map[string]int
	lastResp  map[string]*pending
}

func (b *builder) host(name string) int {
	if i, ok := b.hostIndex[name]; ok {
		return i
	}
	i := len(b.page.Hosts)
	b.hostIndex[name] = i
	b.page.Hosts = append(b.page.Hosts, Host{
		Name: name,
		Site: siteOf(name),
	})
	return i
}

func siteOf(host string) string {
	s, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return s
}

func (b *builder) cookieID(site, name string) (int, bool) {
	key := site + " " + name
	id, ok := b.cookieIDs[key]
	if !ok {
		id = len(b.cookieIDs) + 1
		b.cookieIDs[key] = id
	}
	return id, ok
}

// browserHeaders are the headers a browser adds itself; any other header was
// set by the page.
var browserHeaders = map[string]bool{
	"host": true, "connection": true, "user-agent": true, "accept-encoding": true,
	"accept-language": true, "cookie": true, "referer": true, "origin": true,
	"priority": true, "upgrade-insecure-requests": true, "content-length": true,
	"cache-control": true, "pragma": true, "if-none-match": true, "if-modified-since": true,
	"range": true, "purpose": true, "x-client-data": true, "dnt": true, "te": true,
	"accept": true, "access-control-request-method": true, "access-control-request-headers": true,
}

func (b *builder) sent(r *pending, hs []string) {
	r.Headers = r.Headers[:0]
	r.CookieCrumbs, r.CookieBytes = 0, 0
	site := b.page.Hosts[r.Host].Site
	var accept string
	for _, line := range hs {
		name, value, ok := strings.Cut(line, ":")
		if !ok || name == "" {
			continue
		}
		name = strings.ToLower(name)
		value = strings.TrimSpace(value)
		switch {
		case name == ":authority":
			r.authority = value
		case name == ":path":
			r.path = value
		case name == "sec-fetch-dest":
			r.Dest = value
		case name == "sec-fetch-mode":
			r.Mode = value
		case name == "content-length":
			r.Body, _ = strconv.Atoi(value)
		case name == "cookie":
			r.CookieBytes += len(value)
			for crumb := range strings.SplitSeq(value, ";") {
				cn, cv, _ := strings.Cut(strings.TrimSpace(crumb), "=")
				r.CookieCrumbs++
				b.scriptCookie(site, cn, cv)
			}
		case name == "accept":
			accept = value
		case strings.HasPrefix(name, ":") || strings.HasPrefix(name, "sec-") || browserHeaders[name]:
		default:
			f := Field{
				Name:  name,
				Len:   len(value),
				Class: Classify(value),
			}
			if name == "content-type" {
				f.Value = value
			}
			r.Headers = append(r.Headers, f)
		}
	}
	// A fetch sends */* unless the page asks for something else.
	if r.Dest == "empty" && accept != "" && accept != "*/*" {
		r.Headers = append(r.Headers, Field{
			Name:  "accept",
			Len:   len(accept),
			Class: Classify(accept),
			Value: accept,
		})
	}
	if r.authority == "" {
		r.authority = r.url.Host
		r.path = r.url.RequestURI()
	}
}

// scriptCookie records a cookie sent without any Set-Cookie for it as set
// by script, on its site's latest response so far.
func (b *builder) scriptCookie(site, name, value string) {
	id, known := b.cookieID(site, name)
	if known {
		return
	}
	resp := b.lastResp[site]
	if resp == nil {
		b.page.ScriptCookiesDropped++
		return
	}
	resp.SetCookies = append(resp.SetCookies, Cookie{
		ID:       id,
		NameLen:  len(name),
		ValueLen: len(value),
		Class:    Classify(value),
		SiteWide: true,
		Path:     "/",
		Script:   true,
	})
}

func (b *builder) received(r *pending, hs []string) {
	if len(hs) == 0 {
		return
	}
	if _, code, ok := strings.Cut(hs[0], " "); ok {
		code, _, _ = strings.Cut(code, " ")
		r.Status, _ = strconv.Atoi(code)
	}
	site := b.page.Hosts[r.Host].Site
	for _, line := range hs[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(name) {
		case "content-type":
			r.ContentType, _, _ = mime.ParseMediaType(value)
		case "set-cookie":
			if c, ok := b.setCookie(site, value); ok {
				r.SetCookies = append(r.SetCookies, c)
			}
		}
	}
	b.lastResp[site] = r
}

func (b *builder) setCookie(site, header string) (Cookie, bool) {
	parts := strings.Split(header, ";")
	name, value, ok := strings.Cut(strings.TrimSpace(parts[0]), "=")
	if !ok || name == "" {
		return Cookie{}, false
	}
	value = strings.Trim(value, `"`)
	c := Cookie{
		NameLen:  len(name),
		ValueLen: len(value),
		Class:    Classify(value),
	}
	for _, attr := range parts[1:] {
		k, v, _ := strings.Cut(strings.TrimSpace(attr), "=")
		switch strings.ToLower(k) {
		case "domain":
			c.SiteWide = true
			if s := siteOf(strings.TrimPrefix(v, ".")); s != "" {
				site = s
			}
		case "path":
			c.Path = v
		case "secure":
			c.Secure = true
		case "httponly":
			c.HTTPOnly = true
		case "samesite":
			c.SameSite = strings.ToLower(v)
		case "max-age":
			c.Persistent = true
			if n, err := strconv.Atoi(v); err == nil && n <= 0 {
				c.Delete = true
			}
		case "expires":
			c.Persistent = true
			if strings.Contains(v, "1970") {
				c.Delete = true
			}
		}
	}
	c.ID, _ = b.cookieID(site, name)
	return c, true
}

func (b *builder) finish() *Page {
	sessions := map[int][]int{} // host index -> its sessions, in order
	index := map[*pending]int{}
	for _, r := range b.requests {
		if r.Protocol == "" {
			continue // never sent: cached, blocked or failed
		}
		if r.Protocol != "h1" {
			key := r.Method + " " + r.authority + r.path
			if q := b.streams[key]; len(q) > 0 {
				r.session, b.streams[key] = q[0], q[1:]
			}
		}
		if r.session != 0 {
			owner := r.Host
			if h, ok := b.sessionHost[r.session]; ok {
				owner = b.host(h)
			}
			if owner != r.Host {
				if !slices.Contains(b.page.Hosts[owner].Shared, r.Host) {
					b.page.Hosts[owner].Shared = append(b.page.Hosts[owner].Shared, r.Host)
				}
			} else {
				i := slices.Index(sessions[owner], r.session)
				if i < 0 {
					sessions[owner] = append(sessions[owner], r.session)
					i = len(sessions[owner]) - 1
				}
				r.Conn = i + 1
			}
		}
		r.RedirectFrom = -1
		if i, ok := index[r.from]; ok {
			r.RedirectFrom = i
		}
		index[r] = len(b.page.Requests)
		b.page.Requests = append(b.page.Requests, r.Request)
	}
	return b.page
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func lines(raw json.RawMessage) []string {
	var l []string
	_ = json.Unmarshal(raw, &l)
	return l
}

func headerMap(hs []string) map[string]string {
	m := make(map[string]string, len(hs))
	for _, h := range hs {
		// Pseudo-headers start with ':', so cut after the first character.
		if len(h) < 2 {
			continue
		}
		i := strings.Index(h[1:], ":")
		if i < 0 {
			continue
		}
		m[strings.ToLower(h[:i+1])] = strings.TrimSpace(h[i+2:])
	}
	return m
}

// Classify names the character class of a value, which decides its Huffman
// length: "digits", "hex", "alnum", "base64" (with +/=), "base64url" (with
// -_), "urlencoded" (with %), or "text".
func Classify(s string) string {
	digits, hex, alnum := true, true, true
	var b64, b64url, pct, other bool
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F':
			digits = false
		case c >= 'g' && c <= 'z' || c >= 'G' && c <= 'Z':
			digits, hex = false, false
		case c == '+' || c == '/' || c == '=':
			digits, hex, alnum, b64 = false, false, false, true
		case c == '-' || c == '_':
			digits, hex, alnum, b64url = false, false, false, true
		case c == '%':
			digits, hex, alnum, pct = false, false, false, true
		default:
			digits, hex, alnum, other = false, false, false, true
		}
	}
	switch {
	case s == "":
		return "empty"
	case digits:
		return "digits"
	case hex:
		return "hex"
	case alnum:
		return "alnum"
	case other:
		return "text"
	case pct:
		return "urlencoded"
	case b64url && !b64:
		return "base64url"
	default:
		return "base64"
	}
}
