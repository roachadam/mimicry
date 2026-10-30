# 0005: Request kinds: defaults exact, deviations visible

- Status: Accepted; amended 2026-10-28 by [0036](0036-fast-quic-fallback.md) (fast QUIC fallback by default) and [0039](0039-proxy-credentials-up-front.md) (proxy credentials up front by default)
- Date: 2026-10-23 (designed), 2026-10-24 (built)
- Commits: `157e4c6`, `9594737`, `3fa3983`

**Summary.** The maintainer's decision is that callers state how a request
is made (a kind such as `Script`, `Image` or `Fetch`, and the page it comes
from) and the profile supplies the headers, order and priority, with the
rule that defaults reproduce the browser exactly and every deviation is
visibly the caller's. The reason is that sending every request as a
navigation, or guessing the kind from headers, is detectable when automating
user actions on sites. Since 0036 and 0039 two defaults deviate, each with
an option restoring the browser's behaviour: a faster QUIC fallback
(`Config.BrowserQUICFallback`) and proxy credentials sent up front
(`Config.BrowserProxyAuth`).

## Problem

Browsers send a script, an image, a `fetch()` or a frame with different
headers, header order, priority and HTTP/2 weight, and compute
`sec-fetch-site`, `Origin` and `Referer` from the page making the request.
A client that sends every request as a navigation is detectable on its
second request.

## Options

- **Infer the kind from headers** (httpcloak's fetch modes). Reviewed as the
  counter-example: it defaults `sec-fetch-site` to none/cross-site without
  knowing the page, never sends `Origin`, and cannot remove one header.
- **Callers set every header themselves** (tls-client). Correct only if the
  caller knows each browser's rules.
- **The caller states how the request is made; the profile supplies the
  rest.**

## Decision

Kinds are named after the HTML or JavaScript they imitate: `Navigate` (the
default, so a plain request is unchanged), `Frame`, `Script`,
`AsyncScript`, `DeferScript`, `InjectedScript`, `PreloadScript`, `Style`,
`Image`, `InjectedImage`, `Font`, `PreloadFont`, `Fetch`, `XHR`, `Beacon`.
They are named by how a request is made, not by resource type, because
priority depends on it: a blocking script and an async one differ, an
`<img>` in the HTML and one made by script differ.

`mimicry.As(req, kind)` for the common case; `mimicry.With(req,
Request{Kind, From, Priority, CrossOrigin, Credentials, NoCORS,
Stylesheet, IgnoreCORS})` for full control; `mimicry.Exact(req)` sends only
the caller's headers. Options are a struct, not functional options.

The rule: **defaults reproduce the browser exactly; every deviation is
visibly the caller's.** Precedence, lowest first:

1. the profile's defaults for the kind;
2. what the page decides: `sec-fetch-site` (schemeful, public suffix list),
   `sec-fetch-mode`, `Origin`, `Referer` (strict-origin-when-cross-origin),
   `sec-fetch-storage-access`, credentials (cookies dropped when the mode
   sends none), recomputed along a redirect chain;
3. `Config.Headers`, the caller's own headers for every request (an API
   key), distinct from profile defaults. They count as headers the page
   set (variant choice, preflights, credentials); their `Authorization`
   and `Cookie` are dropped once a redirect chain crossed origins;
4. the request's own headers: a value wins, `nil` removes one header,
   `HeaderOrderKey` reorders (and drops the profile defaults it does not
   name);
5. `Config.HeaderPlacement`, then `Request.HeaderPlacement`: one header
   moved next to another, the rest in the profile's order
   ([0030](0030-header-placement.md)).

Without `From`, other kinds come from the transport's last navigation, else
from the target's own origin: a Transport is one browsing entity
([0019](0019-transport-is-one-browsing-entity.md)). A navigation that is not
a GET needs `From`, the page whose form sends it (0008).

## Evidence

The kinds capture page (`internal/harness/reports/kinds-2026-10-23.md`).
`TestKindsMatchBrowserTraces` makes every request of that page through
mimicry and compares it with the browser's.

Additions found while building: `Stylesheet` (Chrome and Firefox refer a
CSS-loaded font to its stylesheet, Safari to the page); `http.Client`'s
redirect `Referer` is discarded (browsers keep the page's); over HTTP/1.1 a
page-set header keeps the caller's spelling, as browsers keep the page's.

## Consequences

Users describe intent, not headers. `Config.Engine`, which could not carry
the kinds' lists to an outside engine, was removed
([0018](0018-no-public-engine.md)).

## Revisit when

A browser behaviour depends on something `Request` cannot express (user
activation, the top-level site of a frame's own requests).

## Amendment 2026-10-28: two default deviations

Two defaults deviate from the browser, each with an option that restores
it:
- where QUIC fails, requests by default wait less than the browser's do;
  `Config.BrowserQUICFallback` asks for the browser's timing. Nothing a
  server that answers sees differs ([0036](0036-fast-quic-fallback.md));
- an HTTP proxy gets the URL's credentials on the first request, not after
  its 407; `Config.BrowserProxyAuth` asks for the browser's flow. Only the
  proxy sees the difference ([0039](0039-proxy-credentials-up-front.md)).
