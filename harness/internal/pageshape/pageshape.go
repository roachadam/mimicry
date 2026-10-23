// Package pageshape records the shape of a real page load, without its
// content, so the capture server can replay it with synthetic values: which
// hosts a page talks to, what kinds of requests it makes and when, and how
// many cookies of what size it collects. Lengths and character classes are
// kept because they decide how browsers encode headers (HPACK, Huffman).
package pageshape

import (
	"encoding/json"
	"os"
)

// Page is one recorded page load.
type Page struct {
	// Site is the visited registrable domain ("nike.com").
	Site string `json:"site"`
	// Recorded is the date of the recording (YYYY-MM-DD) and Browser the
	// browser that made it ("chrome-155-macos").
	Recorded string `json:"recorded"`
	Browser  string `json:"browser"`
	// Hosts are the hostnames the page reached, real names kept: replay maps
	// them under localhost (ReplayHost).
	Hosts []Host `json:"hosts"`
	// Requests in the order the browser started them; the first is the
	// navigation.
	Requests []Request `json:"requests"`
	// ScriptCookiesDropped counts cookies the page set from script that
	// replay cannot reproduce: no earlier response of their site to set
	// them on.
	ScriptCookiesDropped int `json:"script_cookies_dropped,omitempty"`
}

// Host is one hostname of the page.
type Host struct {
	Name string `json:"name"`
	// Site is its registrable domain.
	Site string `json:"site"`
	// Shared lists the other hosts (indices) whose requests the browser sent
	// on one of this host's connections: connection coalescing.
	Shared []int `json:"shared,omitempty"`
}

// Request is one request of the page and its response.
type Request struct {
	// Host indexes Page.Hosts.
	Host int `json:"host"`
	// At is when it started, in milliseconds after the navigation.
	At int `json:"at_ms"`
	// Method, and the Fetch metadata the browser sent with it.
	Method string `json:"method"`
	Dest   string `json:"dest,omitempty"`
	Mode   string `json:"mode,omitempty"`
	// Protocol is the one it went over: "h1", "h2" or "h3".
	Protocol string `json:"protocol"`
	// Conn numbers its connection among the host's connections, from 1;
	// 0 when the log does not tell.
	Conn int `json:"conn,omitempty"`
	// PathLen and QueryLen are the lengths of the path and of the query
	// (after "?", 0 without one).
	PathLen  int `json:"path_len"`
	QueryLen int `json:"query_len,omitempty"`
	// Headers are the headers the page set itself (not ones the browser
	// adds), in the order sent.
	Headers []Field `json:"headers,omitempty"`
	// Body is the request body's size in bytes.
	Body int `json:"body,omitempty"`
	// CookieCrumbs is how many cookies the request carried, CookieBytes the
	// length of its cookie header(s).
	CookieCrumbs int `json:"cookie_crumbs,omitempty"`
	CookieBytes  int `json:"cookie_bytes,omitempty"`

	// Status is the response status; 0 if none arrived.
	Status int `json:"status"`
	// RedirectTo indexes the host a redirect pointed at (-1 otherwise).
	RedirectTo int `json:"redirect_to"`
	// RedirectFrom indexes the request whose redirect led here (-1
	// otherwise): browsers follow it themselves, so replay does not
	// schedule it.
	RedirectFrom int `json:"redirect_from"`
	// Initiator indexes the host whose document made the request (-1 for
	// the top-level navigation): requests from a third-party frame are
	// replayed from a frame of that host.
	Initiator int `json:"initiator"`
	// ContentType is the response's media type, without parameters.
	ContentType string `json:"content_type,omitempty"`
	// Size is the response body's size in bytes, decoded.
	Size int `json:"size"`
	// SetCookies are the cookies set on this response.
	SetCookies []Cookie `json:"set_cookies,omitempty"`
}

// Field is a header's shape: its real name, and its value's length and
// character class. Value is kept only for headers whose value is not
// personal and changes what the browser does (content-type and accept
// decide whether a cross-origin fetch needs a preflight).
type Field struct {
	Name  string `json:"name"`
	Len   int    `json:"len"`
	Class string `json:"class"`
	Value string `json:"value,omitempty"`
}

// Cookie is the shape of a cookie being set.
type Cookie struct {
	// ID identifies the cookie across the page: the same ID set again
	// replaces it.
	ID       int    `json:"id"`
	NameLen  int    `json:"name_len"`
	ValueLen int    `json:"value_len"`
	Class    string `json:"class"`
	// SiteWide is set when the cookie has a Domain attribute, covering
	// every host of its site.
	SiteWide   bool   `json:"site_wide,omitempty"`
	Path       string `json:"path,omitempty"`
	Secure     bool   `json:"secure,omitempty"`
	HTTPOnly   bool   `json:"http_only,omitempty"`
	SameSite   string `json:"same_site,omitempty"`
	Persistent bool   `json:"persistent,omitempty"`
	// Delete is set when the cookie is being removed (expired, Max-Age 0).
	Delete bool `json:"delete,omitempty"`
	// Script marks a cookie the page set from script (document.cookie):
	// it was sent without any Set-Cookie for it. Replay sets it on this
	// response instead, the site's last before the cookie was first sent.
	Script bool `json:"script,omitempty"`
}

// Load reads a page shape written by Write.
func Load(path string) (*Page, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Page
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Write stores p as indented JSON.
func Write(path string, p *Page) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
