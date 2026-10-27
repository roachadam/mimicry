# 0006: Header order: a default per kind plus measured variants

- Status: Accepted
- Date: 2026-10-24
- Commits: `31bcd28`, `031ec2b`; extended in the 2026-10-24 review (`pageSet`, variants measured over HTTP/2 only)

## Problem

The plan was one header list per kind, with empty "slots" for headers that
come and go. Deriving the lists from captures failed: the browsers' own
requests of one kind could not be put in one order.

## Evidence

- Safari: a request that carries `Origin` or a page-set header uses a
  different order (`sec-fetch-site, accept, origin, sec-fetch-mode, …`)
  from one that does not (`sec-fetch-dest, user-agent, accept, …`); a POST
  another; a cross-site beacon differs from a same-site one; redirected
  requests differ by where they land and drop `sec-fetch-*`,
  `accept-language` and `priority`.
- Firefox: a form POST navigation moves `referer` and `cookie`; its
  cross-origin fetches and preload fonts have orders of their own.
- Chrome and Firefox: a redirected request orders `origin` differently.
- Safari: a cross-site `PUT` places `content-length` differently when the
  page set a header of its own than when it did not (the `cors` capture),
  though both carry `Origin`. So "CORS-checked" alone cannot pick the path.
- All stable across three samples.

Browsers build headers along different code paths; the order records which
path ran.

## Options

- One merged list per kind, accepting mismatches. Rejected: visible.
- Per-request-label lists. Too specific to our capture page.
- A default order plus variants keyed by the conditions that pick the path.

## Decision

A kind (and the navigation) has a default order, for a same-origin GET that
is not CORS-checked, plus `variants` keyed by method, site relation
(the request URL's own: same-origin, same-site, cross-site), CORS-checked
(carries `Origin` or page-set headers), page-set (carries a header the page
set itself; implies CORS-checked), redirected and non-secure (0015). A
request uses the variant its condition matches exactly, else the default.
A variant's list is exactly what the browser sent, so a computed header it
lacks is not sent. Groups whose order agrees with the default fold into
it (Chrome and Firefox mostly have one order); redirects never fold. `*`
in a list marks where page-set headers go.

A variant derived only from HTTP/2 captures (the `cors` and `cookies`
pages run over HTTP/2) has no HTTP/1.1 list. Sent over HTTP/1.1 it is not
measured, and the connection refuses it (0008).

Redirect hops have conditions of their own, each found by testing which
one explains every captured hop (`cors-edge`, `redirects2`,
`nav-redirect-*`):
- `crossOriginRedirect`: the redirect left the origin of the request it
  answered;
- `originBeforeRedirect`: that request carried `Origin`;
- `methodChangedByRedirect`: Chrome places `Origin` differently on a
  GET a redirect made from another method;
- `insecureBeforeRedirect`: the previous request was plain http.

A typed navigation's hop has site `none`. For fetch, images and
navigations, a hop whose combination was not captured is `ErrNotMeasured`,
never the kind's default order: every browser reorders hops. Page-set
headers follow 0021's table where the browser uses one.

`harness/cmd/kindsgen` derives all of this and refuses captures it cannot
reconcile. Profile `context.fetch_redirect_drops` was removed: the redirect
variants encode it.

## Consequences

Profiles are large (data, not code). A condition never captured falls back
to the default order, except where that order has no place for a header
the request computes (`Origin` on a POST navigation that falls back to the
GET list): then it is an error (0008), never a dropped header.

## Revisit when

A capture shows an order depending on something outside these conditions.
One already has: Chrome and Safari place a page-set header by its name
(`x-a` and `x-b` land elsewhere than `x-mimicry`), which one `*` slot cannot
express. Until a capture of many names finds the rule, every page-set
header goes where `X-Mimicry` went, a gap listed in the ledger.
