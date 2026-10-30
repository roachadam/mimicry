# Proxies

Set `Config.Proxy` to send requests through an HTTP or SOCKS5 proxy. It
takes the same function as `http.Transport.Proxy`:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.Chrome(),
	Proxy:   http.ProxyURL(proxyURL), // or http.ProxyFromEnvironment
})
```

If the function returns a nil URL, that request goes directly.

## What each side sees

The server sees what it would see without the proxy. The browser's
ClientHello, HTTP/2 settings and headers pass through the tunnel unchanged.

The proxy sees what the browser sends a proxy:

- **`http://` proxy, `https` request:** a CONNECT tunnel with the
  browser's headers. Chrome sends `Host`, `Proxy-Connection`,
  `User-Agent`. Firefox sends `User-Agent`, `Proxy-Connection`,
  `Connection`, `Host`. Safari sends `Host`, `Proxy-Connection`,
  `Connection`.
- **`http://` proxy, `http` request:** the whole request, with the full
  URL in the request line (`GET http://host/path HTTP/1.1`). Chrome sends
  `Proxy-Connection` instead of `Connection`.
- **`socks5://` or `socks5h://` proxy:** the browser's SOCKS5 handshake.
  It offers no authentication and sends the host name for the proxy to
  resolve. Both schemes behave the same.

## Credentials

Put credentials in the URL: `http://user:pass@proxy:3128`. Only Basic
authentication is supported.

By default the first CONNECT, or the first plain-HTTP request, carries a
`Proxy-Authorization` header where the browser places it. So a new proxy
URL costs no extra round trip. Only the proxy can tell; the server sees
the same bytes. A 407 answer fails the request with a `*ProxyError`.

`Config.BrowserProxyAuth` sends credentials the way the browser does:

- **Chrome and Firefox:** the first request to a proxy goes without them.
  The proxy's 407 is answered, and later requests to that proxy send them
  unasked.
- **Safari:** every CONNECT goes without them and is answered after the
  407. Plain-HTTP requests carry them from the start.

Each 407 is answered on the same connection, or a new one if the proxy
closed it.

One difference remains: after a 407 on a plain-HTTP request, Safari also
sends `Proxy-Authorization` inside its tunnels, where the server reads it.
mimicry never does (see the [backlog](../backlog.md#proxies)).

SOCKS5 credentials are sent when the URL has them. No browser sends
SOCKS5 credentials, so this is a fingerprint no browser produces.

## Connections

Each proxy URL has its own connections and TLS sessions. Nothing is
shared with other proxies or with direct requests. Proxied requests never
coalesce onto another host's HTTP/2 connection.

Firefox never resumes a TLS session through an HTTP proxy's tunnel, and a
Firefox profile does the same. Through SOCKS5 it resumes.

`Config.DialContext`, when set, opens the connection to the proxy.
`Config.DialTimeout` bounds two steps separately: reaching the proxy plus
its handshake, then the TLS handshake with the server. The request's
context bounds both.

### Rotating proxies

To rotate within one client, change what `Config.Proxy` returns. Cookies
and the page are kept. Each new proxy URL opens new connections.

Connections to old proxies close early once both are true:

- the proxy is not among the 16 used most recently, and
- it has not been used for 10 s.

A sweep checks this, so they close 10–20 s after their last request. A
round-robin over up to 16 proxies keeps every connection open. So does a
larger list where each proxy comes round within 10 s.

`Config.MaxIdleProxies` replaces this rule with a strict count. The N most
recently used proxies keep their idle connections. A proxy pushed out of
them has its idle connections closed at once. With 1, only the latest
proxy's connections stay, like azuretls's `SetProxy`. A round-robin over
more proxies than the count opens a new connection per request. For the
memory each setting costs, see [performance](../performance.md#many-clients-through-proxies).

To rotate by client instead, build a new client per identity. Call
`CloseIdleConnections` on the client you drop so its connections close at
once. Otherwise they close only when the garbage collector finds the
client, or at the idle timeout.

Building a client is not free. Build one client per proxy up front, and
reuse it for that proxy's requests.

WebSockets also go through the proxy; see
[WebSockets through a proxy](websockets.md#through-a-proxy).

## HTTP/3 through SOCKS5

Browsers do not use HTTP/3 through a proxy, so by default proxied requests
go over TCP. `Config.HTTP3OverSOCKS5` lets requests through a `socks5://`
or `socks5h://` proxy use HTTP/3 as they would without a proxy:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile:         profiles.Chrome(),
	Proxy:           http.ProxyURL(socksURL), // socks5h://user:pass@proxy:1080
	HTTP3OverSOCKS5: true,
})
```

Each QUIC connection gets its own UDP relay from the proxy (SOCKS5 UDP
ASSOCIATE). Its control connection authenticates like the TCP
connections, and `Config.DialContext` opens it. The proxy resolves the
host name, for both schemes. The site receives the browser's QUIC packets
unchanged, from the proxy's address.

Alt-Svc, 0-RTT and the fallback to TCP work as without a proxy.
Connections, session tickets and failed origins are kept per proxy URL;
advertised HTTP/3 endpoints are shared with direct requests.

When the proxy fails:

- **It refuses the UDP relay** (many SOCKS5 proxies have none): it is asked
  once, and every later request through that proxy URL uses TCP.
- **It drops the relay:** that QUIC connection ends at once, and its
  requests retry over TCP. The next connection asks for a new relay.
- **It relays nothing:** QUIC fails as with a server that never answers.
  Requests go over TCP after a short delay, until the origin is marked
  as not supporting HTTP/3.

While a request goes through a proxy, no packet goes to the site
directly. Through an `http://` proxy, or without the option, no UDP is
sent at all.

## When the proxy fails

A request the proxy fails returns a `*mimicry.ProxyError`. A rotation loop
can use it to tell a dead proxy from a site refusing the request. This
includes WebSocket dials.

```go
resp, err := client.Do(req)
var pe *mimicry.ProxyError
if errors.As(err, &pe) {
	current.Store(proxies.Next()) // pe.StatusCode: 0, 403, 407, ...
}
```

It covers every failure before the server is reached:

- the connection to the proxy fails;
- a CONNECT is answered with anything but 2xx, including a 407;
- the SOCKS5 handshake is refused;
- the deadline passes. `errors.Is(err, context.DeadlineExceeded)` still
  holds.

For a plain-HTTP request sent to the proxy, only a failed connection or a
final 407 is a `ProxyError`. Any other answer is returned as the
response, because it cannot be told apart from the server's.

`ProxyError.Proxy` is the proxy's URL without its password; `Error()`
never shows the password. `StatusCode` and `Header` are the proxy's
answer: 0 and nil for SOCKS5 or when it gave none. Failures after the
tunnel is up are the server's, not the proxy's. See
`ExampleConfig_proxy`.

## Limits

- `https://` proxies (TLS to the proxy) are not supported.
- Chrome's speculative extra connections to the proxy are not made.
- HTTP/3 goes only through SOCKS5 proxies. HTTP/3 through an `http://`
  proxy (CONNECT-UDP, MASQUE) is not supported.
- A CONNECT answer with a header over 64 KiB fails the request with a
  `*mimicry.ProxyError`.
- A request whose host is not a valid `Host` header is refused before
  anything is dialled.

Background: decisions [0014](../decisions/0014-proxies.md),
[0038](../decisions/0038-http3-over-socks5.md),
[0039](../decisions/0039-proxy-credentials-up-front.md),
[0040](../decisions/0040-idle-connection-cleanup.md),
[0044](../decisions/0044-profile-fingerprint.md).
