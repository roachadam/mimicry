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
- Profile section `proxy`: `connect` and `connect_auth` (the CONNECT header
  lists without and with credentials; Host and User-Agent filled in),
  `connect_credentials` (`after_challenge`: unasked once the proxy has
  asked, Chrome and Firefox; `on_challenge`: every tunnel waits for its 407,
  Safari), `forward_credentials` (`after_challenge`, or `always` from the
  first request, Safari), `forward_connection` (Chrome's Proxy-Connection on
  forwarded requests), `forward_auth` (where Proxy-Authorization goes on
  them: after or before named headers, and Firefox's last on the retry),
  `socks5` (methods offered, address type). A profile without the section
  refuses proxies; without `connect_auth` it refuses HTTP-proxy credentials;
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

[`harness/reports/proxies-2026-10-24.md`](../../harness/reports/proxies-2026-10-24.md):
CONNECT header sets per browser, identical SOCKS5 handshakes, plain-HTTP
framing, the credential flow (Chrome's net log separates its preconnects from
the request's own flow), and tunnels that change nothing. Ledger section
"Proxies". Checked by `TestProxyMatchesBrowser` and
`TestUnmeasuredProxyIsAnError`.

## Consequences

- The first request to an authenticating proxy costs a round trip, as in
  the browser; with a Safari profile every tunnel does.
- Safari's credential rules come from Safari 27, measured once macOS itself
  had been given the credentials through its own login dialog (the report
  lists the setups that did not work); Safari 26 copies them.
- Plain-HTTP requests through a proxy are framed as the browser frames them,
  but their header set is still an HTTPS request's (non-secure origins are not
  modelled yet, proxy or not).
- Chrome's speculative preconnects to the proxy are not made.

## Revisit when

Safari 26's credential flow is measured, a profile changes how Safari gets
proxy credentials,
an https proxy is needed, a browser is captured offering SOCKS5
authentication, or a caller needs credentials sent from the first request.
