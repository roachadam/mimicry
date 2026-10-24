# 0019: A Transport is one browsing entity

- Status: Accepted
- Date: 2026-10-24
- Commits: (the 2026-10-24 review commit)

## Problem

A request of a kind other than `Navigate` without `Request.From` comes from
the transport's last navigation (0005). That state is one value per
Transport. Two goroutines crawling different sites through one Transport
would send one site's `Origin`, `Referer` and `sec-fetch-site` to the
other, and key cookie partitions by the wrong top-level site. The same
holds for the cookie jar, the preflight cache, TLS tickets and pooled
connections: they are one browser's state.

## Options

- **Require `From` for every non-navigation kind.** No hidden state, but
  `As(req, kind)` loses its point, and the cookie jar, tickets and caches
  are still shared state.
- **A tab object** (`Transport.Tab()`) with its own last page and shared
  pool and jar. Closer to a browser, but new API whose sharing rules would
  need measuring (what a browser shares between tabs).
- **Keep the default, and say what a Transport is.**

## Decision

A Transport (or a client from `NewClient`) is one browsing entity: one
browser profile used by one task, like a tab with its own cookies, caches
and connections. Work in parallel uses one client per task, typically
with one proxy each. The last-navigation default stays. This is documented
on `Transport`, `Request.From` and `As`. (The user's call, 2026-10-24.)

## Consequences

Per-task clients do not share pools or tickets, as separate browser
profiles do not. A caller who does share one client across tasks gets
mixed pages, and must pass `From` explicitly to avoid it.

## Revisit when

Users need many tabs of one browser profile: same jar, separate pages.
Measure what browsers share between tabs first.
