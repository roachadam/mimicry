package profile

// Proxy is how the browser talks to a proxy, as its proxy sees it; what
// reaches the server through the proxy is the rest of the profile,
// unchanged. See docs/decisions/0014.
type Proxy struct {
	// Connect are a CONNECT request's headers without credentials, in
	// order, named as sent. Host takes the target's host and port, and
	// User-Agent the profile's user agent; others are sent as given.
	Connect []Header `json:"connect"`
	// ConnectAuth are a CONNECT request's headers when it carries
	// credentials, Proxy-Authorization included, filled in as Connect's.
	// Nil: the browser's way with proxy credentials is not measured, and a
	// proxy URL with credentials is an error.
	ConnectAuth []Header `json:"connect_auth,omitempty"`
	// ConnectCredentials is when a CONNECT carries credentials:
	// CredentialsAfterChallenge or CredentialsOnChallenge. Required with
	// ConnectAuth.
	ConnectCredentials string `json:"connect_credentials,omitempty"`
	// ForwardCredentials is when a plain-HTTP request sent to a proxy
	// carries them: CredentialsAfterChallenge or CredentialsAlways.
	// Required with ConnectAuth.
	ForwardCredentials string `json:"forward_credentials,omitempty"`
	// ForwardConnection, when set, is the header that carries Connection's
	// value, in its place, on a plain-HTTP request sent to a proxy
	// ("Proxy-Connection": Chrome). Empty keeps Connection.
	ForwardConnection string `json:"forward_connection,omitempty"`
	// ForwardAuth places Proxy-Authorization in a plain-HTTP request sent
	// to a proxy. Nil: last.
	ForwardAuth *ForwardAuth `json:"forward_auth,omitempty"`
	// SOCKS5 is the browser's SOCKS5 handshake. Nil: not measured, so a
	// SOCKS5 proxy is an error.
	SOCKS5 *SOCKS5 `json:"socks5,omitempty"`
}

// Values of Proxy.ConnectCredentials and Proxy.ForwardCredentials.
const (
	// CredentialsAfterChallenge sends the first request to a proxy without
	// credentials, answers its 407 on the same connection, and sends them
	// unasked on every later request to that proxy (Chrome, Firefox).
	CredentialsAfterChallenge = "after_challenge"
	// CredentialsOnChallenge sends every request without credentials and
	// answers each 407 on the same connection (Safari's CONNECT).
	CredentialsOnChallenge = "on_challenge"
	// CredentialsAlways sends them on every request, the first included
	// (Safari's plain-HTTP requests).
	CredentialsAlways = "always"
)

// ForwardAuth is where Proxy-Authorization goes in a plain-HTTP request
// sent to a proxy, by the request's other headers (lowercase names; for
// Connection, the name before ForwardConnection renames it). With neither
// After nor Before, or when the request has none of the headers named, it
// goes last.
type ForwardAuth struct {
	// After places it after the last of these headers the request
	// carries (Chrome: connection, content-length; Safari:
	// accept-encoding).
	After []string `json:"after,omitempty"`
	// Before places it before this header (Firefox: connection).
	Before string `json:"before,omitempty"`
	// RetryLast places it last instead on a request sent again because
	// the proxy asked for credentials (Firefox).
	RetryLast bool `json:"retry_last,omitempty"`
}

// SOCKS5 is a browser's SOCKS5 (RFC 1928) handshake.
type SOCKS5 struct {
	// Methods are the authentication methods offered, in order: 0, no
	// authentication, is all browsers offer.
	Methods []int `json:"methods"`
	// Address is how the target is named: SOCKS5Domain, the host name for
	// the proxy to resolve.
	Address string `json:"address"`
}

// Values of SOCKS5.Address.
const (
	SOCKS5Domain = "domain"
)
