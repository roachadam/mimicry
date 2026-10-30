# 0048: Cookie twins of the redirect scenarios

- Status: Accepted
- Date: 2026-10-30
- Commits: see the commit that adds this record

**Summary.** Where a redirect hop places the `cookie` header is measured by
cookie twins of the redirect scenarios: `redirects2-cookies`,
`cors-edge-cookies` and `nav-redirect-{so,ss,cs,http,page,form,click}-cookies`.
Each is its original but for cookies set on the hosts its hops go to (the
typed cross-site one adds a hop, as a cookie of another site needs a first
visit there). kindsgen merges each twin's hops into its original's
conditions, so the hop variants gain a measured `cookie` slot; the
originals and their captures stay as they were. Before, a hop variant
derived from cookie-less hops had no slot, and mimicry sent the jar's
cookies after the list (after `priority` in Chrome's).

## Problem

A redirect variant's list is what the browser sent on the captured hops
([0006](0006-header-order-variants.md)). None of the redirect scenarios'
hops carried cookies, so no hop variant had a `cookie` slot, and a header
the list does not name goes after it (`headerorder.Build`). Chrome 156,
through powhttp on httpbin.org's `/cookies/set` → `/cookies`, sent
`… accept-language, cookie, cookie, cookie, priority` on the hop; mimicry
sent the cookies after `priority`. Login and SSO flows redirect with
cookies all the time.

## Options

- **Make the existing redirect scenarios set cookies.** The fewest
  scenarios, but every capture of them that cannot be retaken stops
  matching the server: Chrome 155's own (no longer installable as branded
  Chrome, and `capturesFrom` names only an older version, so it cannot
  inherit Chrome 156's), `TestInheritedProfile`'s synthetic profile built
  from Chrome 155's captures, and any machine that is not available.
  Rejected.
- **A rule placing the slot as the first leg does.** No capture behind
  it for hops ([0003](0003-evidence-rule.md)); Firefox's and Safari's hop
  lists differ from their first legs'. Rejected.
- **Twins: copies of the scenarios that differ only by their cookies.**
  Additive: the old evidence stays, a profile without a twin's capture
  skips it (`missingCaptures`), and the generators merge each twin with
  its original. Chosen.

## Decision

- A twin is named `<original>-cookies` and listed in `server.CookieTwins`.
  Its steps, hosts and responses are its original's, plus `Set-Cookie`:
  - page scenarios (`redirects2`, `nav-redirect-page`, `-form`, `-click`):
    `/` sets a cookie of the page's site (`Domain`, `Secure`,
    `SameSite=None`); every response to a navigation on another site sets
    that site's, so it is a first-party cookie every browser keeps;
  - `cors-edge`: `/` sets the page's site's cookie;
  - typed navigations: the first redirect sets the cookie (`so`: three
    host cookies, as httpbin does; `ss`: the site's; `http`: one without
    `Secure`, set over `http://`). `nav-redirect-cs-cookies` starts on the
    other site, whose 302 sets its cookie, goes to the page's site and
    back: both hops are cross-site, the second carries the cookie.
- Safari keeps its cookies between samples, so every cookie is deleted on
  its last use: the page's at `/done`, another site's on the response to
  the page's last navigation request there.
- kindsgen reads the twins with their originals (`nav-redirect-*` by
  prefix, `redirects2-cookies` and `cors-edge-cookies` by name); the e2e
  tests replay each twin as its original.

## Evidence

Captured 2026-10-30, three samples each: Chrome 156 on macOS and Windows,
Firefox 158, Safari 27 and Safari 26 (no clicks over ssh). Every twin's
request, cookie removed, has its original's header names in every sample
(the one exception is Safari's cold first sample of
`nav-302.cs-so.hop1`, which only the original shows). Slots measured,
on every hop that carried cookies:

- Chrome (macOS, Windows): between `accept-language` and `priority`, on
  navigations, images and fetch() alike.
- Firefox: after `referer` (or `origin`, when sent) and before
  `upgrade-insecure-requests` on navigations, before `sec-fetch-dest` on
  images and fetch(); after `accept-encoding` on a typed navigation's hop.
- Safari 27, 26: last, after `accept-encoding`.

The twins also showed that Safari 27's `cookies.redirects:
block_after_cross_site` holds for subresources only: its navigations
through a cross-site hop carried their cookies, so the jar no longer
blocks them. Rows in [EVIDENCE.md, Redirects](../../profiles/EVIDENCE.md#redirects).

## Consequences

- Hop variants of navigations, images and same-origin fetch() carry the
  slot for Chrome 156 (macOS and Windows), Firefox 158, Safari 27 and 26.
- Chrome 155 keeps the old lists: its hops with cookies still put them
  after the list (`missingCaptures`, backlog).
- Hops no twin reaches with cookies keep no slot: cross-site
  subresources with credentials (`credentials: "include"`,
  `withCredentials`, `crossorigin="use-credentials"`) and hops from
  `http://` (backlog).
- A refresh recaptures twins as it does their originals; a new redirect
  scenario gets a twin when its hops can carry cookies.

## Revisit when

- A redirect scenario changes: its twin changes with it.
- Captures show a hop whose cookie slot differs from its twin's.
