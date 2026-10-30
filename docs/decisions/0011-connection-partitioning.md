# 0011: Model connection partitioning only where a rule is measured

- Status: Accepted; amended 2026-10-24 (top-level-site partitioning measured, not modelled); amended 2026-10-25 by [0029](0029-scope-what-gets-modelled.md) (top-level-site partition deferred); amended 2026-10-28 (the shared Alt-Svc cache is not partitioned by site either)
- Date: 2026-10-24
- Commits: `b4f5f79`

## Problem

Browsers do not always reuse a connection to the same host: which requests
share one is visible to a server.

## Evidence

Kinds captures, three samples each, stable:

- Chrome: requests sent without credentials use connections of their own
  (they group exactly as mimicry's credentials rule computes); a cross-site
  frame's navigation uses its own; same-site frames share the page's.
- Safari: one connection per host.
- Firefox: credential-less requests get their own connection to the
  same-site host only, and share one to the page's own and to the
  cross-site host. No rule explains it.

## Decision

Model Chrome (profile `context.anonymousConnections`,
`frameConnections`; a partition in the pool key). Safari needs nothing.
Do not model Firefox: guessing a rule from one page would be wrong
somewhere else. `TestKindsMatchBrowserTraces` checks which requests share a
connection and logs Firefox's difference as a known gap.

## Revisit when

A capture designed for it (more hosts, orders, timings) explains Firefox's
grouping; or a cross-site frame's own requests are modelled.

## Amendment 2026-10-24: partitioning by top-level site, measured

The `redirects2` captures show every browser sending a
navigation to another site on a connection of its own, not on one the
previous page used. That is partitioning by top-level site. It is not
modelled: it changes the pool key for every request a page makes, and
needs its own capture (which requests of a page share the top-level key,
per browser) and decision. `TestRedirects2MatchesBrowser` compares those
requests apart from their connections and lists the gap.

## Amendment 2026-10-25: top-level-site partition deferred

By [0029](0029-scope-what-gets-modelled.md), no
top-level-site partition until a capture or a vendor shows it matters
for detection. A Transport represents one browsing session
([0019](0019-transport-is-one-browsing-entity.md)). Callers who visit
unrelated sites that share third-party hosts (a CDN, a payment provider,
an analytics host) can use one Transport per site, so that no connection
carries requests from two top-level sites; passing the same jar in
`Config.Cookies` to each keeps one cookie store, as one browser profile
has (the jar keys partitioned cookies by top-level site itself). The gap
is listed in [the backlog](../backlog.md#connections-and-pool).

## Amendment 2026-10-28: the Alt-Svc cache

The same holds for `Config.AltSvc`: a shared `AltSvcCache`
keys alternatives by origin and connection partition, not by top-level
site, where browsers that partition their HTTP server properties
(Chrome's network anonymization key) would keep one site's apart from
another's. One cache per site's Transports keeps them apart.
