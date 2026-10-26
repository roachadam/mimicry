# Proxies

Set `Config.Proxy` to send requests through an HTTP or SOCKS5 proxy. It takes
the same function as `http.Transport.Proxy`:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome155MacOS(),
	Proxy:   http.ProxyURL(proxyURL), // or http.ProxyFromEnvironment
})
```

Returning a nil URL sends that request directly.

## What each side sees

The server sees exactly what it would without the proxy: the profile's
ClientHello, HTTP/2 settings and headers, tunnelled byte for byte.

The proxy sees what the profile's browser sends a proxy, from captures
([decision 0014](../decisions/0014-proxies.md)):

- `http://` proxy, `https` request: a CONNECT tunnel with the browser's
  headers (Chrome: `Host`, `Proxy-Connection`, `User-Agent`; Firefox:
  `User-Agent`, `Proxy-Connection`, `Connection`, `Host`; Safari: `Host`,
  `Proxy-Connection`, `Connection`).
- `http://` proxy, `http` request: the request itself in absolute form
  (`GET http://host/path HTTP/1.1`), Chrome's with `Proxy-Connection` in place
  of `Connection`.
- `socks5://` or `socks5h://` proxy: the browsers' SOCKS5 handshake, offering
  no authentication and sending the host name for the proxy to resolve. Both
  schemes behave alike.

## Credentials

Put them in the URL (`http://user:pass@proxy:3128`). They are used the way
the profile's browser uses them:

- Chrome and Firefox: the first request to a proxy goes without, the
  proxy's 407 is answered on the same connection, and every later request
  to that proxy sends them unasked.
- Safari: every CONNECT goes without and is answered after its 407 (on the
  same connection, in a different header order); plain-HTTP requests carry
  them from the first. Once Safari has answered a 407 to a plain-HTTP
  request, it also puts `Proxy-Authorization` on requests inside its
  tunnels, where the server reads it; mimicry never does (see the
  [backlog](../backlog.md#proxies)).

Only Basic authentication is supported. SOCKS5 credentials, which no
browser sends, are offered when the URL has them; that part is visibly
yours.

## Connections

Each proxy URL has its own connections and TLS sessions (the pool key
includes the proxy); nothing is shared with other proxies or with direct
requests. Firefox resumes no session through an HTTP proxy's CONNECT
tunnel (none of its connections through one did; through SOCKS5 they
resume), and a Firefox profile does the same (`proxy.connect_no_resume`).
`Config.DialContext`, when set, opens the connection to the
proxy. `Config.DialTimeout` bounds the connect to the proxy together with
the proxy's handshake (CONNECT or SOCKS5), and the TLS handshake with the
server separately; the request's context bounds both.

## WebSockets

`Transport.DialWebSocket` goes through the proxy as the browser's socket
does: a `wss://` socket as a stream on the host's HTTP/2 connection inside
its tunnel when the server allows extended CONNECT, else in a tunnel of
its own; a `ws://` socket through an HTTP proxy in a CONNECT tunnel
(Chrome, Firefox) or sent to the proxy whole as `GET ws://…` (Safari).
Credentials go as on the browser's other tunnels and plain-HTTP requests
(Safari's forwarded `ws://` handshake carries them unasked); `ws://`
through SOCKS5 is `ErrNotMeasured`. See
[WebSockets](websockets.md#through-a-proxy).

## When the proxy fails

A request the proxy fails returns a `*mimicry.ProxyError`, so a rotation
loop can tell a dead or refusing proxy from a site refusing the request
(a WebSocket's dial too):

```go
resp, err := client.Do(req)
var pe *mimicry.ProxyError
if errors.As(err, &pe) {
	current.Store(proxies.Next()) // pe.StatusCode: 0, 403, 407, ...
}
```

It covers everything before the server is reached: the connect to the
proxy, a CONNECT answered other than 2xx (a 407 once the URL's
credentials were given as the browser gives them, or when it has none), a
SOCKS5 handshake the proxy refused (no acceptable method, rejected
credentials, a refused connection), and the deadline passing on the way
(`errors.Is(err, context.DeadlineExceeded)` still holds). A plain-HTTP
request sent to the proxy whole returns one only for a failed connect or a
final 407; any other answer is the response, since it cannot be told from
the server's. `ProxyError.Proxy` is the proxy's URL without its password,
which `Error()` never shows; `StatusCode` and `Header` are the proxy's
answer (0 and nil for SOCKS5, or when it gave none). Failures after the
tunnel is up (the TLS handshake with the server, the request) are the
server's. See `ExampleConfig_proxy`.

## Limits

- `https://` proxies (TLS to the proxy) are not supported.
- Chrome's speculative extra connections to the proxy are not made.
- A CONNECT answer whose header is longer than 64 KiB fails the request
  (a `*mimicry.ProxyError`).
- A request whose host is not a valid `Host` header is refused before
  anything is dialled, proxy or not.
