# How a request is built

What happens between `client.Do(req)` and bytes on the wire. Rough draft;
the code is the reference (`kinds.go`, `transport.go`, `preflight.go`,
`internal/`).

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
   whether it follows a redirect
   ([0006](../decisions/0006-header-order-variants.md)), and whether its URL
   is a non-secure origin (plain `http://` to anything but loopback, which
   takes the browser's non-secure lists:
   [0015](../decisions/0015-non-secure-origins.md)).
   A list holds names in order, default values, and slots (empty values)
   for headers that depend on the request. A combination no variant
   matches takes the kind's default order (for a non-secure request, its
   non-secure default).
3. **Compute what the page decides** and fill the slots:
   - `sec-fetch-site`: same-origin, same-site (public suffix list,
     schemeful) or cross-site; after a redirect, the most distant relation
     along the chain (`http.Client` sets `req.Response` on each hop, which
     is how the chain is found).
   - `sec-fetch-mode`: the kind's, `cors` with a crossorigin attribute,
     `no-cors` for a no-cors fetch.
   - Credentials: navigations, frames, beacons and no-cors elements send
     cookies; CORS element requests only same-origin (unless
     use-credentials); fetch and XHR by their credentials mode. Without
     credentials the `Cookie` header is removed.
   - `Origin`: on non-GET/HEAD, on CORS requests to another origin, on
     same-origin CORS element requests where the browser does
     (`context.same_origin_element_origin`); `null` once a redirect chain is
     tainted.
   - `Referer`: strict-origin-when-cross-origin from the page (or the
     stylesheet, where the browser does); `http.Client`'s redirect Referer
     is discarded.
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
   name still go, after the named ones).
5. **Preflight it if the browser would.** A cross-origin `fetch()` or XHR
   with a non-safelisted method or header first gets an `OPTIONS`
   preflight (the `preflight` kind), unless a cached answer allows it. The
   preflight carries only the `Config.Headers` the preflight kind's lists
   name (a User-Agent or Accept-Language override), never others such as
   an API key. An answer that does not allow the request stops it with
   `*CORSError` ([CORS preflights](cors-preflights.md),
   [0017](../decisions/0017-cors-preflights.md)).

`Exact(req)` skips steps 2, 3 and 5: only the request's own headers and
`Config.Headers` are sent, in `HeaderOrderKey`'s order or sorted by name.

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
requests wait for and share, its error included. An idle HTTP/1.1
connection is checked without blocking before reuse (closed by the peer,
or holding bytes nobody asked for: it is closed instead) and closed after
90 s idle; a key with no connections is forgotten after the same time.

A new connection is dialled (`internal/dial`, `Config.DialContext`, through
the proxy if there is one) and handshaked with the profile's ClientHello
(`internal/handshake`); `Config.DialTimeout` bounds the connect and,
separately, the handshake. A request nothing of which was sent goes again
on another connection. The request goes to:

- HTTP/2 (`internal/h2`): pseudo-headers in the profile's order, then the
  plan's headers through `internal/headerorder` (page-set headers where the
  list's `*` puts them), cookies split per the profile, HPACK encoded by the
  profile's policy, HEADERS priority with the plan's weight.
- HTTP/1.1 (`internal/h1`): the plan's HTTP/1.1 list, names cased as the
  browser sends them, Host and framing placed; headers the profile does not
  name keep the caller's spelling. A plan from a variant without an
  `http1` list is refused here (`ErrNotMeasured`).

## 4. The response comes back

With a jar and a credentialed request, the response's `Set-Cookie` lines
go to the jar with the request's context ([cookies](cookies.md)). When the
profile sends `Accept-Encoding` and the caller did not set it (on the
request or in `Config.Headers`), the body is decoded and
`Content-Encoding` and `Content-Length` removed, as a browser would; HEAD
responses, 204 and 304 responses and those with `Content-Length: 0` are
returned untouched. A navigation that succeeded becomes the transport's
last navigation.

## 5. What checks it

`harness/e2e`: `TestProfilesMatchCaptures` (first request),
`TestHTTP1MatchesBrowserTrace`, `TestCookiesMatchBrowserTrace`,
`TestKindsMatchBrowserTraces` (every request of the kinds page and its
preflights, redirects, connection sharing),
`TestCookiesScenarioMatchesBrowser` (the cookies page),
`TestCORSMatchesBrowser` (the preflight cache and refusals),
`TestHPACKReproducesBrowserBlocks`. The resolver's rules by unit tests in
`kinds_test.go`, `transport_test.go` and `preflight_test.go`; the pool's by
`internal/pool`'s, with fake connections and a fake clock.
