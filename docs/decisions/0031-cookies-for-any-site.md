# 0031: Seeding cookies from strings, and cookies for any site

- Status: Accepted
- Date: 2026-10-26
- Commits: (where it landed)

## Problem

Retail automation moves cookies between tools: a `Cookie` header string
kept in a database, a solver's `[]*http.Cookie`, a harvested browser's
session. With bogdanfinn/tls-client these go in with
`client.SetCookies(u, cookies)` and come out with `client.GetCookies(u)`
(see [coming from tls-client](../concepts/coming-from-tls-client.md)).
mimicry's jar had `Set`, which needs a `Domain` and asks the caller to
choose host-only, path and attributes a header string does not carry, so
every codebase wrote the same loop and made the choices differently.
`NewClient`'s own jar could not be reached at all.

Some codebases also send one cookie value to every host (a flag, an
experiment bucket, a token a proxy layer reads). No browser has such a
cookie. The rule for deviations (the user's): defaults reproduce the
browser exactly; a deliberate deviation is allowed but must be explicit
and visible in the caller's code.

## Options

- **A host-only cookie per URL** (`SetHeader(u, header)`, the earlier
  proposal: path `/`, Secure on https). Close to what one response from
  `u` would set, but a session string usually comes from a whole site:
  the cookies would not reach `api.` or `cdn.` hosts the site's own
  Domain cookies reach, and the caller has to call it once per host.
- **A Domain cookie of the site's registrable domain** (chosen). The
  string names no host, and the registrable domain is the widest scope a
  browser ever gives a cookie, so every host of the site gets it and no
  other site does. Not Secure (the string does not say; a non-Secure
  cookie still goes over HTTPS), no SameSite (the profile's default
  applies, as for a server that sets none), a session cookie.
- **Cookies for every site by default, or a wildcard domain the caller
  types (`"*.com"`).** The first leaks a site's session to every host the
  Transport talks to without the caller asking; the second invites
  half-wildcards no rule can match sensibly.
- **Cookies for every site behind an explicit constant** (chosen):
  `cookies.AnySite` as the site. The deviation is one visible identifier
  at the call site, documented with its cost.
- **`[]*http.Cookie` through `Set`**, each `http.Cookie` mapped onto
  `Cookie` field by field. It skips the browser's rules (prefixes,
  `Domain` matching the URL, public suffixes, `SameSite=None` without
  `Secure`), so a cookie the browser would have refused gets stored and
  sent: a tell. Writing each as a `Set-Cookie` line and passing it through
  `SetCookies` as a navigation response from the URL applies them.

## Decision

- `Transport.Cookies()` returns the transport's jar (nil when none);
  `NewClient`'s is `client.Transport.(*mimicry.Transport).Cookies()`.
- `Jar.SetString(site, cookies)` stores each pair of a `Cookie` header
  string as a Domain cookie of `site`'s registrable domain (a host, a URL
  or the domain itself; ports ignored), path `/`, session, not Secure, no
  SameSite; it replaces the site's cookie of the same name, domain and
  path. Parsing tolerates missing spaces, a trailing `;` and empty
  pairs; a pair without `=` is a nameless cookie (as `Header` writes
  one). A site that is a public suffix (except a loopback name such as
  `localhost`, its own site), not a host, or a pair `Set` refuses is an
  error, and nothing is stored. `FromString(site, cookies, cfg)` is
  `New` plus `SetString`.
- `Jar.String(site)` returns the site's unexpired, unpartitioned
  cookies, of any host and path, as a header string in creation order:
  the order `SetString` recreates them in. Creation order rather than a
  send order, because the cookies of different paths and hosts never go
  out together.
- `cookies.AnySite` (`"*"`) as the site stores, or returns, cookies sent
  to every host of every site: a `Cookie` with `Domain` `AnySite`, kept
  in a bucket of its own that every request's lookup also reads. They
  pass the request's SameSite and third-party rules like any cookie (as
  if each site had set it), count towards no site's per-site limit, sort
  with the site's own cookies by the browser's order, survive
  `WriteJSON`/`ReadJSON`, `Delete`, `All` and `Clone`, and are left out
  of `WriteNetscape` (the format cannot name every site).
- `Jar.SetHTTPCookies(u, cs)` writes each `http.Cookie` from its fields
  (not `Raw`) as a `Set-Cookie` line, refusing one whose name, value,
  `Path` or `Domain` would change the line's meaning (`;`, control
  characters, `=` in the name), and stores the lines as a navigation
  response from `u`. It returns an error naming every cookie refused and
  why; the rest are stored.

## Evidence

No capture: these are API choices, not browser behaviour. What a browser
does with the stored cookies is unchanged and covered by the cookie e2e
tests. `TestSetString` and `TestSetHTTPCookies` (`cookies/api_test.go`)
pin the scoping (every host of the site, no other site, URL and host
reduced to the registrable domain, AnySite everywhere, both of two
cookies of one name), parsing, refusals and the `String` round trip,
with the jar's own `Cookies` as observer.

## Consequences

Porting a tls-client codebase's cookie handling is one call each way.
Nothing changes on the wire unless the caller seeds cookies, and
`AnySite` cookies only where the caller wrote `cookies.AnySite`. A
seeded cookie that was host-only or path-scoped in the source browser
becomes site-wide: harmless for session cookies, and `WriteJSON` or
`Set` keep exact scopes when the source has them. An `AnySite` cookie
reaches trackers, CDNs and every unrelated site the Transport visits,
and a site that sets its own cookie of the same name receives both.

## Revisit when

A site is found to check that a cookie arrives on one host only (a
host-only cookie the string seeded site-wide), or users need exported
strings per host; then `String` and `SetString` would take a URL and
keep host-only cookies host-only.
