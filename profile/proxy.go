package profile

// Proxy is how the browser talks to a proxy, as its proxy sees it; what
// reaches the server through the proxy is the rest of the profile,
// unchanged. See docs/decisions/0014.
//
// Credentials go only where a proxy asks for them: the first request to a
// proxy goes without, a 407 challenge (Basic) is answered on the same
// connection, and every later request to that proxy carries them unasked.
type Proxy struct {
	// Connect are a CONNECT request's headers, in order, named as sent.
	// Host takes the target's host and port, and User-Agent the profile's
	// user agent; others are sent as given. Proxy-Authorization is sent in
	// its place, only with credentials; a list without it means the
	// browser's way with proxy credentials is not measured, and an HTTP
	// proxy URL with credentials is an error.
	Connect []Header `json:"connect"`
	// ForwardConnection, when set, is the header that carries Connection's
	// value, in its place, on a plain-HTTP request sent to a proxy
	// ("Proxy-Connection": Chrome). Empty keeps Connection.
	ForwardConnection string `json:"forward_connection,omitempty"`
	// ForwardAuthAfter places Proxy-Authorization in a plain-HTTP request
	// sent to a proxy: after this header (lowercase), or last when empty.
	ForwardAuthAfter string `json:"forward_auth_after,omitempty"`
	// SOCKS5 is the browser's SOCKS5 handshake. Nil: not measured, so a
	// SOCKS5 proxy is an error.
	SOCKS5 *SOCKS5 `json:"socks5,omitempty"`
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
