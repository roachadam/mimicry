# How a request is built

What happens between `client.Do(req)` and bytes on the wire. Rough draft;
the code is the reference (`kinds.go`, `transport.go`, `internal/`).

## 1. The request says how it is made

A plain request is a top-level navigation, as if typed into the address
bar. `mimicry.As(req, mimicry.Fetch)` or `mimicry.With(req,
mimicry.Request{…})` say otherwise: the kind, the page it comes from
(`From`; by default the transport's last navigation), fetchpriority,
crossorigin attribute, credentials mode, no-cors, the stylesheet loading
it. See [decision 0005](../decisions/0005-request-kinds-api.md).

## 2. The transport resolves it against the profile

`Transport.RoundTrip` turns the request into a plan:

1. **Pick the lists.** The navigation's (`headers`, `http1`) or the kind's
   (`kinds.<kind>`); then the variant matching the request's method, its
   URL's relation to the page, whether it is CORS-checked and whether it
   follows a redirect ([0006](../decisions/0006-header-order-variants.md)).
   A list holds names in order, default values, and slots (empty values)
   for headers that depend on the request.
2. **Compute what the page decides** and fill the slots:
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
     fetchpriority high/low (unmeasured is an error,
     [0008](../decisions/0008-unmeasured-is-an-error.md)).
   - A connection partition: Chrome's credential-less requests and
     cross-site frames ([0011](../decisions/0011-connection-partitioning.md)).
3. **Layer the caller's headers.** `Config.Headers` where the request does
   not set the same header; then the request's own headers win, `nil`
   suppresses a default, `HeaderOrderKey` reorders.

`Exact(req)` skips steps 1 and 2: only the caller's headers are sent.

## 3. The connection writes it

The plan rides on the request context to the engine
(`internal/engine/native`), which picks a pooled connection by scheme,
host and partition, or dials one (`internal/dial`, `Config.DialContext`),
handshakes with the profile's ClientHello (`internal/handshake`), and hands
the request to:

- HTTP/2 (`internal/h2`): pseudo-headers in the profile's order, then the
  plan's headers through `internal/headerorder` (page-set headers where the
  list's `*` puts them), cookies split per the profile, HPACK encoded by the
  profile's policy, HEADERS priority with the plan's weight.
- HTTP/1.1 (`internal/h1`): the plan's HTTP/1.1 list, names cased as the
  browser sends them, Host and framing placed; headers the profile does not
  name keep the caller's spelling.

## 4. What checks it

`harness/e2e`: `TestProfilesMatchCaptures` (first request),
`TestHTTP1MatchesBrowserTrace`, `TestCookiesMatchBrowserTrace`,
`TestKindsMatchBrowserTraces` (every request of the kinds page, redirects,
connection sharing), `TestHPACKReproducesBrowserBlocks`.
