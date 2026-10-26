# 0011: Model connection partitioning only where a rule is measured

- Status: Accepted
- Date: 2026-10-24
- Commits: `031ec2b`

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

Model Chrome (profile `context.anonymous_connections`,
`frame_connections`; a partition in the pool key). Safari needs nothing.
Do not model Firefox: guessing a rule from one page would be wrong
somewhere else. `TestKindsMatchBrowserTraces` checks which requests share a
connection and logs Firefox's difference as a known gap.

## Revisit when

A capture designed for it (more hosts, orders, timings) explains Firefox's
grouping; or a cross-site frame's own requests are modelled.

Measured since (2026-10-24, `redirects2`): every browser sends a
navigation to another site on a connection of its own, not on one the
previous page used. That is partitioning by top-level site. It is not
modelled: it changes the pool key for every request a page makes, and
needs its own capture (which requests of a page share the top-level key,
per browser) and decision. `TestRedirects2MatchesBrowser` compares those
requests apart from their connections and lists the gap.

Deferred (2026-10-25, [0029](0029-scope-what-gets-modelled.md)): no
top-level-site partition until a capture or a vendor shows it matters
for detection. A Transport represents one browsing session
([0019](0019-transport-is-one-browsing-entity.md)). Callers who visit
unrelated sites that share third-party hosts (a CDN, a payment provider,
an analytics host) can use one Transport per site, so that no connection
carries requests from two top-level sites; passing the same jar in
`Config.Cookies` to each keeps one cookie store, as one browser profile
has (the jar keys partitioned cookies by top-level site itself). The gap
is listed in [the backlog](../backlog.md#connections-and-pool).
