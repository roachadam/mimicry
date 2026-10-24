# 0006: Header order: a default per kind plus measured variants

- Status: Accepted
- Date: 2026-10-24
- Commits: `31bcd28`, `031ec2b`

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
(carries `Origin` or page-set headers) and redirected. A request uses the
variant its condition matches exactly, else the default. A variant's list
is exactly what the browser sent, so a computed header it lacks is not sent.
Groups whose order agrees with the default fold into it (Chrome and Firefox
mostly have one order); redirects never fold. `*` in a list marks where
page-set headers go.

`harness/cmd/kindsgen` derives all of this and refuses captures it cannot
reconcile. Profile `context.fetch_redirect_drops` was removed: the redirect
variants encode it.

## Consequences

Profiles are large (data, not code). A condition never captured falls back
to the default order: a possible deviation, recorded in the ledger.

## Revisit when

A capture shows an order depending on something outside the four
conditions.
