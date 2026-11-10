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
	ConnectAuth []Header `json:"connectAuth,omitempty"`
	// ConnectCredentials is when a CONNECT carries credentials:
	// CredentialsAfterChallenge or CredentialsOnChallenge. Empty, with
	// ConnectAuth: the browser's answer to a proxy's 407 is not measured
	// (Safari on iOS answers none), so credentials go with the first
	// request and a challenge flow (Config.BrowserProxyAuth) is an error.
	ConnectCredentials CredentialTiming `json:"connectCredentials,omitempty"`
	// ForwardCredentials is when a plain-HTTP request sent to a proxy
	// carries them: CredentialsAfterChallenge or CredentialsAlways. Empty
	// as ConnectCredentials, and only with it.
	ForwardCredentials CredentialTiming `json:"forwardCredentials,omitempty"`
	// ForwardConnection, when set, is the header that carries Connection's
	// value, in its place, on a plain-HTTP request sent to a proxy
	// ("Proxy-Connection": Chrome). Empty keeps Connection.
	ForwardConnection string `json:"forwardConnection,omitempty"`
	// ForwardAuth places Proxy-Authorization in a plain-HTTP request sent
	// to a proxy. Nil: last.
	ForwardAuth *ForwardAuth `json:"forwardAuth,omitempty"`
	// SOCKS5 is the browser's SOCKS5 handshake. Nil: not measured, so a
	// SOCKS5 proxy is an error.
	SOCKS5 *SOCKS5 `json:"socks5,omitempty"`
	// SOCKS4 is the browser's SOCKS4 request. Nil: not measured, so a
	// socks4 or socks4a proxy is an error. hellogen derives it from the
	// proxy-socks4 and proxy-socks4a captures.
	SOCKS4 *SOCKS4 `json:"socks4,omitempty"`
	// ConnectNoResume: TLS connections through a CONNECT tunnel, an
	// http:// or an https:// proxy's, offer no earlier session; through
	// SOCKS5, and directly, they resume as TLS.Resumption says (Firefox).
	ConnectNoResume bool `json:"connectNoResume,omitempty"`
	// HTTPS is how the browser uses an https:// proxy, reached over TLS.
	// Nil: not measured, and an https:// proxy is an error. hellogen
	// derives it from the proxy-https captures.
	HTTPS *ProxyHTTPS `json:"https,omitempty"`
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

// ProxyHTTPS is how a browser uses an https:// proxy. The TLS connection
// to the proxy sends the profile's ClientHello, server_name the proxy's
// host, and its ALPN offer (h2, http/1.1). Over HTTP/1.1 each tunnel takes
// a connection to the proxy of its own, its CONNECT Proxy.Connect's or
// Proxy.ConnectAuth's, as to an http:// proxy. Over HTTP/2 every tunnel
// is a stream of one connection, which is the profile's http2 section;
// a CONNECT's pseudo-header fields are :method and :authority, in
// http2.pseudoHeaderOrder's order. See docs/decisions/0065.
type ProxyHTTPS struct {
	// Connect are an HTTP/2 CONNECT's header fields after its
	// pseudo-header fields, in order, named as sent (lowercase). An
	// empty user-agent takes the user agent in the profile's headers.
	Connect []Header `json:"connect"`
	// ConnectAuth are those of a CONNECT carrying credentials,
	// proxy-authorization included. Nil: not measured, and an https://
	// proxy URL with credentials is an error.
	ConnectAuth []Header `json:"connectAuth,omitempty"`
	// Weight is a CONNECT stream's HEADERS priority weight, as on the
	// wire, in place of http2.headersPriority's (Chrome: 146, its lowest
	// priority; Firefox: 21). Its dependency and exclusivity are as for
	// any request.
	Weight uint8 `json:"weight"`
	// Reset is the error code, by its RFC 9113 name, of the RST_STREAM
	// that ends a tunnel's stream when its connection closes ("CANCEL").
	Reset string `json:"reset"`
	// Resume: a new TLS connection to the proxy resumes a session from
	// an earlier one, as TLS.Resumption says (Chrome). False: each is a
	// full handshake (Firefox, nearly always; profiles/EVIDENCE.md).
	Resume bool `json:"resume,omitempty"`
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
	RetryLast bool `json:"retryLast,omitempty"`
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

// SOCKS4 is a browser's SOCKS4 request: CONNECT, with an empty USERID.
// Through socks4:// the browser resolves the host itself and sends its
// IPv4 address (Chrome, Firefox); an IP address goes out as itself
// through either scheme.
type SOCKS4 struct {
	// Hostname: the browser can instead send the host name for the proxy
	// to resolve (SOCKS4A), which socks4a:// asks for (Firefox, with
	// network.proxy.socks_remote_dns; Safari). False: a socks4a proxy is an
	// error (Chrome has no SOCKS4A).
	Hostname bool `json:"hostname,omitempty"`
	// HostnameOnly: the browser always sends a host name (SOCKS4A) and
	// never resolves it itself, so a socks4 proxy is an error (Safari,
	// through macOS's SOCKS proxy setting as SOCKS4). Requires Hostname.
	HostnameOnly bool `json:"hostnameOnly,omitempty"`
	// HostnameDSTIP is DSTIP in a SOCKS4A request, 0.0.0.x with x not 0:
	// "0.0.0.1" (Firefox) or "0.0.0.16" (Safari). Required with Hostname.
	HostnameDSTIP string `json:"hostnameDstip,omitempty"`
}
