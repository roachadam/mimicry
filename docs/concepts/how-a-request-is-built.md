# How a request is built

What happens between `client.Do(req)` and bytes on the wire. Rough draft;
the code is the reference (`kinds.go`, `transport.go`, `preflight.go`,
`plan.go`, `internal/`).

## 1. The request says how it is made

A plain request is a top-level navigation, as if typed into the address
bar. `mimicry.As(req, mimicry.Fetch)` or `mimicry.With(req,
mimicry.Request{…})` say otherwise: the kind, the page it comes from
(`From`), fetchpriority, crossorigin attribute, credentials mode, no-cors,
the stylesheet loading it. See [decision 0005](../decisions/0005-request-kinds-api.md).

A Transport (and the client `NewClient` builds on one) is one browsing
entity, like one browser tab: a request of another kind than a navigation
without `From` comes from the transport's last navigation, whichever
goroutine made it (or, before any, from the request's own origin). Tasks
browsing in parallel use a client each, or set `From`.

A navigation without `From` is typed into the address bar, so it must be a
GET or HEAD: a POST navigation comes from a page's form and needs `From`
(an API call is a `Fetch`). Combinations no browser makes are errors before
anything is sent: a no-cors fetch with a method other than GET, HEAD or
POST, a beacon that is not a POST, a `Stylesheet` on anything but a font or
an image, a crossorigin attribute on a kind without one, a credentials mode
on anything but Fetch and XHR.

## 2. The transport resolves it against the profile

`Transport.RoundTrip` turns the request into a plan (`resolve`):

1. **Add `Config.Headers`.** Each one the request does not set itself (in
   any case) is added first, so from here on it counts as a header the
   page set: for the variant, for credentials and for the preflight. Once
   the redirect chain has left an origin, `Authorization` and `Cookie`
   from `Config.Headers` are no longer added, even if the chain comes back;
   ones the request sets itself follow `http.Client`.
2. **Pick the lists.** The navigation's (`headers`, `http1`) or the kind's
   (`kinds.<kind>`); then the variant matching the request's method, its
   URL's relation to the page, whether it is CORS-checked, whether it
   carries a header the page set (one the list does not name: `page_set`),
   whether it follows a redirect (and then whether the redirect went to
   another origin, `cross_origin_redirect`, for a navigation whether to
   another site, `cross_site_redirect`, and whether the request it
   answered carried `Origin`, `origin_before_redirect`)
   ([0006](../decisions/0006-header-order-variants.md)), and whether its URL
   is a non-secure origin (plain `http://` to anything but loopback, which
   takes the browser's non-secure lists:
   [0015](../decisions/0015-non-secure-origins.md)).
   A list holds names in order, default values, and slots (empty values)
   for headers that depend on the request. A combination no variant
   matches takes the kind's default order (for a non-secure request, its
   non-secure default), except a redirect's request of a kind whose
   redirects were captured (fetch, images): that is `ErrNotMeasured`. A
   user's navigation (one that keeps the profile's `Sec-Fetch-User`: a
   typed URL, a clicked link or submit button; a script's suppresses it
   with a nil header) whose variant has no `sec-fetch-user` slot was
   measured only on a script's, so it is `ErrNotMeasured` too, and over
   HTTP/1.1 where the HTTP/1.1 list has no slot.
3. **Compute what the page decides** and fill the slots:
   - `sec-fetch-site`: same-origin, same-site (public suffix list,
     schemeful) or cross-site; after a redirect, the most distant relation
     along the chain (`http.Client` sets `req.Response` on each hop, which
     is how the chain is found); for a navigation under
     `redirects.navigation_site` `restart` (Safari 27), only from its last
     redirect to another site on.
   - `sec-fetch-mode`: the kind's, `cors` with a crossorigin attribute,
     `no-cors` for a no-cors fetch.
   - Credentials: navigations, frames, beacons and no-cors elements send
     cookies; CORS element requests only same-origin (unless
     use-credentials); fetch and XHR by their credentials mode. Without
     credentials the `Cookie` header is removed.
   - `Origin`: on non-GET/HEAD, on CORS requests to another origin, on
     same-origin CORS element requests where the browser does
     (`context.same_origin_element_origin`); `null` once a redirect chain is
     tainted, and for a navigation (a form POST kept by a 307 or 308) by
     `redirects.navigation_origin`: also once any redirect left its origin
     (Chrome, Safari).
   - `Referer`: strict-origin-when-cross-origin from the page (or the
     stylesheet, where the browser does), applied hop by hop, so a chain
     that left the page's origin sends only the origin from then on;
     `http.Client`'s redirect Referer is discarded.
   - `sec-fetch-storage-access`: on credentialed cross-site requests, the
     browser's value.
   - `priority` and HTTP/2 weight: the kind's, or its measured
     fetchpriority high/low.
   - A connection partition: Chrome's credential-less requests and
     cross-site frames ([0011](../decisions/0011-connection-partitioning.md)).
4. **Layer the request's own headers.** They win over the profile's
   defaults and `Config.Headers`; a header set to `nil` suppresses a
   default; `HeaderOrderKey` replaces the order, so a profile default it
   does not name is not sent (headers the request sets that it does not
   name still go, after the named ones). `Config.HeaderPlacement` and then
   `Request.HeaderPlacement` (`Place`: one header directly after or before
   another) ride along to the connection, which applies them last
   ([0030](../decisions/0030-header-placement.md)); a request that sets
   `HeaderOrderKey` takes no `Config` placements, and with placements of
   its own is an error (`ErrPlacement`), as is an invalid `Place`.
5. **Preflight it if the browser would.** A cross-origin `fetch()` or XHR
   with a non-safelisted method or header first gets an `OPTIONS`
   preflight (the `preflight` kind), unless a cached answer allows it. The
   preflight carries only the `Config.Headers` the preflight kind's lists
   name (a User-Agent or Accept-Language override), never others such as
   an API key. An answer that does not allow the request stops it with
   `*CORSError` ([CORS preflights](cors-preflights.md),
   [0017](../decisions/0017-cors-preflights.md)).

`Exact(req)` skips steps 2, 3 and 5: only the request's own headers and
`Config.Headers` are sent, in `HeaderOrderKey`'s order or sorted by name,
without `Config.HeaderPlacement` (a `Request.HeaderPlacement` is an
error).

What the profile never measured is an error wrapping
`mimicry.ErrNotMeasured`, not a guess
([0008](../decisions/0008-unmeasured-is-an-error.md)): a kind the profile
lacks; a fetchpriority the kind has no measurement for; a non-secure
request with neither a matching variant nor a non-secure default; a request
that sends Origin when no variant matches and the fallback list has no
Origin slot (a same-site form POST navigation, for one); a CORS preflight
for a profile without `context.preflight_cache`; a variant measured over
HTTP/2 only (no `http1` list) sent on an HTTP/1.1 connection.

## 3. The connection writes it

The plan and the partition ride on the request context to the engine
(`internal/engine/native`). It refuses a host that is not a valid Host
header before dialling, picks the request's proxy, and asks the pool
(`internal/pool`) for a connection by key: scheme, host and port,
partition, and proxy. Per key the pool holds one shared HTTP/2 connection,
the idle HTTP/1.1 ones, and the dial in progress, which concurrent
requests wait for and share, its error included. Once a key's server is
known to speak HTTP/1.1 only, requests dial in parallel up to the
profile's `connections.http1_per_host` (6 in every browser); more wait,
first come first served, for a connection to come back. An idle HTTP/1.1
connection is checked without blocking before reuse (closed by the peer,
or holding bytes nobody asked for: it is closed instead) and closed after
90 s idle; a key with no connections is forgotten after the same time.

A request to a host with no HTTP/2 connection of its own may take another
host's (coalescing), as the profile's `connections.coalesce` says: one in
the same partition, to the same port, not through a proxy, whose
certificate covers the host and passes `Config.Pins` and
`Config.VerifyConnection` for it. With `lookup` (Chrome, Safari) the host is
looked up (`Config.Resolver`) and the connection must reach one of its
addresses: no new connection is opened. With `connect` (Firefox) the host
is dialled and handshaked first; if another connection reaches the same
address, the new one sends its preface and GOAWAY and closes, and the
request goes on the other.

A new connection is dialled (`internal/dial`, `Config.DialContext`, through
the proxy if there is one; a proxy's failure is a `*ProxyError`) and
handshaked with the profile's ClientHello (`internal/handshake`), its
certificate verified against `Config.RootCAs`, then `Config.Pins` and
`Config.VerifyConnection` (every handshake, resumed ones and QUIC's
included; [decision 0032](../decisions/0032-certificate-pinning.md));
`Config.DialTimeout` bounds the connect and, separately, the handshake. A
request nothing of which was sent goes again on another connection. The
response's `TLS` is the connection's state. The request goes to:

- HTTP/2 (`internal/h2`): pseudo-headers in the profile's order, then the
  plan's headers through `internal/headerorder` (page-set headers where the
  list's `*` puts them), cookies split per the profile, HPACK encoded by the
  profile's policy, HEADERS priority with the plan's weight (for Chrome,
  `chain_priorities`: depending exclusively on the newest stream still
  open at the nearest weight at or above it, as Chromium's lists do).
- HTTP/1.1 (`internal/h1`): the plan's HTTP/1.1 list, names cased as the
  browser sends them, Host and framing placed; headers the profile does not
  name keep the caller's spelling. A plan from a variant without an
  `http1` list is refused here (`ErrNotMeasured`).
- HTTP/3 (`internal/h3`): as HTTP/2, with the profile's `http3.headers`
  changes (dropped headers, `alt-used`) and QPACK.

Each writer then applies the request's header placements
(`headerorder.Place`) to the lines it is about to send, whatever put them
there: the profile's list with its page-set run, cookies, framing, and over
HTTP/1.1 Host and Connection too (not `Proxy-Authorization`, which goes
where the browser puts it afterwards). Pseudo-headers are never placed. A
header or anchor the request does not send fails it with `ErrPlacement`
before anything of it is written. Without placements the lines are
untouched.

## 4. The response comes back

With a jar and a credentialed request, the response's `Set-Cookie` lines
go to the jar with the request's context ([cookies](cookies.md)). When the
profile sends `Accept-Encoding` and the caller did not set it (on the
request or in `Config.Headers`), the body is decoded and
`Content-Encoding` and `Content-Length` removed, as a browser would; HEAD
responses, 204 and 304 responses and those with `Content-Length: 0` are
returned untouched. A navigation that succeeded becomes the transport's
last navigation.

## 5. Seeing it before it is sent

`Transport.Plan(req)` takes the same path as `RoundTrip`, steps 1 to 3
down to each connection's writer, and stops before anything goes out
([0034](../decisions/0034-request-plans.md)). It returns:

- the kind, the profile's list it took (`Variant`, a path in the
  profile's JSON such as `kinds.fetch.variants[4]`, or
  `kinds.fetch.headers` when no variant matched) and the request's
  `Condition` (method, site, `cors_checked`, `page_set`, the redirect
  flags, `insecure`);
- the header lines over HTTP/2, HTTP/1.1 and HTTP/3 (when the Transport
  speaks it), each as its writer would put them on the wire:
  pseudo-headers, Host, Connection and framing, casing, cookies from the
  jar, placements; or, per protocol, the error that protocol would give
  (`ErrNotMeasured` for a variant measured over HTTP/2 only, sent over
  HTTP/1.1) or why the request never goes there;
- `Protocol`: the one the request would take now, named as `resp.Proto`
  names it, from the open connections and the Alt-Svc seen (as `quicFor`
  decides), or empty when the server's ALPN answer, a QUIC attempt the
  request would wait for, or a DNS lookup (coalescing) decides it;
- `Preflight`: the CORS preflight's own plan when one would be sent (nil
  when none is needed or the cache allows the request);
- `Overrides`: headers the request or `Config.Headers` set in place of a
  profile default, a computed value (`Referer`, `Origin`,
  `sec-fetch-*`) or the jar's cookies; `Dropped`: profile headers a
  `HeaderOrderKey` leaves out.

Plan changes nothing: it dials nothing and looks nothing up, reads the
jar with `Peek` (no last-access times, expired cookies left), and leaves
the last navigation, the preflight cache, connections and Alt-Svc state as
they were. Its error is the one `RoundTrip` would return before sending:
`ErrNotMeasured`, `ErrPlacement`, a request no browser makes, or a
protocol error when every protocol the request may take now fails. A
`*CORSError` is the server's answer, so Plan never returns one. A plan is
a snapshot: a response setting cookies, a connection opened or closed, or
a navigation in between changes what goes out. `Plan.String()` prints it
all for a log (`ExampleTransport_Plan`).

## 6. What checks it

`harness/e2e`: `TestProfilesMatchCaptures` (first request),
`TestHTTP1MatchesBrowserTrace`, `TestCookiesMatchBrowserTrace`,
`TestKindsMatchBrowserTraces` (every request of the kinds page and its
preflights, redirects, connection sharing),
`TestCookiesScenarioMatchesBrowser` (the cookies page),
`TestCORSMatchesBrowser` (the preflight cache and refusals),
`TestConnectionsMatchBrowser` (the per-host HTTP/1.1 cap and coalescing),
`TestHPACKReproducesBrowserBlocks`. The page replays of the kinds,
headers-order, cors, cors-edge, redirects2, nav-redirect, cookies,
cookies-edge and h3 pages also plan every request just before sending it
and check the plan against what the server received (`checkPlans`). The
resolver's rules by unit tests in
`kinds_test.go`, `transport_test.go` and `preflight_test.go`; the pool's by
`internal/pool`'s, with fake connections and a fake clock; coalescing by
`internal/engine/native`'s, with fake servers and lookups. Header placement
by `TestHeaderPlacement` (`placement_test.go`): each placed request against
the same one unplaced, over HTTP/1.1 and a raw HTTP/2 peer, and its error
paths; over HTTP/3 by `TestHeaderPlacementOverHTTP3` in
`internal/engine/native`, against a QUIC server decoding the field
sections.
