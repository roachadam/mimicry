// Package profile defines a browser profile, split by concern (TLS, HTTP/2,
// HTTP/1.1, headers by request kind, cookies, proxies, redirects, connection
// sharing, WebSocket, QUIC, HTTP/3), and loads and validates it from JSON, the format the harness
// writes browser captures in. It imports only the standard library.
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
	// Deprecated marks a built-in profile older than the newest five
	// versions of its browser on its platform. See decision 0035.
	Deprecated bool `json:"deprecated,omitempty"`
	// CapturesFrom names, per capture scenario ("cookies", "h3-page"), the
	// earlier profile whose captures the harness checks this one against,
	// because the scenario was not recaptured. Only the harness reads it.
	// See decision 0035.
	CapturesFrom map[string]string `json:"capturesFrom,omitempty"`

	TLS TLS `json:"tls"`
	// HTTP2 is required when the profile offers h2 in ALPN.
	HTTP2 *HTTP2 `json:"http2,omitempty"`
	// Headers of a top-level navigation request over HTTP/2, in order: the
	// profile's base request (KindNavigate in the client).
	Headers []Header `json:"headers"`
	// HTTP1 describes the same request over HTTP/1.1. Nil writes Headers in
	// canonical casing after Host and Connection; set, every kind needs its
	// HTTP/1.1 list too.
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
	// Redirects is how the browser follows redirects where browsers
	// differ. Nil: not measured, so a redirect that changes a request's
	// method is an error.
	Redirects *Redirects `json:"redirects,omitempty"`
	// Connections are how the browser shares connections: its HTTP/1.1
	// connections per host and HTTP/2 coalescing. Required.
	Connections *Connections `json:"connections,omitempty"`
	// WebSocket is how the browser speaks on WebSockets. Nil: not measured,
	// so opening a WebSocket is an error.
	WebSocket *WebSocket `json:"websocket,omitempty"`
	// QUIC and HTTP3 are how the browser speaks HTTP/3; TLS.QUIC is its
	// ClientHello. Both or neither. Nil: not measured, so HTTP/3 is never
	// used.
	QUIC  *QUIC  `json:"quic,omitempty"`
	HTTP3 *HTTP3 `json:"http3,omitempty"`
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

// Decode decodes a profile from JSON without validating it, for harness
// captures, which lack what a complete profile needs. Unknown, miscased and
// repeated keys and trailing data are errors.
func Decode(data []byte) (*Profile, error) {
	var p Profile
	if err := jsonv2.Unmarshal(data, &p, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	return &p, nil
}

// Encode writes p as indented JSON with a trailing newline. It does not
// validate p, so captures of unusual browser behaviour can be recorded.
func Encode(p *Profile) ([]byte, error) {
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	return append(out, '\n'), nil
}
