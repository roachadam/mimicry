# What mimicry does on the wire

You call `client.Do(req)`. mimicry sends that request the way the
profile's browser would. A browser also does other things around a request
(following redirects, sending CORS preflights, reusing and opening
connections, switching to HTTP/3), and so does mimicry. This page lists
every one of them: what goes on the wire, why (the browser does it), and
the option that turns it off or changes it, if there is one.

Nothing here happens behind your back in the sense of extra page loads:
mimicry sends no request you did not ask for, apart from redirect hops and
preflights (see [what mimicry never does](#what-mimicry-never-does)). The
rest is connection-level traffic, as a browser makes it.

Where no option exists, the [backlog](../backlog.md#options-that-do-not-exist)
lists it as a candidate. Browsers have no such switches either, so none is
needed to look like one.

To see what a request will send before sending it, `Transport.Plan(req)`
returns its header lines for each protocol, the protocol it would take
now, its preflight if one would go first, and the headers you set in
place of the browser's own. It sends nothing and changes nothing: no
connection, no DNS lookup, no cookie touched, no preflight cached
([how a request is built](how-a-request-is-built.md#5-seeing-it-before-it-is-sent),
[decision 0034](../decisions/0034-request-plans.md)).

## At a glance

| Behaviour | Extra traffic | Turn it off or change it |
|---|---|---|
| [Redirects](#redirects) | one request per hop | `http.Client.CheckRedirect` |
| [CORS preflights](#cors-preflights) | an `OPTIONS` before some cross-origin `Fetch`/`XHR` | `Exact`; `Request.NoCORS` (fetch only) |
| [Cookies](#cookies) | none (headers only) | `Config.Cookies`; a `Cookie` header on the request; `Request.Credentials` |
| [Response decoding](#response-decoding) | none | set `Accept-Encoding` yourself |
| [Connection reuse and limits](#connection-reuse-and-limits) | fewer connections, queued requests | `Transport.CloseIdleConnections`; no other option |
| [Retries on another connection](#retries-on-another-connection) | the same request again | none |
| [HTTP/2 coalescing](#http2-coalescing) | a DNS lookup; Firefox: a connection opened and declined | none (`Config.Resolver` answers the lookup) |
| [HTTP/3](#http3-alt-svc-and-the-switch) | a QUIC connection beside TCP, GOAWAY on TCP (Firefox) | `Config.DisableHTTP3` |
| [QUIC failure and TCP fallback](#when-quic-fails-tcp-racing-and-fallback) | TCP connections racing QUIC | `Config.DisableHTTP3` |
| [Keep-alive and housekeeping](#keep-alive-pings-and-housekeeping-frames) | PING, WINDOW_UPDATE, ACK frames | none |
| [TLS resumption and 0-RTT](#tls-session-resumption-and-0-rtt) | none (a different ClientHello); a request answered `425 Too Early` sent again | none; `Config.DisableHTTP3` stops 0-RTT |
| [WebSockets](#websockets) | only when you call `Transport.DialWebSocket` | don't call it |
| [Proxies](#proxies) | CONNECT, SOCKS5, credential retries | `Config.Proxy` |
| [Certificate pins and checks](#certificate-pins-and-checks) | none unless set; a refused connection ends with a TLS alert; `FetchPins` makes a handshake | `Config.Pins`, `Config.VerifyConnection` (both off by default) |
| [The last navigation](#the-last-navigation) | none (header values) | `Request.From` |

## Redirects

**What happens.** A `3xx` with a `Location` is followed by `http.Client`,
and each hop goes back through the Transport, which sends it the way the
browser does:
- a PUT, DELETE or PATCH answered 301 or 302 keeps its method and body
  (`http.Client` alone would turn it into a GET);
- a POST answered 301 or 302, and anything but GET or HEAD answered 303,
  becomes a GET without its body;
- each hop gets its own headers (`Referer`, `Origin`, `sec-fetch-site`),
  its cookies from the jar, and the response's `Set-Cookie` lines go back
  to the jar;
- a hop to another origin may need a [preflight](#cors-preflights) of its
  own.

A chain stops with an error after 20 redirects, or after the profile's
`redirects.navigationLimit` for a navigation (Chrome 19, the others 20).
A `Fetch`, `XHR` or CORS-mode element request to another origin whose
redirect does not allow its origin returns a `*CORSError`, and the next
URL is never requested. See
[decision 0022](../decisions/0022-redirects-as-the-browser-follows-them.md)
and [CORS preflights](cors-preflights.md#redirects).

**Why.** The browser follows redirects itself, and a server can see each
hop's headers and timing.

**To turn it off or change it.** Redirects are `http.Client`'s, not the
Transport's: the Transport never follows a redirect on its own.
`NewClient` sets a `CheckRedirect` that lifts `http.Client`'s limit of 10;
set your own on the returned client to stop earlier:

```go
client.CheckRedirect = func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse // return the 3xx itself
}
```

A refused CORS redirect still returns its `*CORSError`, since that check
runs in the Transport; `Request.IgnoreCORS` returns or follows it instead.
There is no way to raise the limit above the browser's.

## CORS preflights

**What happens.** Before a `Fetch` (not `NoCORS`) or `XHR` to another
origin than its page, with a method other than GET, HEAD or POST or a
header the page set that the Fetch standard does not safelist (a JSON
`Content-Type`, `Authorization`, any `X-…` header), mimicry sends an
`OPTIONS` request first, as the browser does. If the answer does not
allow the request, `client.Do` returns a `*CORSError` and the request is
never sent. Answers are cached per Transport, for `Access-Control-Max-Age`
or 5 s. Firefox and Safari also preflight a redirect hop because the
request before it was (`redirects.hopPreflight`). See
[CORS preflights](cors-preflights.md) and
[decision 0017](../decisions/0017-cors-preflights.md).

**Why.** A browser always preflights these requests. A cross-origin API
call that arrives without its preflight is a request no browser makes.

**To turn it off or change it.**
- `Request.IgnoreCORS` sends the request even when the answer refuses it.
  The preflight is still sent.
- `Exact(req)` sends no preflight, but also drops the profile's headers:
  only the request's own headers and `Config.Headers` go.
- `Request.NoCORS` makes a `Fetch` a `no-cors` fetch, which is never
  preflighted (GET, HEAD and POST only). It also changes the request's
  `sec-fetch-mode` and credentials.
- A request from a page on the same origin (`Request.From`) needs none.

There is no switch that skips the preflight while keeping the profile's
headers.

## Cookies

**What happens.** With a jar, each request carries the cookies the
browser would send (SameSite, partitions, prefixes and order per the
profile), and each response's `Set-Cookie` lines are stored with the
request's context, redirect hops included. No extra traffic: only the
`Cookie` header changes. See [cookies](cookies.md).

**Why.** The cookies a site set coming back as the browser would send
them is one of the things anti-bot systems check.

**To turn it off or change it.**
- `NewClient` creates a jar with the profile's rules unless
  `Config.Cookies` holds one; `Transport.Cookies()` returns it, to
  inspect, seed or persist it, and passing your own shares it between
  Transports.
- Cookies you seed (`Set`, `SetString`, `SetHostString`,
  `SetHTTPCookies`) go only where a browser's cookie of that scope would:
  `SetString`'s to the name given and the names under it (its `Domain`,
  never widened to the site), `SetHostString`'s to one host. Their name
  prefixes get what browsers require without being asked: a `__Host-`
  cookie is stored host-only on the name given, `Secure`, path `/`; a
  `__Secure-` one `Secure`. `cookies.AnySite` as `SetString`'s domain
  sends them to every site instead, a deviation no browser makes (decision
  [0031](../decisions/0031-cookies-for-any-site.md)).
- `NewTransport` with a nil `Config.Cookies` keeps no cookies: an
  `http.Client` `Jar` or your own `Cookie` headers are then up to you.
- A request that sets its own `Cookie` header sends that instead; one set
  with no values sends none.
- `Request.Credentials` (`CredentialsOmit`, `CredentialsInclude`) sets a
  `Fetch` or `XHR` request's credentials mode, as in the browser.

## Response decoding

**What happens.** When the profile sends `Accept-Encoding` and you did
not set it, the response body is decompressed (gzip, deflate, Brotli,
zstd, as the profile asks) and `Content-Encoding` and `Content-Length`
are removed. HEAD, 204, 304 and empty responses are left as they are.
Nothing changes on the wire.

**To turn it off.** Set `Accept-Encoding` yourself, on the request or in
`Config.Headers`. Set it to the profile's own value to keep the wire
identical and get the raw body.

## Connection reuse and limits

**What happens.** Connections are pooled per scheme, host, port,
partition and proxy:
- One HTTP/2 connection per host carries every request to it.
- HTTP/1.1 connections are reused one request at a time. Once a host is
  known to speak HTTP/1.1 only, at most 6 are open to it at once (the
  profile's `connections.http1PerHost`, 6 in every browser); further
  requests wait, first come, first served.
- An idle HTTP/1.1 connection is checked before reuse and closed after
  90 s idle.
- An HTTP/2 connection stays open until the server closes it, a Chrome
  idle PING goes unanswered, or you close it.
- Chrome profiles put credential-less requests and cross-site frames on
  connections of their own (a partition); see
  [decision 0011](../decisions/0011-connection-partitioning.md).

See [how a request is built](how-a-request-is-built.md#3-the-connection-writes-it)
and [decision 0023](../decisions/0023-connection-limits-and-coalescing.md).

**Why.** Which requests share a connection, and how many connections a
page opens, are visible to the server.

**To turn it off or change it.** `Transport.CloseIdleConnections` closes
idle HTTP/1.1 connections, and HTTP/2 and HTTP/3 connections with nothing
in flight. The limit, the idle timeout and the partition rules have no
option. Connections are never partitioned by top-level site: a Transport
is one browsing session ([decision 0019](../decisions/0019-transport-is-one-browsing-entity.md)),
so when visiting unrelated sites that share third-party hosts, use one
Transport per site (they can share one jar through `Config.Cookies`).

## Retries on another connection

**What happens.** A request goes again on another connection, up to 5
attempts in all, when:
- nothing of it was sent (the connection was going away), or the server
  refused it unprocessed (HTTP/2 REFUSED_STREAM, HTTP/3 request rejected);
- it is idempotent (GET, HEAD, OPTIONS, TRACE, PUT, DELETE) with no body
  or one `GetBody` can recreate, and failed on a reused HTTP/1.1
  connection the server had closed, or its HTTP/3 connection ended before
  the response.

A POST that may have reached the server is never sent twice.

**Why.** Browsers do the same: a server closing an idle connection as a
request goes out is normal, and the user never sees it.

**To turn it off.** No option.

## HTTP/2 coalescing

**What happens.** A request to a host with no HTTP/2 connection of its
own may go on another host's connection, if that connection's certificate
covers the host, on the same port and partition, not through a proxy:
- Chrome and Safari (`lookup`): mimicry resolves the host
  (`Config.Resolver`) and uses an existing connection that reaches one of
  its addresses. No new connection is opened.
- Firefox (`connect`): mimicry dials the host and does the handshake
  first; if an existing connection reaches the same address, the new one
  sends its preface and a GOAWAY, closes, and the request goes on the old
  one. That is one extra connection, as Firefox makes it.

**Why.** Browsers coalesce, so a site whose hosts share a certificate and
an address sees one connection from them.

**To turn it off or change it.** No option. `Config.Resolver` answers the
lookup; a failed lookup only means a new connection.

## HTTP/3: Alt-Svc and the switch

**What happens.** HTTP/3 is on by default for every built-in profile.
mimicry learns that an origin speaks HTTP/3 only from an `Alt-Svc: h3=…`
header on that origin's own responses (same host; the port may differ),
kept until its `ma` expires. Requests sent through a proxy never use
HTTP/3. Then each browser moves the origin as it does:
- **Chrome:** the next request still goes on HTTP/2 and starts a QUIC
  connection beside it; requests after that use HTTP/3 once it is up. The
  HTTP/2 connection stays open, unused.
- **Firefox:** the response carrying `Alt-Svc` starts a QUIC connection at
  once, in the background; requests stay on TCP until it is up. Then each
  HTTP/2 connection to the origin gets a GOAWAY from mimicry.
- **Safari:** only a request that needs a new connection uses HTTP/3; a
  live HTTP/2 connection is kept.

QUIC goes over UDP to the address `Config.Resolver` gives for the host.
`Config.DialContext` plays no part in it, so traffic you route through
`DialContext` does not cover HTTP/3. See [HTTP/3](http3.md).

**Why.** A browser that has seen `Alt-Svc` moves to HTTP/3, and a site
behind a CDN sees which protocol its visitors use.

**To turn it off.** `Config.DisableHTTP3` keeps every request on TCP. No
option changes the discovery rule, or reads, seeds or clears the
`Alt-Svc` cache.

## When QUIC fails: TCP racing and fallback

**What happens.** While an origin's QUIC connection is not up, each
browser races or falls back to TCP in its own way (the profile's
`http3.discovery.fallback`):
- **Chrome:** a request waits 300 ms for the QUIC attempt, then goes on
  TCP while the attempt continues.
- **Firefox:** requests never wait (except while a resumed attempt after
  the server's close is pending), and the attempt runs in the background.
  When a QUIC connection dies with requests pending, Firefox opens a TCP
  connection per pending request, sends them all on the first, and
  declines the others; mimicry does the same.
- **Safari:** QUIC first, TCP 100 ms later, and the QUIC attempt is
  dropped once the request goes on TCP.

After the profile's number of failed attempts in a row, the origin stays
on TCP for the Transport's life. A live Chrome connection whose path stops
answering moves to a new UDP port. See
[HTTP/3, when QUIC fails](http3.md#when-quic-fails).

**Why.** A request must not stall behind a QUIC path that a firewall
blocks, and each browser's way out is visible as its connection pattern.

**To turn it off.** `Config.DisableHTTP3`. There is no option for the race
itself.

## Keep-alive PINGs and housekeeping frames

**What happens.**
- **HTTP/2:** Chrome profiles send a PING before a request when nothing
  was read for 10 s, and close the connection if its answer does not come
  back within 10 s. Firefox and Safari never ping, as measured. Every
  profile answers the server's PINGs and SETTINGS, and returns flow
  control credit (WINDOW_UPDATE) as its browser does, Chrome also after
  5 s however little was read.
- **QUIC:** while a request waits on a quiet connection, PINGs keep it
  alive (Chrome and Firefox after 15 s, Safari every 2 s, or half the
  server's idle timeout if that is shorter). An idle connection with no
  request waiting ends silently at its idle timeout. ACKs, credit and
  connection IDs follow each browser's rules.

None of this is periodic background traffic on an idle connection.

**Why.** These frames are part of each browser's HTTP/2 and QUIC
fingerprint.

**To turn it off.** No option.

## TLS session resumption and 0-RTT

**What happens.** Chrome and Firefox profiles keep the server's TLS
session tickets and resume a later connection to the same host (one
ticket per connection), with the browser's resumed ClientHello. Safari
never resumes over TCP. Tickets are kept per Transport, per host, port,
partition and proxy. No TCP connection ever sends 0-RTT data, as no
browser did. Over QUIC, Chrome and Firefox profiles resume and send their
first requests in 0-RTT; Safari's QUIC connections are always fresh. See
[decision 0013](../decisions/0013-tls-resumption.md).

In 0-RTT go only the methods the profile's browser sends there (GET, HEAD
and OPTIONS, `http3.earlyData.methods`). A request with any other method
(a POST, PUT or DELETE, a form submission) made while a resumed
connection's handshake is in progress waits for it and leaves in 1-RTT:
for Firefox in the datagram that carries the client's Finished, for
Chrome in one of its own right after it. A request mimicry sent in 0-RTT
that the server answers `425 Too Early` (RFC 8470) goes again on the same
connection once the handshake is complete, and you get the response to
that; you see the 425 only if the request's body cannot be read again
(no `GetBody`). Chrome, Firefox and Safari all did both on the
`h3-early` and `h3-too-early` pages
([report](../../internal/harness/reports/h3-early-2026-10-26.md)).

**Why.** A client that never resumes looks unlike a browser on its second
connection, and is slower. Browsers keep requests that change state out
of early data, which an attacker can replay, and a POST in 0-RTT would
stand out.

**To turn it off or change it.** No option for TCP resumption; a new
Transport starts without tickets. `Config.DisableHTTP3` removes 0-RTT.

## WebSockets

**What happens.** Only when you call `Transport.DialWebSocket`. The
handshake is the profile's `websocket` request kind, over an extended
CONNECT stream on the host's HTTP/2 connection when the server allows it,
otherwise on an HTTP/1.1 connection of its own with the browser's socket
ClientHello.
Then, as the browser: every frame masked, messages compressed when the
server accepted permessage-deflate, pings answered at once, and the close
handshake ending the transport Chrome's, Safari's or Firefox's way.
Through `Config.Proxy`, a socket of its own gets a tunnel of its own (or,
Safari's `ws://`, its handshake goes to the proxy whole). See
[WebSockets](websockets.md).

**To turn it off.** Don't call `Transport.DialWebSocket`; mimicry never
opens a socket by itself.

## Proxies

**What happens.** With `Config.Proxy`, an `https://` request goes through
an HTTP proxy's CONNECT tunnel or a SOCKS5 handshake, and an `http://`
request goes to an HTTP proxy whole. The proxy sees the profile's
browser's CONNECT lines and SOCKS5 bytes; the server sees what it would
without the proxy. With credentials in the proxy URL, Chrome and Firefox
send the first request without them, answer the 407 once on the same
connection, and send them unasked from then on; Safari waits for a 407 on
every CONNECT, and sends them on plain-HTTP requests from the first.
Each proxy has its own connections and TLS tickets (a Firefox profile
resumes no session through an HTTP proxy's CONNECT tunnel, as Firefox
does not), and HTTP/3 is never
used through a proxy. A proxy that fails a request (unreachable, refusing
the tunnel or the credentials, too slow) returns a `*ProxyError`, so a
rotation loop can move on; nothing more is sent through it for that
request. See [proxies](proxies.md#when-the-proxy-fails).

**To turn it off or change it.** `Config.Proxy`; a nil URL sends that
request directly.

## Certificate pins and checks

**What happens.** Nothing, unless you set `Config.Pins` or
`Config.VerifyConnection`; then every TLS and QUIC handshake (resumed
ones too, WebSockets and proxied connections included) is checked after
the certificate is verified, and before a request goes on another host's
HTTP/2 connection (coalescing). A connection that fails the check is
closed before any request goes on it: over TLS the server sees an alert
(`bad_certificate`) where the browser would have gone on, and an HTTP/3
attempt that fails falls back to TCP as a failed QUIC connection does
(whose own handshake is checked too). `FetchPins`, and `cmd/pins` built
on it, open one connection with the profile's hello and close it after
the handshake, without a request. Every response carries its
connection's state in `resp.TLS`. See
[decision 0032](../decisions/0032-certificate-pinning.md).

**Why.** Browsers do not pin, so this is your choice, not the browser's:
it changes what mimicry accepts, never what it sends to a server that
passes.

**To turn it off or change it.** Leave `Config.Pins` and
`Config.VerifyConnection` nil (the default).

## The last navigation

**What happens.** A Transport remembers the URL of its last navigation.
A request of another kind without `Request.From` comes from that page:
its `Origin`, `Referer`, `sec-fetch-site` and cookies follow from it. No
extra traffic, but the header values depend on what the Transport sent
before.

**To change it.** Set `Request.From`, or use one Transport per task
([decision 0019](../decisions/0019-transport-is-one-browsing-entity.md)).

## What mimicry never does

mimicry sends the requests you make, their redirect hops (through
`http.Client`) and their CORS preflights, and nothing else. In particular
it never:
- fetches a page's subresources (stylesheets, scripts, images, fonts,
  favicons) or parses HTML: it has no HTML parser and no request of its
  own outside the paths above;
- runs scripts, so nothing a page's JavaScript would request
  (analytics, beacons, fetches, sensor data) is sent unless you send it;
- preconnects, prefetches or prerenders (Chrome's speculative preconnect
  is a known difference: [backlog](../backlog.md#connections-and-pool));
- upgrades `http://` to `https://` or applies HSTS
  ([decision 0016](../decisions/0016-literal-urls-no-https-upgrade.md));
- caches responses or revalidates them;
- looks up DNS HTTPS records;
- fetches OCSP responses, CRLs or intermediate certificates itself. (With
  `Config.RootCAs` nil, Go hands verification to the operating system's
  verifier, which on macOS may contact a certificate authority on its
  own; that traffic never goes to the site.)
- opens a WebSocket unless you call `Transport.DialWebSocket`.

The traffic it does add on its own is connection-level: connections
opened, coalesced, declined or closed, QUIC attempts and their TCP races,
GOAWAY, PING, WINDOW_UPDATE and ACK frames, the retries above, and a
request answered `425 Too Early` sent again
([0-RTT](#tls-session-resumption-and-0-rtt)).
