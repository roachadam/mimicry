# Proxies

Set `Config.Proxy` to send requests through an HTTP, HTTPS, SOCKS5 or
SOCKS4 proxy. It takes the same function as `http.Transport.Proxy`:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.ChromeMacOS(),
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
- **`socks4://` proxy:** the browser's SOCKS4 request. The browser looks
  the host up itself (mimicry with `Config.Resolver`, IPv4 only) and sends
  its IPv4 address, with an empty user ID. Chrome, Edge, Brave and Firefox
  do this. Safari never does (below), so a Safari profile refuses
  `socks4://` with `ErrNotMeasured`.
- **`socks4a://` proxy:** SOCKS4A, the host name sent for the proxy to
  resolve, with an empty user ID: Firefox with
  `network.proxy.socks_remote_dns` (address field `0.0.0.1`), and Safari
  (`0.0.0.16`). Chrome has no SOCKS4A, so a Chrome, Edge or Brave profile
  refuses `socks4a://` with `ErrNotMeasured`. Safari speaks SOCKS4 only
  when the Mac's SOCKS proxy setting carries a SOCKS version of 4, a
  hidden key (`kCFStreamPropertySOCKSVersion`) that no settings screen
  offers; its proxy script's `SOCKS` is SOCKS5. safari-27-macos sends
  what Safari 27 sent so configured; safari-26-macos sends the same,
  assumed, not captured.
- **`https://` proxy:** see [HTTPS proxies](#https-proxies).

## HTTPS proxies

An `https://` proxy (`https://user:pass@proxy:443`) is an HTTP proxy
reached over TLS. The proxy sees what the browser sends one:

- **The TLS handshake:** the profile's ClientHello, the same as to a site,
  with the proxy's host as `server_name` and `h2` and `http/1.1` offered.
  The proxy's certificate is checked against `Config.RootCAs` (the system
  roots when nil), and `Config.Pins` and `Config.VerifyConnection` apply,
  called with the proxy's host. `Config.KeyLogWriter` gets its secrets.
  A proxy asking for a client certificate gets none:
  `Config.GetClientCertificate` is not asked.
- **The proxy picks HTTP/2:** one connection carries every tunnel as a
  stream, as in Chrome, Edge and Firefox, with the browser's HTTP/2
  settings. A CONNECT sends `:method`, `:authority` and `user-agent`
  (`proxy-authorization` last with credentials), at the browser's
  priority (Chrome's lowest, Firefox's 21). A closed tunnel resets its
  stream (CANCEL).
- **The proxy picks HTTP/1.1:** each tunnel takes a TLS connection of
  its own, with the CONNECT lines an `http://` proxy gets. Chrome and Edge
  profiles resume the TLS session on the next connection; Firefox
  profiles do not.

Credentials go as to an `http://` proxy: up front by default, after the
407 with `Config.BrowserProxyAuth`, answered on the same HTTP/2
connection or the same (or a new) HTTP/1.1 connection.

Only `https://` URLs and `wss://` sockets go through an HTTPS proxy:
no browser was captured sending plain `http://` or a `ws://` socket
through one, so those are `ErrNotMeasured`. Safari and the iPhone
profiles, and Chrome 155 and Firefox 158, refuse HTTPS proxies the same
way: they were never captured (macOS offers Safari no proxy reached over
TLS).

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
mimicry never does
([EVIDENCE.md, Not covered](../../profiles/EVIDENCE.md#not-covered-by-any-claim)).

SOCKS5 credentials are sent when the URL has them. No browser sends
SOCKS5 credentials, so this is a fingerprint no browser produces. A
SOCKS4 URL's user name goes in the request's user ID field, which
browsers leave empty; SOCKS4 has no password, so a SOCKS4 URL with one
is an error.

## Connections

Each proxy URL has its own connections and TLS sessions. Nothing is
shared with other proxies or with direct requests. Proxied requests never
coalesce onto another host's HTTP/2 connection.

Firefox never resumes a TLS session through an HTTP or HTTPS proxy's
tunnel, and a Firefox profile does the same. Through SOCKS5 it resumes.

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

What mimicry learned through a proxy lasts longer than its connections:
its TLS sessions, its 407, its refused UDP relay or CONNECT-UDP, and its
sites' HTTP/3 state stay for the 1,024 proxies used most recently (and
any used in the last 10 s), about 1–2 KB each. One of them used again
opens a new connection that resumes its TLS session (0-RTT over HTTP/3)
and sends its credentials at once. A proxy beyond them is forgotten and,
used again later, starts like a new proxy: a full TLS handshake, and the
407 again with `Config.BrowserProxyAuth`. With `Config.MaxIdleProxies` a
proxy pushed out is forgotten at once. A forgotten proxy's tickets leave
a shared `Config.Tickets` cache too. Sessions are never shared between
proxies.

To rotate by client instead, build a new client per identity. Call
`CloseIdleConnections` on the client you drop so its connections close at
once. Otherwise they close only when the garbage collector finds the
client, or at the idle timeout.

Building a client costs about a microsecond: its profile is checked once
and shared. Still build one client per identity, and reuse it for that
identity's requests. Headers that differ per identity (`Accept-Language`,
a user agent) go in its `Config.Header`; a profile edited with
`Profile.With` per client costs about 200 µs each. A User-Agent set in
`Config.Header` does not reach an HTTP proxy: the CONNECT carries the
profile's, as the browser's carries its own user agent and not a page's.

WebSockets also go through the proxy; see
[WebSockets through a proxy](websockets.md#through-a-proxy).

## HTTP/3 through SOCKS5

Browsers do not use HTTP/3 through a proxy, so by default proxied requests
go over TCP. `Config.HTTP3OverSOCKS5` lets requests through a `socks5://`
or `socks5h://` proxy use HTTP/3 as they would without a proxy:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile:         profiles.ChromeMacOS(),
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
directly. Through an `http://` or `https://` proxy without
[CONNECT-UDP](#http3-through-http-proxies-connect-udp), or SOCKS5
without the option, no UDP is sent at all.

## HTTP/3 through HTTP proxies (CONNECT-UDP)

`Config.ConnectUDPProxy` lets requests through an `http://` or
`https://` proxy use HTTP/3, carried by CONNECT-UDP (RFC 9298, MASQUE)
to a proxy reached over HTTP/3. Providers that offer it (Bright Data,
Oxylabs, Decodo) run it on an endpoint of its own, beside their TCP
proxy, so the function maps each proxy to its CONNECT-UDP proxy:

```go
client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.ChromeMacOS(),
	Proxy:   http.ProxyURL(proxyURL), // http://user:pass@pr.oxylabs.io:7777
	ConnectUDPProxy: func(p *url.URL) (*url.URL, error) {
		return &url.URL{
			Scheme: "https",
			User:   p.User,
			Host:   "masque.oxylabs.io:50000",
		}, nil
	},
})
```

Return the proxy itself when it also serves HTTP/3 on its own address,
or nil to keep that proxy's requests on TCP. Requests over TCP still go
through `Config.Proxy`.

Each QUIC connection to a site opens its own HTTP/3 connection to the
CONNECT-UDP proxy and asks it for the site (`/.well-known/masque/udp/host/port/`).
The site receives the browser's QUIC packets unchanged, from the proxy's
address. Alt-Svc, 0-RTT and the fallback to TCP work as without a proxy;
each new connection waits two round trips to the CONNECT-UDP proxy
first.

What the CONNECT-UDP proxy sees:

- **The QUIC handshake:** the profile's QUIC ClientHello, with its host
  as `server_name`, checked like an `https://` proxy's (`Config.RootCAs`,
  `Pins`, `VerifyConnection`, `KeyLogWriter`). Its session is never
  resumed. Its packets are 64 bytes larger than the browser's (1314
  bytes for Chrome), so the site's fit inside them.
- **The request:** the profile's HTTP/3 settings, then a CONNECT-UDP
  naming the site's host and port, with `capsule-protocol: ?1`, the
  profile's `user-agent` and, with credentials, `proxy-authorization`.
  No browser sends one.

Credentials go as to an `http://` proxy: up front, or after the 407 with
`Config.BrowserProxyAuth`.

When the CONNECT-UDP proxy fails:

- **It refuses** (its connection has no HTTP/3 datagrams, or it answers
  400, 404, 405, 501, or 407 to the credentials): it is asked once, and
  later requests through that proxy use TCP. Other answers, such as 403
  for a target the provider blocks, fail that one attempt.
- **It ends the stream:** that QUIC connection ends at once, and its
  requests retry over TCP.
- **It never answers:** requests go over TCP after the race delay, until
  the origin is marked as not supporting HTTP/3.

Profiles whose QUIC announces no datagrams (Safari, and Chrome and
Firefox on iOS, which use Safari's network stack) stay on TCP.

What a site can see: HTTP/3 from a proxy's address, which no browser
behind a proxy sends. Its packets to the client must fit in the proxy's
with about 50 bytes to spare, so one over about 1420 bytes (Chrome
accepts 1472) is dropped on the way.

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

- the connection to the proxy fails, its TLS handshake included (an
  `https://` proxy's certificate, `Config.Pins`, `VerifyConnection`);
- a CONNECT is answered with anything but 2xx, including a 407;
- the SOCKS5 handshake or the SOCKS4 request is refused;
- the deadline passes. `errors.Is(err, context.DeadlineExceeded)` still
  holds.

Not a `ProxyError`: an IPv6 target through SOCKS4, which it cannot
carry, a failed lookup through `socks4://`, and a `socks4://` host name
with `Config.DialContext` set and no `Config.Resolver` with its own
`Dial` (the lookup would bypass your dialer; use `socks4a://` or such a
resolver). All fail before any proxy is dialled, and another proxy would
do no better.

For a plain-HTTP request sent to the proxy, only a failed connection or a
final 407 is a `ProxyError`. Any other answer is returned as the
response, because it cannot be told apart from the server's.

`ProxyError.Proxy` is the proxy's URL without its password; `Error()`
never shows the password. `StatusCode` and `Header` are the proxy's
answer: 0 and nil for SOCKS or when it gave none. Failures after the
tunnel is up are the server's, not the proxy's. See
`ExampleConfig_proxy`.

## Limits

- `https://` proxies take `https://` requests and `wss://` sockets only;
  plain HTTP and `ws://` through one are `ErrNotMeasured`. A proxy asking
  for a client certificate gets none. Firefox occasionally resumes its
  TLS session with the proxy; a Firefox profile never does.
- Proxy authentication is Basic only; a proxy asking for Digest, NTLM or
  Negotiate fails the request.
- `Config.DialContext` opens the connection to the proxy, so it cannot
  also pin the server's address.
- Chrome's speculative extra connections to the proxy are not made.
- Through a proxy no DNS HTTPS record is looked up, so a site gets the
  ClientHello with GREASE ECH, never real Encrypted Client Hello; the TLS
  to an `https://` proxy uses GREASE too. No capture shows what the
  browsers do there ([decision 0068](../decisions/0068-real-ech.md)).
  Through `socks4://` too: mimicry looks the host's address up for the
  SOCKS4 request, but not its HTTPS record.
- HTTP/3 through an `http://` or `https://` proxy goes only by
  CONNECT-UDP over HTTP/3, with the default URI template; CONNECT-UDP
  over HTTP/2 or HTTP/1.1 (capsules), and other templates, are not
  supported. Safari and the iPhone profiles never use it. SOCKS4 has no
  UDP: requests through a SOCKS4 proxy always stay on TCP.
- Through `socks4://` the host must have an IPv4 address; when it has
  several, the first the resolver returns is sent (which address a
  browser picks, and whether it tries another when the proxy refuses, is
  not measured).
- WebSockets through a SOCKS4 proxy were not measured, and the iPhone
  profiles and the older versions (Chrome 155, Firefox 158) were never
  captured with SOCKS4: they fail a SOCKS4 proxy with `ErrNotMeasured`.
  Safari 26's SOCKS4 is Safari 27's, assumed rather than captured.
- A SOCKS5 proxy that accepts UDP ASSOCIATE but relays nothing, and a
  CONNECT-UDP proxy that never answers, are not remembered: each origin
  waits up to the race delay per request until it is marked as not
  supporting HTTP/3.
- A CONNECT answer with a header over 64 KiB fails the request with a
  `*mimicry.ProxyError`.
- A request whose host is not a valid `Host` header is refused before
  anything is dialled.

Background: decisions [0014](../decisions/0014-proxies.md),
[0038](../decisions/0038-http3-over-socks5.md),
[0039](../decisions/0039-proxy-credentials-up-front.md),
[0040](../decisions/0040-idle-connection-cleanup.md),
[0044](../decisions/0044-profile-fingerprint.md),
[0063](../decisions/0063-socks4-proxies.md),
[0065](../decisions/0065-https-proxies.md),
[0066](../decisions/0066-http3-over-connect-udp.md).
