package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Profile describes one browser version on one platform.
type Profile struct {
	// Name identifies the profile, for example "chrome-155-macos".
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
	// in canonical casing after Host and Connection.
	HTTP1 *HTTP1 `json:"http1,omitempty"`
	// Variants are the orders the navigation takes otherwise (a form POST),
	// as Kind.Variants.
	Variants []Variant `json:"variants,omitempty"`
	// Kinds describe the browser's other requests, by kind (KindFetch, ...).
	Kinds map[string]*Kind `json:"kinds,omitempty"`
	// Context is how the browser fills what depends on which page makes a
	// request. Nil: as the Fetch standard says, with no browser extras.
	Context *Context `json:"context,omitempty"`
	// CookieOrder is the order of cookies in the Cookie header:
	// CookieOrderCreation (empty; Chrome, Firefox: RFC 6265 order, as Go's
	// cookie jar produces) or CookieOrderName (Safari: sorted by name).
	CookieOrder string `json:"cookie_order,omitempty"`
}

// HTTP1 describes a browser's HTTP/1.1 requests.
type HTTP1 struct {
	// Headers of a navigation request, in order, named exactly as sent
	// ("User-Agent", "sec-ch-ua"). Host takes the request's host, and
	// Content-Length the body's length; values work as in Profile.Headers.
	Headers []Header `json:"headers"`
}

// Cookie orders.
const (
	CookieOrderCreation = ""
	CookieOrderName     = "name"
)

// Parse decodes a profile from JSON and validates it. Unknown fields and
// trailing data are errors, so a typo cannot silently drop a setting.
func Parse(data []byte) (*Profile, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Profile
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("profile: trailing data after the profile")
	}
	if err := p.Validate(); err != nil {
		return nil, err
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
