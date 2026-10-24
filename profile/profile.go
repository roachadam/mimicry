package profile

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
)

// Profile describes one browser version on one platform.
type Profile struct {
	// Name identifies the profile, for example "chrome-155-macos":
	// lowercase letters and digits in parts joined by dashes.
	Name     string `json:"name"`
	Browser  string `json:"browser"`
	Version  string `json:"version"`
	Platform string `json:"platform"`

	TLS TLS `json:"tls"`
	// HTTP2 is required when the profile offers h2 in ALPN.
	HTTP2 *HTTP2 `json:"http2,omitempty"`
	// Headers of a top-level navigation request over HTTP/2, in order: the
	// profile's base request (KindNavigate in the client).
	Headers []Header `json:"headers"`
	// HTTP1 describes the same request over HTTP/1.1, which browsers send
	// with a header set, order and casing of their own. Nil writes Headers
	// in canonical casing after Host and Connection. Set, every kind needs
	// its HTTP/1.1 list too.
	HTTP1 *HTTP1 `json:"http1,omitempty"`
	// Variants are the orders the navigation takes otherwise (a form POST),
	// as Kind.Variants.
	Variants []Variant `json:"variants,omitempty"`
	// Kinds describe the browser's other requests, by kind (KindFetch, ...).
	Kinds map[string]*Kind `json:"kinds,omitempty"`
	// Context is how the browser fills what depends on which page makes a
	// request. Nil: as the Fetch standard says, with no browser extras.
	Context *Context `json:"context,omitempty"`
	// Cookies are the browser's cookie rules. Required.
	Cookies *Cookies `json:"cookies,omitempty"`
	// Proxy is how the browser talks to HTTP and SOCKS5 proxies. Nil: not
	// measured, so sending through a proxy is an error.
	Proxy *Proxy `json:"proxy,omitempty"`
}

// HTTP1 describes a browser's HTTP/1.1 requests.
type HTTP1 struct {
	// Headers of a navigation request, in order, named exactly as sent
	// ("User-Agent", "sec-ch-ua"). Host takes the request's host, and
	// Content-Length the body's length; values work as in Profile.Headers.
	Headers []Header `json:"headers"`
}

// Parse decodes a profile from JSON, as Decode, and validates it.
func Parse(data []byte) (*Profile, error) {
	p, err := Decode(data)
	if err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// Decode decodes a profile from JSON without validating it, as Encode
// writes one: for harness captures, which record one request and lack what
// a complete profile needs. Unknown, miscased and repeated keys and trailing
// data are errors, so a typo cannot silently drop a setting.
func Decode(data []byte) (*Profile, error) {
	var p Profile
	if err := jsonv2.Unmarshal(data, &p, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	return &p, nil
}

// Encode writes p as indented JSON with a trailing newline, the format of the
// built-in profiles and of harness captures. It does not validate p, so that a
// capture of unusual browser behaviour can still be recorded.
func Encode(p *Profile) ([]byte, error) {
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	return append(out, '\n'), nil
}
