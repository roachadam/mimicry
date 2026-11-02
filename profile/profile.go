// Package profile defines a browser profile. A Profile is one checked and
// ready to use: a small read-only value, safe to copy and to share between
// clients, that mimicry.Config takes. A Spec is its data, split by concern
// (TLS, HTTP/2, HTTP/1.1, headers by request kind, cookies, proxies,
// redirects, connection sharing, WebSocket, QUIC, HTTP/3), to read or to
// edit through Profile.With. The package loads and validates a profile
// from JSON, the format the harness writes browser captures in. It imports
// only the standard library, and internal/checked, through which the root
// package reads the Spec a Profile holds.
package profile

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/roachadam/mimicry/internal/checked"
)

// Profile is a checked browser profile, ready to use: a small value with no
// exported fields, safe to copy and to share between clients, which
// nothing can change. The built-in profiles are in package profiles; Parse
// reads one from JSON; With makes an edited copy. The zero Profile is no
// profile.
//
// Profiles of equal content share one checked copy of it, so building a
// client from a Profile copies and checks nothing: build each profile
// once, and reuse it for every client.
type Profile struct {
	c *compiled
}

// compiled is what a Profile holds: its Spec, validated and read-only,
// shared by every Profile of equal content; or, for an edit that left it
// invalid, the edited Spec and the problems found, never shared. A client
// holds the Spec, which keeps its compiled.
type compiled struct {
	spec Spec
	err  error
}

// Name is the profile's name, such as "chrome-156-macos"; "" for the zero
// Profile.
func (p Profile) Name() string {
	if p.c == nil {
		return ""
	}

	return p.c.spec.Name
}

// UserAgent is the User-Agent header the profile's navigation sends; ""
// for the zero Profile.
func (p Profile) UserAgent() string {
	if p.c == nil {
		return ""
	}

	for _, h := range p.c.spec.Headers {
		if h.Name == "user-agent" {
			return h.Value
		}
	}

	return ""
}

// Spec returns a deep copy of the profile's data, for reading what the
// profile sends, saving it with Encode, or a value Name and UserAgent don't
// cover (a standalone cookie jar's Cookies rules, say). Changing the copy
// changes no Profile; to edit a profile, use With. A copy costs as much as
// With, so read it once rather than per request; requests never need it.
// The zero Profile's is empty.
func (p Profile) Spec() *Spec {
	s := new(Spec)
	if p.c != nil {
		copySpec(s, &p.c.spec)
	}

	return s
}

// With returns a copy of p with edit applied to a copy of its data,
// checked once. With never fails: when the edited data is invalid, the
// Profile it returns carries the problems (a *ValidationError), and
// building a client from it returns them. Its Spec is then the edited data,
// so a further With can fix it. On the zero Profile, edit starts from an
// empty Spec.
//
// With reads the whole profile, a fraction of a millisecond; a client
// built from its result copies nothing. Edit once and reuse the result:
// what differs between the clients of one profile (a user agent, the
// Accept-Language, a proxy) belongs in mimicry.Config, its Header and
// Proxy, not in a Profile per client. Profiles With returns for equal
// data share one copy of it.
func (p Profile) With(edit func(*Spec)) Profile {
	s := p.Spec()
	if edit != nil {
		edit(s)
	}

	return Profile{
		c: checkEdited(s),
	}
}

// errNoProfile is checkedSpec's error for the zero Profile.
var errNoProfile = errors.New("profile: the zero Profile is no profile")

// checkedSpec returns the validated Spec p holds, read-only, or the
// problems With found in it. The root package reads it through
// internal/checked.
func checkedSpec(p Profile) (*Spec, error) {
	switch {
	case p.c == nil:
		return nil, errNoProfile
	case p.c.err != nil:
		return nil, p.c.err
	default:
		return &p.c.spec, nil
	}
}

// The root package reads a Profile's Spec through internal/checked, which
// cannot import this package to name its type.
var _ = func() bool {
	checked.Spec = checkedSpec

	return true
}()

// Spec is a browser profile's data: one browser version on one platform.
type Spec struct {
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
	// profile whose captures the harness checks this one against, because
	// the scenario was not recaptured: an earlier version of its browser on
	// its platform, or its own or an earlier version on the platform whose
	// network stack it shares (Safari on iOS: macOS). Only the harness
	// reads it. See decisions 0035 and 0051.
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
	// Content-Length the body's length; values work as in Spec.Headers.
	Headers []Header `json:"headers"`
	// UploadRecords is how the browser cuts a request with a body into
	// TLS records; hellogen derives it from the http1-upload captures.
	// Nil: not measured, and the request goes out as one stream in full
	// records, as with HeadApart false.
	UploadRecords *UploadRecords `json:"uploadRecords,omitempty"`
}

// UploadRecords is how a browser cuts an HTTP/1.1 request with a body
// into TLS records. Every record is full (MaxRecord bytes of content) but
// the last of the head or of the body.
type UploadRecords struct {
	// HeadApart sends the head in a record of its own, the body starting
	// a new one (Chrome, Safari); false sends head and body as one
	// stream, cut into full records from the head's first byte (Firefox).
	HeadApart bool `json:"headApart"`
	// JoinUpTo, with HeadApart, sends a request whose head and body
	// together are at most this many bytes in one record (Chrome: 1400);
	// 0: never.
	JoinUpTo int `json:"joinUpTo,omitempty"`
}

// MaxRecord is the most content a TLS record carries (RFC 8446, section
// 5.1): every record of an upload is this full but the last of the head
// or body, in every captured browser.
const MaxRecord = 16384

// Parse decodes a profile from JSON, as Decode, validates it and returns
// it checked, sharing its copy with every Profile of equal content.
func Parse(data []byte) (Profile, error) {
	s, err := Decode(data)
	if err != nil {
		return Profile{}, err
	}

	if err := s.Validate(); err != nil {
		return Profile{}, err
	}

	c, key := find(s)
	if c == nil {
		c = keep(key, &compiled{
			spec: *s,
		})
	}

	return Profile{
		c: c,
	}, nil
}

// Decode decodes a profile from JSON without validating it, for harness
// captures, which lack what a complete profile needs. Unknown, miscased and
// repeated keys and trailing data are errors.
func Decode(data []byte) (*Spec, error) {
	var p Spec
	if err := jsonv2.Unmarshal(data, &p, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	return &p, nil
}

// Encode writes p as indented JSON with a trailing newline. It does not
// validate p, so captures of unusual browser behaviour can be recorded.
func Encode(p *Spec) ([]byte, error) {
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	return append(out, '\n'), nil
}
