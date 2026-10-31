# 0014: Proxies as the browser uses them

- Status: Accepted; amended 2026-10-26 (WebSockets through proxies, Firefox's tunnels, Safari's WebSockets with credentials); amended 2026-10-28 by [0038](0038-http3-over-socks5.md) (HTTP/3 through SOCKS5 on request); amended 2026-10-28 by [0039](0039-proxy-credentials-up-front.md) (credentials up front by default) and [0040](0040-idle-connection-cleanup.md) (unused proxies' connections close; `MaxIdleProxies`); amended 2026-10-30 (`https://` proxies not planned)
- Date: 2026-10-24
- Commits: `fb21232`, `e906da5`, `4af5b8d`

**Summary.** `Config.Proxy` (the `http.Transport` shape) sends through
HTTP and SOCKS5 proxies with each browser's measured CONNECT headers,
SOCKS5 handshake and plain-HTTP framing, so neither proxy nor server sees
Go's defaults; connections and TLS sessions are never shared between
proxies. As amended, WebSockets go through proxies as measured,
credentials go up front by default (the browser's challenge-first flow is
`Config.BrowserProxyAuth`, 0039), HTTP/3 through SOCKS5 is opt-in (0038),
and unused proxies' idle connections close (`MaxIdleProxies`, 0040).

## Problem

Callers send through HTTP and SOCKS5 proxies. A proxy sees the client's
CONNECT request, its SOCKS5 handshake, plain-HTTP requests whole, and how it
hands over credentials; the server behind sees only what comes through. Both
have to look like the profile's browser, and a proxy must not change what the
server sees. Decision 0010 left proxies to compose with `Config.DialContext`.

## Options

- `http.Transport`-style CONNECT and `golang.org/x/net/proxy` SOCKS5: Go's
  header set (no Proxy-Connection, its own order) and preemptive Basic
  credentials, unlike every browser.
- A fixed "browser-like" CONNECT for all profiles: the browsers differ (order,
  Connection, User-Agent), so one shape is wrong for two of three.
- A profile section from captures, with credentials handled the way browsers
  handle them. Chosen.
- For credentials, preemptive from the first request (one round trip saved)
  against challenge-first: browsers never send credentials a proxy has not
  asked for, so preemptive-first would be visibly the caller's. Not chosen as a
  default then; now the default ([0039](0039-proxy-credentials-up-front.md)).

## Decision

- `Config.Proxy func(*http.Request) (*url.URL, error)`, the `http.Transport`
  shape, so `http.ProxyURL` and `http.ProxyFromEnvironment` work. Schemes
  `http` (CONNECT for https, absolute-form requests for http), `socks5` and
  `socks5h` (both send the host name for the proxy to resolve, as browsers
  do). `https` proxies are not supported yet.
- `DialContext` reaches the proxy; everything above the tunnel is unchanged.
- Profile section `proxy`: `connect` and `connectAuth` (the CONNECT header
  lists without and with credentials; Host and User-Agent filled in),
  `connectCredentials` (`after_challenge`: unasked once the proxy has
  asked, Chrome and Firefox; `on_challenge`: every tunnel waits for its 407,
  Safari), `forwardCredentials` (`after_challenge`, or `always` from the
  first request, Safari), `forwardConnection` (Chrome's Proxy-Connection on
  forwarded requests), `forwardAuth` (where Proxy-Authorization goes on
  them: after or before named headers, and Firefox's last on the retry),
  `socks5` (methods offered, address type). A profile without the section
  refuses proxies; without `connectAuth` it refuses HTTP-proxy credentials;
  without `socks5` it refuses SOCKS5 (decision 0008).
- A 407 is answered once, on the same connection (a new one if the proxy
  closed it), Basic only. Whether a proxy has asked is kept per proxy URL,
  shared by tunnels and forwarded requests ([INFERENCE]: not measured
  separately). SOCKS5 credentials, which no browser sends, are offered
  (method 2) only when the URL has them.
- Each proxy URL (credentials included) is part of the pool key: connections
  and TLS sessions are never shared between proxies, or with direct traffic.
  [INFERENCE] Browsers use one proxy configuration at a time, so there is no
  browser behaviour to copy; separating them keeps rotating proxies
  unlinkable by session tickets.
- Protocols in `internal/proxy` (CONNECT exchange, SOCKS5), the browser's way
  of using them in `internal/dial`, forwarding in `internal/h1`.

## Evidence

[`internal/harness/reports/proxies-2026-10-24.md`](../../internal/harness/reports/proxies-2026-10-24.md):
CONNECT header sets per browser, identical SOCKS5 handshakes, plain-HTTP
framing, the credential flow (Chrome's net log separates its preconnects from
the request's own flow), and tunnels that change nothing. Ledger section
"Proxies". Checked by `TestProxyMatchesBrowser` and
`TestUnmeasuredProxyIsAnError`.

## Consequences

- With `BrowserProxyAuth`, the first request to an authenticating proxy
  costs a round trip, as in the browser; with a Safari profile every tunnel
  does.
- Safari's credential rules come from Safari 27, measured once macOS itself
  had been given the credentials through its own login dialog (the report
  lists the setups that did not work); Safari 26 copies them.
- Plain-HTTP requests through a proxy are framed as the browser frames them,
  with the browser's non-secure header set ([0015](0015-non-secure-origins.md)).
- Chrome's speculative preconnects to the proxy are not made.

## Revisit when

Safari 26's credential flow is measured, a profile changes how Safari gets
proxy credentials, an https proxy is needed, or a browser is captured
offering SOCKS5 authentication.

## Amendment 2026-10-26: WebSockets, and Firefox's tunnels

The [proxy-websocket captures](../../internal/harness/reports/proxy-websocket-2026-10-26.md)
(all four browsers; Safari without credentials) added:
- `proxy.websocket`: `plain` is how a `ws://` socket goes through an
  HTTP proxy, `tunnel` (CONNECT, Chrome and Firefox) or `forward` (the
  handshake sent whole as `GET ws://…`, Safari); `credentials` that the
  socket tunnels' credential flow was measured (Chrome, Firefox). A
  `wss://` socket needs no field: every browser goes on the page's HTTP/2
  connection in its tunnel or opens a tunnel of its own, as any request.
  Without the section, with credentials but not `credentials`, and for
  `ws://` through SOCKS5 (no capture: Safari's proxy script sends one
  port to SOCKS5), a socket through a proxy is `ErrNotMeasured`.
- `proxy.connectNoResume`: Firefox resumes no session through an HTTP
  proxy's CONNECT tunnel (every connection of `proxy-auth` and both
  socket connections of `proxy-websocket`, while through SOCKS5 it
  resumes). This was already in the `proxy-auth` captures and unmodelled;
  it applies to every request, not only sockets. [INFERENCE] Firefox keys
  its TLS session cache by the proxy for CONNECT tunnels in a way that
  never matches; not established from source.
- Considered: refusing every proxied socket until a capture shows each
  combination (the earlier state), and inferring Safari's credential flow
  from its CONNECT rule (`on_challenge`). The second would be a guess on a
  path (CFNetwork's WebSocket task) that differs from its page loads in
  the one case measured (`ws://`).

## Amendment 2026-10-26: Safari's WebSockets with credentials

Safari 27's `proxy-websocket-auth` capture (3 samples; the
[report's addendum](../../internal/harness/reports/proxy-websocket-2026-10-26.md#addendum-2026-10-26-safari-27-with-credentials))
shows its sockets following its page rules: every socket tunnel
challenged, then again on its connection (`on_challenge`), and the
`ws://` handshake sent whole with credentials unasked, after
`Accept-Encoding` (`forwardCredentials: always`, `forwardAuth`). Both
Safaris now set `credentials`; Safari 26's is copied (not captured), as
its other credential rules are. `credentials` with `plain: forward` now
requires `forwardCredentials: always`: no browser was measured answering
a challenge to a handshake sent whole. The capture also shows Safari
putting `Proxy-Authorization` on requests inside its tunnels, to the
server, once it has answered a challenge to a plain-HTTP request; that is
not modelled ([EVIDENCE.md, Not covered](../../profiles/EVIDENCE.md#not-covered-by-any-claim)).

## Amendment 2026-10-28: HTTP/3 through SOCKS5

Requests through a proxy still stay on TCP by default, as in the browser.
`Config.HTTP3OverSOCKS5` carries HTTP/3 through `socks5` and `socks5h`
proxies over UDP associations, naming the host for the proxy to resolve
as the TCP path does, and no datagram ever goes around a proxy
([0038](0038-http3-over-socks5.md)).

## Amendment 2026-10-28: credentials up front by default, and idle proxies

The credential flow above is now `Config.BrowserProxyAuth`. By default a
proxy URL's credentials go on the first CONNECT (with `connectAuth`) and
the first request sent to the proxy whole (placed by `forwardAuth` as
unasked), so a new proxy URL costs no 407 round trip; a 407 to them is
final. Only the proxy sees the difference
([0039](0039-proxy-credentials-up-front.md)). Connections of a proxy
no longer used close early: by default the 16 most recently used proxies,
and any used in the last 10 seconds, keep their idle connections;
`Config.MaxIdleProxies` changes the count
([0040](0040-idle-connection-cleanup.md)).

## Amendment 2026-10-30: https:// proxies

TLS to the proxy stays unsupported until a caller needs it (the
maintainer's decision, 2026-10-30). The limit is documented in
[Proxies](../concepts/proxies.md#limits). Adding it takes an
implementation and a capture of each browser's TLS to the proxy.
