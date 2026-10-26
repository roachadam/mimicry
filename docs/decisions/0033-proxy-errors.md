# 0033: Proxy failures are a *ProxyError

- Status: Accepted
- Date: 2026-10-26
- Commits: (where it landed)

## Problem

A rotation loop has to tell "this proxy is dead, slow or refusing me"
from "the site refused me": the first means the next proxy, the second
something else. mimicry's proxy failures were plain errors whose text
named the proxy and the status, so callers matched strings
([coming from tls-client](../concepts/coming-from-tls-client.md)). No
browser behaviour is involved: this is what the caller is told, not what
is sent.

## Options

- **Sentinel errors** (`ErrProxyRefused`, `ErrProxyAuth`, ...). Cheap,
  but the answer's status and header (a `Proxy-Authenticate` realm, a
  vendor's `X-...` reason) are lost, and the list of sentinels grows with
  every way a proxy fails.
- **Wrap the status in the text only.** What existed.
- **One struct type with the answer.** `ProxyError{Proxy, StatusCode,
  Header, Err}`: `errors.As` finds it, `StatusCode` tells a 407 from a
  403 from no answer at all, `Err` keeps the cause so `errors.Is(err,
  context.DeadlineExceeded)` still says it was a timeout. Chosen.

What it covers: everything before the server is reached. The connect to
the proxy, a CONNECT answered other than 2xx (a 407 only once the
browser's credential flow is spent: answering the first 407 is part of
the flow, decision 0014), a SOCKS5 refusal at any step, a deadline on the
way. A plain-HTTP request sent to an HTTP proxy whole is answered by the
proxy too; only a final 407 there is surely the proxy's, so that becomes
a `ProxyError` and every other answer stays the response. What fails
after the tunnel is up (the server's TLS handshake, the request) is the
server's: a proxy that tunnels and then drops traffic shows as the
server failing, since nothing can tell the two apart.

The proxy's URL carries its password; an error is logged, so `Proxy` is
a copy with the password removed (the username stays, to tell accounts
apart), and `Error()` prints that.

## Decision

`*mimicry.ProxyError` as above, made in the root package from the dial
package's internal error (internal packages cannot import the root), on
every path that reaches a proxy: requests, CORS preflights, redirect hops
and `FetchPins`, over HTTP/1.1 and HTTP/2, and (amended 2026-10-26, once
they were measured) `DialWebSocket`: a socket's own tunnel, and a 407
to a handshake sent to the proxy whole (Safari's `ws://`). It replaces
the errors that wrapped it (attempt counts), as it says all they did.

The SOCKS5 and CONNECT code now reports a timeout at the context's
deadline as `context.DeadlineExceeded` even when the connection's
deadline, set from the context, fires a moment before the context's own
timer.

## Evidence

`TestProxyErrors` (root): one table against the examples' CONNECT proxy
and a fake SOCKS5 server: unreachable, CONNECT 403, 407 without
credentials, 407 after the credential flow (CONNECT and plain HTTP), an
unanswered CONNECT and SOCKS5 handshake (deadline), SOCKS5 refused, no
acceptable method, rejected credentials, and a WebSocket's tunnel 407,
forwarded handshake 407 and SOCKS5 refusal; the password never in
`Error()` or `Proxy`. `ExampleConfig_proxy` rotates on it. Removing the
password strip, or the plain-HTTP 407 conversion, fails the test.

## Consequences

Rotation is `errors.As(err, &pe)`. Callers that matched the old text
must switch (the text changed: "mimicry: proxy URL: cause").

## Revisit when

`https://` proxies land (a TLS failure to the proxy belongs here too).
