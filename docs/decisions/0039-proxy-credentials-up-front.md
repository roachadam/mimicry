# 0039: Proxy credentials from the first request by default, the browser's challenge on request

- Status: Accepted
- Date: 2026-10-28
- Commits: `145b90b`

**Summary.** The maintainer's decision: by default an HTTP proxy gets the
URL's credentials on the first CONNECT or forwarded request, placed where
the browser places them, instead of after a 407; the browser's challenge
flow stays available as `Config.BrowserProxyAuth`. The challenge cost a
round trip per new proxy URL (per tunnel with Safari), which rotating
identities pay constantly, and only the proxy can see the difference. For
a library user, new proxy identities connect a round trip sooner, and
rejected credentials fail on the first request.

## Problem

Decision [0014](0014-proxies.md) gives an HTTP proxy the URL's
credentials as the profile's browser does. Chrome and Firefox send the
first CONNECT (or the first request sent to the proxy whole) without
them, answer the proxy's 407 on the same connection, and send them
unasked to that proxy from then on (`after_challenge`). Safari waits for
a 407 on every CONNECT (`on_challenge`). Each new proxy URL therefore
costs one round trip to the proxy before its first tunnel, and with a
Safari profile every tunnel costs one.

Rotating residential proxies put the identity in the URL's user name
(`user-session-123:pass@…`), so every rotation is a new proxy URL to
mimicry, and every new identity paid that round trip. The benchmark's
many-clients layer
([multiclient-2026-10-28](../../internal/bench/reports/multiclient-2026-10-28.md),
100 clients, a new identity every 10 requests, proxy +5 ms) put mimicry's
first request on a new identity through CONNECT behind every other client
over HTTP/2 at 1 KB (p50 58.5 ms against 50.1–54.8 ms); through SOCKS5,
which has no challenge, it was within 3 ms of the best.

The rule (decision [0037](0037-coalescing-opt-out.md)): a browser
behaviour that costs significant performance at a low-ish detection risk
defaults to the faster setting, documented plainly, with a `Config`
option for the browser's. The maintainer's decision (2026-10-28):
credentials go up front by default, and the browser's flow stays an option.

## Options

- **Keep the challenge flow (0014).** Exact for the proxy, a round trip
  per new proxy URL (per tunnel for Safari).
- **Credentials from the first request, placed where the browser places
  them when it sends them unasked.** Chosen. The proxy sees the
  browser's own credentialed CONNECT (or forwarded request) one round
  trip earlier and no 407; the server sees nothing different.
- **A fixed "Go" placement (Proxy-Authorization last, as net/http).**
  Rejected: the profiles measured where each browser puts it, so
  inventing a place would make the CONNECT unlike any browser's for no
  gain.
- **Remember credentials across Transports** (so only the first Transport
  per proxy pays). Does not help rotation (every identity is a new URL),
  and would share state the user keeps per Transport.

## Decision

By default (`Config.BrowserProxyAuth` false), when the proxy URL has
credentials:

- every CONNECT carries `Proxy-Authorization` (Basic) from the first,
  with the profile's `proxy.connectAuth` header list: the measured
  CONNECT with credentials (Chrome and Firefox: the same list unasked and
  on the retry; Safari: its retry, the only credentialed CONNECT it sends);
- a plain-HTTP request sent to the proxy whole carries it from the first,
  placed by `proxy.forwardAuth` as on the browser's unasked requests
  (Firefox's `retryLast` placement is for a retry, which no longer
  happens); every profile measured that placement (`after_challenge`
  browsers on the requests after the challenge, Safari on every request);
- WebSocket tunnels and Safari's `ws://` handshake sent whole follow the
  same paths, so the same rule;
- a 407 to credentials sent up front is final: a `*ProxyError` with the
  proxy's answer, as a 407 after the browser's flow was
  ([0033](0033-proxy-errors.md)).

`Config.BrowserProxyAuth` restores 0014's flow exactly (named like
`BrowserQUICFallback`). SOCKS5 is unchanged: no browser authenticates to
a SOCKS5 proxy, and credentials there are offered with the first
greeting already.

## Evidence

- Where the header goes is measured, never invented: EVIDENCE.md
  "Proxies" rows `connectAuth` (Chrome and Firefox: `connect` with
  Proxy-Authorization last, retry and unasked alike; Safari: its retry's
  order) and `forwardAuth`.
- `TestProxyMatchesBrowser` (harness) runs the captured flows with
  `BrowserProxyAuth`, unchanged, and adds two default-mode checks for
  every profile: `auth-upfront` (three tunnels, each `200` on its first
  CONNECT with credentials, each CONNECT's lines equal to the browser's
  first credentialed CONNECT, and the tunnel's ClientHello the profile's)
  and `plain-auth-upfront` (both forwarded requests credentialed, on one
  connection, placed as the browser's unasked one).
- `TestWebSocketsMatchBrowser` runs `proxy-websocket-auth` in both modes:
  the browser's, with every proxy route compared as before; and the
  default ("upfront"), where every socket is still the browser's against
  every sample (transport, handshake, frames, close) and every CONNECT
  carries credentials with the lines of a credentialed CONNECT the browser
  sent, none answered 407. That is the claim that only the proxy sees the
  difference, checked on the server's side.
- `TestProxyErrors` (root) has a final 407 in both modes, for CONNECT,
  plain HTTP and a WebSocket's tunnel.
- Every other client benchmarked sends `Proxy-Authorization` with the
  first CONNECT: net/http (`transport.go:1981-1990` at go1.27.3),
  tls-client v1.17.1 (`connect.go:168-175, 213-224`), httpcloak v1.7.3
  (`transport/http1_transport.go:946-950`,
  `transport/http2_transport.go:1150-1159`), azuretls v1.13.2
  (`proxy.go:162-175, 384-398`).

**Detection risk: low, and the proxy's only.** What reaches the server
through the tunnel is byte for byte what it was (the same ClientHello,
SETTINGS and headers; the tunnel opens a round trip sooner). The proxy
sees a client that knew it needed credentials: the browser's own
credentialed CONNECT, without the 407 before it. A proxy operator could
tell that apart from a browser, but proxies that sell rotation expect
programmatic clients, which all send credentials up front; nothing of it
reaches the sites.

## Consequences

- A new proxy URL's first tunnel, or forwarded request, costs no extra
  round trip; with a Safari profile no tunnel does.
- The proxy never sees the challenge flow unless `BrowserProxyAuth` is
  set; code measuring what a proxy sees (the harness) sets it.
- A proxy URL with credentials the proxy rejects fails on its first
  request, without a credential-less attempt first.

## Revisit when

- A site or vendor is seen reading the proxy's side of a connection
  (a proxy run by an anti-bot vendor, or one that forwards the
  credential flow), or a proxy refuses unasked credentials.
- A browser is captured sending proxy credentials unasked to a proxy it
  has not seen.
