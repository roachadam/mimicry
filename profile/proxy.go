package profile

// Proxy is how the browser talks to a proxy, as its proxy sees it; what
// reaches the server through the proxy is the rest of the profile,
// unchanged. See docs/decisions/0014.
type Proxy struct {
	// Connect are a CONNECT request's headers without credentials, in
	// order, named as sent. Host takes the target's host and port, and
	// User-Agent the user agent in the profile's headers, which must have
	// one; others are sent as given.
	Connect []Header `json:"connect"`
	// ConnectAuth are a CONNECT request's headers when it carries
	// credentials, Proxy-Authorization included, filled in as Connect's.
	// Nil: the browser's way with proxy credentials is not measured, and a
	// proxy URL with credentials is an error.
	ConnectAuth []Header `json:"connect_auth,omitempty"`
	// ConnectCredentials is when a CONNECT carries credentials:
	// CredentialsAfterChallenge or CredentialsOnChallenge. Required with
	// ConnectAuth.
	ConnectCredentials CredentialTiming `json:"connect_credentials,omitempty"`
	// ForwardCredentials is when a plain-HTTP request sent to a proxy
	// carries them: CredentialsAfterChallenge or CredentialsAlways.
	// Required with ConnectAuth.
	ForwardCredentials CredentialTiming `json:"forward_credentials,omitempty"`
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
	// ConnectNoResume: TLS connections through an HTTP proxy's CONNECT
	// tunnel offer no earlier session; through SOCKS5, and directly, they
	// resume as TLS.Resumption says (Firefox).
	ConnectNoResume bool `json:"connect_no_resume,omitempty"`
	// WebSocket is how the browser opens WebSockets through a proxy. Nil:
	// not measured, and a WebSocket through a proxy is an error.
	WebSocket *ProxyWebSocket `json:"websocket,omitempty"`
}

// ProxyWebSocket is how a browser takes WebSockets through a proxy. A
// wss:// socket goes as it does directly, inside the proxy tunnel. A ws://
// socket through SOCKS5 was never measured.
type ProxyWebSocket struct {
	// Plain is how a ws:// socket goes through an HTTP proxy:
	// PlainWebSocketTunnel or PlainWebSocketForward. Required.
	Plain PlainWebSocket `json:"plain"`
	// Credentials: the browser's way with proxy credentials was measured
	// for sockets; PlainWebSocketForward then needs ForwardCredentials
	// CredentialsAlways. False: a WebSocket through a proxy URL with
	// credentials is an error. Requires ConnectAuth.
	Credentials bool `json:"credentials,omitempty"`
}

// PlainWebSocket is how a ws:// socket goes through an HTTP proxy: a
// value of ProxyWebSocket.Plain.
type PlainWebSocket string

// Values of PlainWebSocket.
const (
	// PlainWebSocketTunnel opens a CONNECT tunnel of its own to the
	// host and port and sends the handshake through it (Chrome, Firefox).
	PlainWebSocketTunnel PlainWebSocket = "tunnel"
	// PlainWebSocketForward sends the handshake to the proxy whole on its
	// own connection, the request line naming the ws:// URL (Safari).
	PlainWebSocketForward PlainWebSocket = "forward"
)

// CredentialTiming is when a request to a proxy carries credentials: a value
// of Proxy.ConnectCredentials and Proxy.ForwardCredentials.
type CredentialTiming string

// Values of CredentialTiming.
const (
	// CredentialsAfterChallenge sends the first request to a proxy without
	// credentials, answers its 407 on the same connection, and sends them
	// unasked on every later request to that proxy (Chrome, Firefox).
	CredentialsAfterChallenge CredentialTiming = "after_challenge"
	// CredentialsOnChallenge sends every request without credentials and
	// answers each 407 on the same connection (Safari's CONNECT).
	CredentialsOnChallenge CredentialTiming = "on_challenge"
	// CredentialsAlways sends them on every request, the first included
	// (Safari's plain-HTTP requests).
	CredentialsAlways CredentialTiming = "always"
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
	Address SOCKS5Address `json:"address"`
}

// SOCKS5Address is how a SOCKS5 request names its target: a value of
// SOCKS5.Address.
type SOCKS5Address string

// Values of SOCKS5Address.
const (
	SOCKS5Domain SOCKS5Address = "domain"
)
