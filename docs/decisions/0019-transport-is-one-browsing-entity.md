# 0019: A Transport is one browsing entity

- Status: Accepted; amended 2026-10-28 by [0040](0040-idle-connection-cleanup.md) (rotating proxies within one Transport) and 2026-10-29 by [0044](0044-profile-fingerprint.md) (cheap clients)
- Date: 2026-10-24
- Commits: `96bdaa0`

**Summary.** A Transport (or a client from `NewClient`) is one browsing
entity: one browser profile used by one task, with its own last navigation,
cookies, caches, tickets and connections. This is the maintainer's decision,
because that state is one browser's and sharing it across tasks would mix one
site's `Origin`, `Referer` and cookie partitions into another's. For
automating user actions on sites, use one client per task (cheap since 0044,
which shares the decoded profile); a Transport may also rotate proxies, whose
unused connections close early since 0040.

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
on `Transport`, `Request.From` and `As`. (The maintainer's decision,
2026-10-24.)

## Consequences

Per-task clients do not share pools or tickets, as separate browser
profiles do not. A caller who does share one client across tasks gets
mixed pages, and must pass `From` explicitly to avoid it.

## Revisit when

Users need many tabs of one browser profile: same jar, separate pages.
Measure what browsers share between tabs first.

## Amendment 2026-10-29: rotating proxies, cheap clients

Since [0040](0040-idle-connection-cleanup.md), rotating proxies within one
Transport is also supported: the connections of proxies no longer used
close early (`Config.MaxIdleProxies`), so a Transport does not hold every
identity it has used. Since [0044](0044-profile-fingerprint.md), a new
client shares its decoded profile with the others built from the same
one, so one client per task costs little.
