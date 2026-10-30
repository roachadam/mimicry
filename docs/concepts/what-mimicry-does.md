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

Two behaviours depart from the browser by default, because the browser's
costs a user noticeable time and the difference is unlikely to be
checked: the waits when QUIC fails are shorter
([below](#when-quic-fails-tcp-racing-and-fallback);
`Config.BrowserQUICFallback` restores the browser's), and proxy
credentials go with the first request instead of after the proxy's 407,
which only the proxy can see ([below](#proxies);
`Config.BrowserProxyAuth` restores the browser's).

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
| [Connection reuse and limits](#connection-reuse-and-limits) | fewer connections, queued requests; idle ones closed after 90 s | `Transport.CloseIdleConnections`; `Config.MaxConnsPerHost`; `Config.IdleConnTimeout` |
| [Retries on another connection](#retries-on-another-connection) | the same request again | none |
| [A 30 s request timeout](#a-30-s-request-timeout) | none (a request gives up) | `Config.Timeout`; the client's `Timeout` |
| [HTTP/2 coalescing](#http2-coalescing) | a DNS lookup; Firefox: a connection opened and declined | `Config.DisableCoalescing` (`Config.Resolver` answers the lookup) |
| [HTTP/3](#http3-alt-svc-and-the-switch) | a QUIC connection beside TCP, GOAWAY on TCP (Firefox); with a shared or saved Alt-Svc cache, QUIC from a new Transport's first request | `Config.DisableHTTP3`; `Config.AltSvc`; `Config.UDPReadBufferSize` (receive buffer); `Config.HTTP3ACKEvery` (fewer ACKs, not the browser's) |
| [QUIC failure and TCP fallback](#when-quic-fails-tcp-racing-and-fallback) | TCP connections racing QUIC; by default, sooner than the browser's when QUIC fails | `Config.DisableHTTP3`; `Config.BrowserQUICFallback` |
| [Keep-alive and housekeeping](#keep-alive-pings-and-housekeeping-frames) | PING, WINDOW_UPDATE, ACK frames | none |
| [TLS resumption and 0-RTT](#tls-session-resumption-and-0-rtt) | none (a different ClientHello); a request answered `425 Too Early` sent again | none; `Config.DisableHTTP3` stops 0-RTT |
| [WebSockets](#websockets) | only when you call `Transport.DialWebSocket` | don't call it |
| [Proxies](#proxies) | CONNECT, SOCKS5; with `Config.BrowserProxyAuth`, credential retries; with `Config.HTTP3OverSOCKS5`, a SOCKS5 UDP association per QUIC connection; connections of proxies no longer used closed | `Config.Proxy`; `Config.BrowserProxyAuth`, `Config.HTTP3OverSOCKS5` (off by default); `Config.MaxIdleProxies` |
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
  `SetHTTPCookies`) go only where a browser's cookie of that scope would,
  their name prefixes' requirements applied; `cookies.AnySite` sends them
  to every site instead, a deviation no browser makes
  ([cookies](cookies.md), [decision 0031](../decisions/0031-cookies-for-any-site.md)).
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
  profile's `connections.http1PerHost`, 6 in every browser;
  `Config.MaxConnsPerHost` changes it); further requests wait, first
  come, first served.
- An idle connection is closed 90 s after its last request ended
  (`Config.IdleConnTimeout`): HTTP/2 as the browser lets one go
  (Firefox's with GOAWAY first), HTTP/3 silently, if QUIC's own idle
  timeout (30 s in Chrome and Firefox) has not ended it first. A host with
  no connections is forgotten 90 s later. None of these values is
  measured in a browser
  ([decision 0040](../decisions/0040-idle-connection-cleanup.md)).
- An HTTP/2 connection also closes when the server closes it or a Chrome
  idle PING goes unanswered.
- Chrome profiles put credential-less requests and cross-site frames on
  connections of their own (a partition); see
  [decision 0011](../decisions/0011-connection-partitioning.md).

See [how a request is built](how-a-request-is-built.md#3-the-connection-writes-it)
and [decision 0023](../decisions/0023-connection-limits-and-coalescing.md).

**Why.** Which requests share a connection, and how many connections a
page opens, are visible to the server.

**To turn it off or change it.** `Transport.CloseIdleConnections` closes
idle HTTP/1.1 connections, and HTTP/2 and HTTP/3 connections with nothing
in flight; call it on a client you discard (otherwise its idle
connections close when the garbage collector finds its Transport).
`Config.MaxConnsPerHost` changes the HTTP/1.1 limit per host: no browser
has more than 6 open to one host, so a server counting a client's
concurrent connections sees more as not a browser.
`Config.IdleConnTimeout` sets the idle timeout for every protocol: longer
reuses connections across longer pauses at the cost of memory and,
through a proxy, its connection slots; negative leaves idle connections
to the server, the proxy, QUIC's own idle timeout or
`CloseIdleConnections`. Whatever it says, an unused proxy's connections
close early (see [Proxies](#proxies)). The partition rules have no
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

## A 30 s request timeout

**What happens.** The `http.Client` `NewClient` returns has a 30 s
`Timeout`: the whole request, redirects and reading the body included,
fails after it. A request whose server or connection stops mid-response
fails then instead of waiting forever. `NewTransport` sets none: a client
you build yourself has the timeout you give it, as with net/http.

**Why.** A request that hangs on a stalled connection is a stall the
caller notices; tls-client, httpcloak and azuretls default to 30 s too.
Nothing on the wire changes.

**To turn it off or change it.** `Config.Timeout` (negative for none), or
the returned client's `Timeout`; raise it for large downloads or slow
proxies. A context deadline on the request applies as well.

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

**To turn it off.** `Config.DisableCoalescing` gives every host its own
connection: no lookup, no declined connection. It pays on lossy networks
and costs on clean ones; a server hosting several of a site's hosts at
one address under one certificate then sees a connection per host where
the browser ends with one
([decision 0037](../decisions/0037-coalescing-opt-out.md)).
`Config.Resolver` answers the lookup; a failed lookup only means a new
connection.

## HTTP/3: Alt-Svc and the switch

**What happens.** HTTP/3 is on by default for every built-in profile.
mimicry learns that an origin speaks HTTP/3 only from an `Alt-Svc: h3=…`
header on that origin's own responses, kept until its `ma` expires, per
Transport unless `Config.AltSvc` gives Transports one shared cache (which
can be saved and loaded, as a browser keeps its alternatives across
restarts). A request through a proxy uses HTTP/3 only with
`Config.HTTP3OverSOCKS5` and a SOCKS5 proxy ([below](#proxies)). Each
browser then moves the origin its own way: Chrome from the request after
the Alt-Svc, Firefox at once in the background (then GOAWAY on its HTTP/2
connections), Safari only for a request that needs a new connection. See
[HTTP/3](http3.md#when-a-request-goes-over-http3).

Sent directly, QUIC goes over UDP to the address `Config.Resolver` gives
for the host, and `Config.DialContext` plays no part in it: traffic you
route through `DialContext` does not cover direct HTTP/3. Through a
SOCKS5 proxy, `DialContext` opens the UDP association's control
connection and the proxy resolves the host.

**Why.** A browser that has seen `Alt-Svc` moves to HTTP/3, and a site
behind a CDN sees which protocol its visitors use.

**To turn it off or change it.** `Config.DisableHTTP3` keeps every
request on TCP. `Config.AltSvc` shares, saves, loads and clears what
Transports learnt; a new Transport whose cache knows an origin starts it
as the browser would ([HTTP/3](http3.md#alternatives-a-transport-did-not-learn-itself)).
No option changes the discovery rule or forgets broken origins.
`Config.UDPReadBufferSize` sizes each QUIC socket's receive buffer (by
default 1 MB, what Chrome asks for; nothing on the wire depends on it:
[decision 0045](../decisions/0045-udp-receive-buffer-default.md)).
`Config.HTTP3ACKEvery` acknowledges less often than the browser, which
helps against a server whose one worker a client saturates, but the
server sees a rate no browser sends
([decision 0043](../decisions/0043-http3-ack-rate-opt-in.md)).

## When QUIC fails: TCP racing and fallback

**What happens.** While an origin's QUIC connection is not up, each
browser races or falls back to TCP its own way (the profile's
`http3.discovery.fallback`): Chrome waits 300 ms for the attempt, then
goes on TCP; Firefox never waits and validates QUIC in the background;
Safari tries QUIC first, TCP 100 ms later, and drops the attempt. After
the profile's number of failed attempts in a row, the origin stays on TCP
for the Transport's life.

By default two waits are shorter than the browser's: a request on a
resumed (0-RTT) connection whose server has not answered goes on TCP
after the race delay instead of when the attempt gives up (Chrome 4 s,
Firefox 5 s), and a request whose attempt failed outright goes on TCP at
once. A connection whose server answers is unaffected. See
[HTTP/3, when QUIC fails](http3.md#when-quic-fails),
[fast fallback](http3.md#fast-fallback-the-default) and
[decision 0036](../decisions/0036-fast-quic-fallback.md).

**Why.** A request must not stall behind a QUIC path that a firewall
blocks, and each browser's way out is visible as its connection pattern.
The shorter waits only apply where QUIC is failing, a path anti-bot
systems are not seen to time.

**To turn it off or change it.** `Config.DisableHTTP3`.
`Config.BrowserQUICFallback` keeps the browser's waits. There is no option
for the race itself.

## Keep-alive PINGs and housekeeping frames

**What happens.**
- **HTTP/2:** Chrome profiles send a PING before a request when nothing
  was read for 10 s, and close the connection if its answer does not come
  back within 10 s. Firefox and Safari never ping, as measured. Every
  profile answers the server's PINGs and SETTINGS, and returns flow
  control credit (WINDOW_UPDATE) as its browser does, Chrome also after
  5 s however little was read.
- **HTTP/2 records:** a new connection's preface, SETTINGS and
  WINDOW_UPDATE go out in one TLS record, the first request's frames in
  the next (Chrome, Firefox) or in the same one (Safari),
  as the profile's `http2.openingRecords` says; a request's HEADERS and
  Firefox's stream WINDOW_UPDATE always share a record
  ([decision 0042](../decisions/0042-http2-opening-records.md)).
- **TLS key shares:** Firefox profiles send one X25519 key in both the
  X25519MLKEM768 and the x25519 share of every hello, resumed and QUIC
  included, as Firefox does (`tls.x25519Reuse`).
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

Only the methods the profile's browser sends in 0-RTT go there (GET, HEAD
and OPTIONS, `http3.earlyData.methods`); any other request made while the
handshake runs waits for it and leaves in 1-RTT, as the browser's does. A
request answered `425 Too Early` (RFC 8470) goes again on the same
connection once the handshake is complete; you see the 425 only if its
body cannot be read again (no `GetBody`). See
[HTTP/3](http3.md#the-quic-connection).

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
without the proxy. Credentials in the proxy URL go with the first CONNECT
or request, saving the browser's 407 round trip, which only the proxy can
see ([decision 0039](../decisions/0039-proxy-credentials-up-front.md)).
Each proxy has its own connections and TLS tickets. HTTP/3 goes through a
proxy only with `Config.HTTP3OverSOCKS5`, each QUIC connection over a
SOCKS5 UDP association; no datagram then goes to the server directly. A
proxy that fails a request returns a `*ProxyError`, so a rotation loop
can move on. When you rotate proxies within a Transport, the idle
connections of proxies no longer used close early
([rotating](proxies.md#rotating)). See [Proxies](proxies.md).

**To turn it off or change it.** `Config.Proxy`; a nil URL sends that
request directly. `Config.BrowserProxyAuth` waits for the proxy's 407 as
the browser does. `Config.HTTP3OverSOCKS5` lets SOCKS5 proxies carry
HTTP/3 ([proxies](proxies.md#http3-through-socks5),
[decision 0038](../decisions/0038-http3-over-socks5.md)).
`Config.MaxIdleProxies` sets how many proxies keep their idle connections
([rotating](proxies.md#rotating)).

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
