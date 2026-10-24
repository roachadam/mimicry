# 0014: Proxies as the browser uses them

- Status: Accepted
- Date: 2026-10-24
- Commits: (this commit)

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
  default; no option for it yet.

## Decision

- `Config.Proxy func(*http.Request) (*url.URL, error)`, the `http.Transport`
  shape, so `http.ProxyURL` and `http.ProxyFromEnvironment` work. Schemes
  `http` (CONNECT for https, absolute-form requests for http), `socks5` and
  `socks5h` (both send the host name for the proxy to resolve, as browsers
  do). `https` proxies are not supported yet.
- `DialContext` reaches the proxy; everything above the tunnel is unchanged.
- Profile section `proxy`: `connect` (the CONNECT header list; Host and
  User-Agent filled in; a `Proxy-Authorization` slot marks the credential flow
  as measured), `forward_connection` (Chrome's Proxy-Connection on forwarded
  requests), `forward_auth_after` (where Proxy-Authorization goes on them),
  `socks5` (methods offered, address type). A profile without the section
  refuses proxies; without the slot it refuses HTTP-proxy credentials; without
  `socks5` it refuses SOCKS5 (decision 0008).
- Credentials: the first request goes without; a Basic 407 is answered once on
  the same connection (a new one if the proxy closed it); from then on every
  request to that proxy URL carries them unasked. SOCKS5 credentials, which no
  browser sends, are offered (method 2) only when the URL has them.
- Each proxy URL (credentials included) is part of the pool key: connections
  and TLS sessions are never shared between proxies, or with direct traffic.
  [INFERENCE] Browsers use one proxy configuration at a time, so there is no
  browser behaviour to copy; separating them keeps rotating proxies
  unlinkable by session tickets.
- Protocols in `internal/proxy` (CONNECT exchange, SOCKS5), the browser's way
  of using them in `internal/dial`, forwarding in `internal/h1`.

## Evidence

[`harness/reports/proxies-2026-10-24.md`](../../harness/reports/proxies-2026-10-24.md):
CONNECT header sets per browser, identical SOCKS5 handshakes, plain-HTTP
framing, the credential flow (Chrome's net log separates its preconnects from
the request's own flow), and tunnels that change nothing. Ledger section
"Proxies". Checked by `TestProxyMatchesBrowser` and
`TestUnmeasuredProxyIsAnError`.

## Consequences

- The first request to an authenticating proxy costs a round trip, as in the
  browser.
- Safari profiles work with proxies without credentials only: macOS takes
  proxy credentials from System Settings, and for a PAC-configured proxy has
  no field for them, so their flow could not be captured.
- Plain-HTTP requests through a proxy are framed as the browser frames them,
  but their header set is still an HTTPS request's (non-secure origins are not
  modelled yet, proxy or not).
- Chrome's speculative preconnects to the proxy are not made.

## Revisit when

Safari's credential flow is measured (a manual proxy with a saved password),
an https proxy is needed, a browser is captured offering SOCKS5
authentication, or a caller needs credentials sent from the first request.
