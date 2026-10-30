# What mimicry does on the wire

You call `client.Do(req)`, and mimicry sends the request the way the
profile's browser would. A browser also does things around a request: it
follows redirects, sends CORS preflights, reuses connections and switches
to HTTP/3. mimicry does them too. This page lists each one: what happens,
why, and how to change it.

mimicry sends no request you did not ask for, apart from redirect hops and
CORS preflights. Everything else is connection-level traffic, as a browser
makes it.

Four defaults differ from the browser ([README](../../README.md#deviations-from-the-browser)): the 30 s request timeout, the 90 s idle close, and these two, because the browser's way costs noticeable time and is unlikely to be checked:
- when QUIC fails, mimicry falls back to TCP sooner
  (`Config.BrowserQUICFallback` restores the browser's waits);
- proxy credentials go with the first request instead of after the proxy's
  407 (`Config.BrowserProxyAuth` restores the browser's way).

Where no option exists, the [backlog](../backlog.md#options-that-do-not-exist)
lists it as a candidate. To see what a request will send without sending
it, use [`Transport.Plan`](how-a-request-is-built.md#5-seeing-it-before-it-is-sent).

## At a glance

| Behaviour | Extra traffic | Turn it off or change it |
|---|---|---|
| [Redirects](#redirects) | one request per hop | `http.Client.CheckRedirect` |
| [CORS preflights](#cors-preflights) | an `OPTIONS` before some cross-origin requests | `Exact`; `Request.NoCORS` |
| [Cookies](#cookies) | none | `Config.Cookies`; a `Cookie` header; `Request.Credentials` |
| [Response decoding](#response-decoding) | none | `Config.DisableDecompression` |
| [Connection reuse and limits](#connection-reuse-and-limits) | fewer connections; idle ones closed after 90 s | `Config.MaxConnsPerHost`; `Config.IdleConnTimeout` |
| [Retries](#retries-on-another-connection) | the same request again | none |
| [30 s request timeout](#a-30-s-request-timeout) | none | `Config.Timeout` |
| [HTTP/2 coalescing](#http2-coalescing) | a DNS lookup; Firefox: one declined connection | `Config.DisableCoalescing` |
| [HTTP/3](#http3) | a QUIC connection beside TCP | `Config.DisableHTTP3`; `Config.AltSvc` |
| [QUIC failure](#when-quic-fails) | TCP connections racing QUIC | `Config.BrowserQUICFallback` |
| [Keep-alive and housekeeping](#keep-alive-and-housekeeping-frames) | PING, WINDOW_UPDATE, ACK frames | none |
| [TLS resumption and 0-RTT](#tls-resumption-and-0-rtt) | a `425 Too Early` request sent again | `Config.DisableHTTP3` stops 0-RTT |
| [WebSockets](#websockets) | only when you call `DialWebSocket` | don't call it |
| [Proxies](#proxies) | CONNECT or SOCKS5 handshakes | `Config.Proxy` |
| [Certificate pins](#certificate-pins-and-checks) | none unless set | `Config.Pins`; `Config.VerifyConnection` |
| [The last navigation](#the-last-navigation) | none (header values) | `Request.From` |

## Redirects

**What happens.** `http.Client` follows a `3xx` with a `Location`. Each
hop goes back through the Transport, which sends it as the browser does:
- A POST answered 301 or 302 becomes a GET without its body and
  `Content-Type`. So does anything but GET or HEAD answered 303.
- Every other request keeps its method, body and `Content-Type`. This
  includes a PUT, DELETE or PATCH answered 301 or 302, which
  `http.Client` alone would turn into a GET. The body is rebuilt from the
  request's `GetBody`. `http.NewRequest` sets it for in-memory bodies;
  without it, following such a redirect is an error.
- Each hop gets its own `Referer`, `Origin`, `sec-fetch-site` and cookies.
  Each response's `Set-Cookie` lines go to the jar.
- A hop to another origin may need a [preflight](#cors-preflights).
- When a same-origin `fetch()` POST, PUT or DELETE becomes a GET, Safari
  keeps its `Origin` header and Chrome and Firefox drop it.

A chain stops with an error after 20 redirects. A navigation stops at the
browser's limit: 19 in Chrome, 20 in Firefox and Safari.

A cross-origin `Fetch`, `XHR` or CORS-mode request is checked at each
redirect. If the redirect does not allow the request's origin,
`client.Do` returns a `*CORSError` with `Redirect` set, and the next URL
is never requested. Same-origin and `no-cors` requests, navigations and
elements are not checked.

**Why.** The browser follows redirects itself, and a server sees each
hop's headers and timing.

**How to change it.** The Transport never follows a redirect on its own.
`NewClient` sets a `CheckRedirect` that lifts `http.Client`'s limit of 10.
Set your own to stop earlier:

```go
client.CheckRedirect = func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse // return the 3xx itself
}
```

`Request.IgnoreCORS` follows a refused redirect anyway. The limit cannot
go above the browser's. `http.Client`'s `via` list shows the hops as it
built them: a PUT answered 301 appears there as a GET. The requests
actually sent, `resp.Request` included, are the browser's.

## CORS preflights

**What happens.** Some cross-origin `Fetch` and `XHR` requests need
permission first. These use a method other than GET, HEAD or POST, or a
header such as a JSON `Content-Type`, `Authorization` or `X-…`. mimicry
sends an `OPTIONS` request before them, as the browser does. If the answer
refuses, `client.Do` returns a `*CORSError` and the request is not sent.
Answers are cached per Transport. See [CORS preflights](cors-preflights.md).

**Why.** A cross-origin API call without its preflight is a request no
browser makes.

**How to change it.**
- `Request.IgnoreCORS` sends the request even if the answer refuses it.
- `Exact(req)` sends no preflight, but also drops the profile's headers.
- `Request.NoCORS` makes a `Fetch` a `no-cors` fetch, which is never
  preflighted. It allows only GET, HEAD and POST.
- A request from a same-origin page (`Request.From`) needs none.

## Cookies

**What happens.** Each request carries the cookies the browser would send,
and each response's `Set-Cookie` lines are stored. Redirect hops included.
See [cookies](cookies.md).

**Why.** Anti-bot systems check that a site's cookies come back as a
browser would send them.

**How to change it.**
- `NewClient` creates a jar unless `Config.Cookies` holds one.
  `Transport.Cookies()` returns it, to inspect, seed or save. Passing one
  jar to several Transports shares it.
- `NewTransport` with no `Config.Cookies` keeps no cookies.
- A `Cookie` header on the request is sent instead of the jar's.
- `Request.Credentials` sets a `Fetch` or `XHR` request's credentials mode.

## Response decoding

**What happens.** A body encoded with gzip, deflate, Brotli or zstd is
decoded as you read it. The `Content-Encoding` and `Content-Length`
headers are removed. This depends only on the response's
`Content-Encoding`, not on who set `Accept-Encoding`. Other or stacked
encodings come back raw. Nothing changes on the wire.

**How to change it.** `Config.DisableDecompression` returns every body
exactly as the server sent it.

## Connection reuse and limits

**What happens.**
- One HTTP/2 connection per host carries every request to it.
- HTTP/1.1 connections carry one request at a time. At most 6 are open
  to one host, as in every browser. Further requests wait their turn.
- An idle connection closes 90 s after its last request. Through
  rotating proxies, idle connections can close sooner: see
  [Proxies](proxies.md#rotating-proxies).
- Chrome profiles keep requests without credentials and cross-site frames
  on separate connections, as Chrome does.

**Why.** How many connections a client opens, and which requests share
them, is visible to the server.

**How to change it.**
- `Config.MaxConnsPerHost` changes the limit of 6. A server counting
  connections sees more than 6 as not a browser.
- `Config.IdleConnTimeout` changes the 90 s. A negative value leaves idle
  connections open until the server or proxy closes them.
- `Transport.CloseIdleConnections` closes idle connections now. Call it on
  a client you discard.
- A Transport is one browsing session. To visit unrelated sites, use one
  Transport per site; they can share a cookie jar.

## Retries on another connection

**What happens.** A request goes again on another connection, up to 5
attempts, when nothing of it was sent or the server refused it unread. An
idempotent request (GET, HEAD, OPTIONS, PUT, DELETE…) is also retried when
a reused connection had closed. A POST that may have reached the server is
never sent twice.

**Why.** Browsers do the same; a server closing an idle connection is
normal.

**How to change it.** No option.

## A 30 s request timeout

**What happens.** The client from `NewClient` gives up on a request after
30 s, redirects and reading the body included. `NewTransport` sets none.
Nothing on the wire changes.

**Why.** A stalled connection should fail, not hang forever.

**How to change it.** `Config.Timeout` (negative for none), or the
client's `Timeout`. A context deadline applies as well.

## HTTP/2 coalescing

**What happens.** A request to a new host may reuse another host's HTTP/2
connection, if its certificate covers both hosts. Chrome and Safari
profiles look up the host's address and reuse a connection to the same
address. Firefox profiles open a connection first, then close it with a
GOAWAY if an existing one reaches the same address.

**Why.** Browsers coalesce, so a site sees one connection where a browser
would open one.

**How to change it.** `Config.DisableCoalescing` gives every host its own
connection. `Config.Resolver` answers the lookup.

## HTTP/3

**What happens.** HTTP/3 is on by default. mimicry uses it for an origin
only after that origin sends an `Alt-Svc: h3` header. Each browser then
switches its own way: Chrome from the next request, Firefox at once in the
background, Safari on the next new connection. HTTP/3 traffic goes straight
over UDP and ignores `Config.DialContext`. See [HTTP/3](http3.md).

**Why.** A browser that has seen `Alt-Svc` moves to HTTP/3, and a CDN sees
which protocol its visitors use.

**How to change it.**
- `Config.DisableHTTP3` keeps every request on TCP.
- `Config.AltSvc` shares, saves and loads what Transports learnt, so a new
  Transport can start on HTTP/3 as a restarted browser does.
- `Config.UDPReadBufferSize` sizes the receive buffer; nothing on the wire
  depends on it.
- `Config.HTTP3ACKEvery` sends fewer ACKs. This helps with an overloaded
  server, but no browser does it.

Through a proxy, see [HTTP/3 through SOCKS5](proxies.md#http3-through-socks5).

## When QUIC fails

**What happens.** While a QUIC connection is not up, each browser races or
falls back to TCP its own way. After several failures in a row, the
origin stays on TCP. By default, mimicry moves to TCP sooner than the
browser when QUIC is failing. A working QUIC connection is unaffected.
See [HTTP/3: if QUIC is blocked](http3.md#if-quic-is-blocked).

**Why.** A request must not stall behind a blocked QUIC path. Anti-bot
systems are not known to time that path.

**How to change it.** `Config.BrowserQUICFallback` keeps the browser's
waits. `Config.DisableHTTP3` skips QUIC.

## Keep-alive and housekeeping frames

**What happens.** Each profile sends the frames its browser sends:
- HTTP/2: Chrome pings a connection that was quiet for 10 s before using
  it. Firefox and Safari never ping. Every profile answers the server and
  returns flow-control credit as its browser does.
- TLS: frames are grouped into TLS records as the browser groups them.
- QUIC: PINGs keep a quiet connection alive while a request waits. ACKs
  follow each browser's rules.

None of this is background traffic on an idle connection.

**Why.** These frames are part of each browser's fingerprint.

**How to change it.** No option.

## TLS resumption and 0-RTT

**What happens.** Chrome and Firefox profiles resume TLS sessions on later
connections to the same host. Safari never resumes over TCP. No TCP
connection sends early (0-RTT) data. Over QUIC, Chrome and Firefox send
their first GET, HEAD or OPTIONS requests early; other methods wait for
the handshake. A request answered `425 Too Early` is sent again once the
handshake completes.

**Why.** A client that never resumes looks unlike a browser, and is
slower.

**How to change it.** No option for TCP. `Config.DisableHTTP3` removes
0-RTT.

## WebSockets

**What happens.** Only when you call `Transport.DialWebSocket`; mimicry
never opens a socket by itself. See [WebSockets](websockets.md).

## Proxies

**What happens.** With `Config.Proxy`, requests go through an HTTP or
SOCKS5 proxy, using the browser's handshake. Credentials go with the first
request. See [Proxies](proxies.md).

**How to change it.** `Config.Proxy`, `Config.BrowserProxyAuth`,
`Config.HTTP3OverSOCKS5` and `Config.MaxIdleProxies`, all described in
[Proxies](proxies.md).

## Certificate pins and checks

**What happens.** Nothing, unless you set `Config.Pins` or
`Config.VerifyConnection`. Then every handshake is checked after the
certificate is verified. A connection that fails is closed before any
request goes on it, and the server sees a TLS alert. A failed HTTP/3
attempt falls back to TCP. `FetchPins` opens one connection to read a
host's certificates and sends no request.

**Why.** Browsers don't pin; this is your choice. It changes what mimicry
accepts, never what it sends.

**How to change it.** Leave both fields nil (the default).

## The last navigation

**What happens.** A Transport remembers its last navigation's URL. A
request without `Request.From` comes from that page: its `Origin`,
`Referer`, `sec-fetch-site` and cookies follow from it.

**How to change it.** Set `Request.From`, or use one Transport per task.

## What mimicry never does

mimicry sends your requests, their redirect hops and their preflights,
and nothing else. It never:
- fetches subresources (stylesheets, scripts, images, favicons) or parses
  HTML;
- runs scripts, so no analytics, beacons or sensor data go out unless you
  send them;
- preconnects, prefetches or prerenders (see the
  [backlog](../backlog.md#connections-and-pool));
- upgrades `http://` to `https://` or applies HSTS;
- caches or revalidates responses;
- looks up DNS HTTPS records;
- fetches OCSP responses, CRLs or intermediate certificates. With
  `Config.RootCAs` nil, the operating system verifies certificates and may
  contact a certificate authority; that traffic never goes to the site.

Background: decisions
[0011](../decisions/0011-connection-partitioning.md),
[0013](../decisions/0013-tls-resumption.md),
[0016](../decisions/0016-literal-urls-no-https-upgrade.md),
[0017](../decisions/0017-cors-preflights.md),
[0019](../decisions/0019-transport-is-one-browsing-entity.md),
[0022](../decisions/0022-redirects-as-the-browser-follows-them.md),
[0023](../decisions/0023-connection-limits-and-coalescing.md),
[0031](../decisions/0031-cookies-for-any-site.md),
[0032](../decisions/0032-certificate-pinning.md),
[0036](../decisions/0036-fast-quic-fallback.md),
[0037](../decisions/0037-coalescing-opt-out.md),
[0038](../decisions/0038-http3-over-socks5.md),
[0039](../decisions/0039-proxy-credentials-up-front.md),
[0040](../decisions/0040-idle-connection-cleanup.md),
[0042](../decisions/0042-http2-opening-records.md),
[0043](../decisions/0043-http3-ack-rate-opt-in.md),
[0045](../decisions/0045-udp-receive-buffer-default.md),
[0046](../decisions/0046-response-decompression.md).
