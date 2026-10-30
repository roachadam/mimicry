# 0017: CORS preflights as the browser sends them, refusal included

- Status: Accepted
- Date: 2026-10-24
- Commits: `d3eb402`

**Summary.** The maintainer's decision is that cross-origin `Fetch` and
`XHR` requests that need one get an `OPTIONS` preflight as the browser
sends it, cached as the browser caches it, and are refused with
`*CORSError` when the answer does not allow them, because a request with no
preflight before it is one no browser sends. Such requests cost a round
trip more and can fail; `Request.IgnoreCORS` sends them anyway, and
`Exact` requests are never preflighted.

## Problem

Before a cross-origin `fetch()` or XHR with a method other than GET, HEAD
and POST, or with a header the Fetch standard does not safelist, a browser
sends an `OPTIONS` preflight. It sends the request only if the answer
allows it, and it remembers the answer for a while. Until now mimicry sent
the request alone. A server, or anything watching the traffic, sees a
request arrive with no preflight before it: no browser does that. The
`kinds` captures already held preflights (four per sample), and the kinds
test left them out.

## Options

1. **Leave preflights to the caller.** The caller sends the `OPTIONS`
   request with `Exact` and its own headers. Every caller would have to
   rebuild the browser's header list, Origin, cache and refusal rules, and
   most would not. This breaks the API rule that defaults reproduce the
   browser.
2. **Send the preflight but always send the request.** The wire then shows
   requests no browser would send: after a 403, or after an answer that
   does not allow a header.
3. **Send it as the browser does, refusal included.** Preflight when the
   browser would. Cache the answer as the browser does. When the answer
   does not allow the request, return an error and send nothing more. A
   per-request option (`Request.IgnoreCORS`) sends it anyway, so the
   deviation is the caller's, and visible.

## Decision

Option 3, the maintainer's decision.

- **When.** A `Fetch` (not `NoCORS`) or `XHR` request to another origin
  than its own gets a preflight. Its own origin is the page's, or `null`
  after a tainted redirect chain. It needs a method other than GET, HEAD and
  POST, or a CORS-unsafe header. The Fetch standard's lists decide which
  headers are unsafe: the header the page set, or `Config.Headers`, by name
  and value. Forbidden headers and User-Agent never count.
- **What.** The profile's `preflight` kind is derived like every other kind.
  `Access-Control-Request-Method` is the request's method.
  `Access-Control-Request-Headers` lists the unsafe names lowercase,
  sorted, joined by `,`. The preflight never carries credentials. In Chrome
  it goes on the credential-less connection.
- **Cache.** Each entry is keyed by origin, URL and credentials mode. It
  lasts for `Access-Control-Max-Age`, or 5 s without one. Chrome and Safari
  keep one entry per URL and replace it with each answer
  (`context.preflightCache: replace`). Firefox merges what each answer
  allowed (`merge`).
- **Check.** The preflight's status must be 2xx, and
  `Access-Control-Allow-Origin` must be the origin, or `*` for a request
  without credentials. With credentials, `Access-Control-Allow-Credentials`
  must be `true`. The method must be safelisted or allowed (`*` allows any
  method only without credentials). Every unsafe header must be allowed,
  with `*` covering headers only without credentials and never
  `Authorization`. Otherwise the result is `*CORSError` and no request.
- **Headers.** The preflight carries the `Config.Headers` its own lists
  name (a User-Agent or Accept-Language override, as the request carries
  them), never page headers such as an API key.
- **Redirects.** Each hop comes back through `RoundTrip`, so a redirected
  request is preflighted again for its new URL. A CORS-mode 3xx whose
  answer fails the CORS check ends the chain with `*CORSError` (`Redirect`
  set), as every browser in the `cors-edge` capture did (0022).

The actual response's own CORS headers are not checked otherwise. A
browser has already sent that request by then; what it withholds is the
response from the page's script, which mimicry has no counterpart for.

## Evidence

- **`kinds`, `kinds-http1`, `kinds-plain` captures:** the preflight header
  lists, values and weight, per browser, over HTTP/2, HTTP/1.1 and plain
  HTTP (`fetch-json`, `fetch-custom`, same-site and cross-site).
- **The new `cors` scenario**
  ([report](../../internal/harness/reports/cors-2026-10-24.md)): three samples per
  browser, identical within each browser, Safari 26 included. It measured:
  - the joiner and order of `Access-Control-Request-Headers` (`x-a,x-b`);
  - `PUT`;
  - a credentialed request's preflight going without cookies;
  - Max-Age 600 and the 5 s default (the same request at once: cached;
    after 6 s: preflighted again);
  - replace versus merge (after another header's preflight, the first
    request is preflighted again in Chrome and Safari, not in Firefox);
  - refusals (no request after a 403 or a missing allowed header);
  - a 307's second preflight.

## Consequences

- Requests that need a preflight cost a round trip more, as in the browser,
  and can fail with `*CORSError` where they used to succeed. Callers who
  must reach a server that answers preflights wrongly set `IgnoreCORS`.
- A profile without `context.preflightCache` cannot send such requests: an
  error, as for anything not measured
  ([0008](0008-unmeasured-is-an-error.md)).
- `Exact` requests are never preflighted.

## Revisit when

- Long-lived sessions show a Max-Age cap. Browsers cap it (by their source:
  Chrome 2 h, Firefox 24 h, Safari 10 min). That is unmeasured, so mimicry
  honours Max-Age as sent.
- A capture shows Chrome partitioning its preflight cache by top-level site.
- A capture shows a redirect hop's preflight over plain HTTP using another
  list than the first.
