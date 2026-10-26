# 0031: Seeding cookies from strings, and cookies for any site

- Status: Accepted
- Date: 2026-10-26
- Commits: `68e2255` (first design), then the commit that adds `SetHostString` (the scope rework)

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
  proposal: path `/`, Secure on https) as the only way in. Close to what
  one response from `u` would set, but a session string usually comes
  from a whole site: the cookies would not reach `api.` or `cdn.` hosts
  the site's own Domain cookies reach, and the caller has to call it once
  per host. Kept as `SetHostString`, beside the Domain form.
- **A Domain cookie of the name's registrable domain** (first landed in
  68e2255, then reversed). `www.amazon.com` became `amazon.com`, so the
  cookies reached `api.amazon.com` too: a scope the caller did not ask for
  and no browser would give a cookie set for `www.amazon.com`, and no way
  to say "this host and below". A caller who wants the whole site names
  it.
- **A Domain cookie of exactly the name given** (chosen). The name is read
  as a browser reads a cookie's `Domain` attribute: that name and every
  name under it, nothing wider. Not Secure (the string does not say; a
  non-Secure cookie still goes over HTTPS), no SameSite (the profile's
  default applies, as for a server that sets none), a session cookie.
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
- `Jar.SetString(domain, cookies)` stores each pair of a `Cookie` header
  string as a Domain cookie of exactly `domain` (a host name, ports
  ignored, or a URL's host; never reduced to the registrable domain), so
  it goes to that name and every name under it: `amazon.com` reaches
  `www.`, `api.` and `smile.amazon.com`, `www.amazon.com` reaches
  `www.amazon.com` and names under it, not `api.amazon.com`. Path `/`,
  session, not Secure, no SameSite; it replaces the jar's cookie of the
  same name, Domain and path. Parsing tolerates missing spaces, a
  trailing `;` and empty pairs; a pair without `=` is a nameless cookie
  (as `Header` writes one). A domain that is a public suffix (`com`,
  `co.uk`, `localhost`) or an IP address (no `Domain` attribute may name
  either; the error points to `SetHostString`), not a host, or a pair
  `Set` refuses is an error, and nothing is stored. `FromString(domain,
  cookies, cfg)` is `New` plus `SetString`; no host variant, as `New`
  plus `SetHostString` is as short.
- `Jar.SetHostString(host, cookies)` stores the pairs as host-only
  cookies of exactly that host (a name, an IP address or a URL's host).
- Name prefixes are applied as browsers require, case-blind: a `__Host-`
  cookie is stored host-only on exactly the name given (`SetString`'s
  domain too: `SetString("amazon.com", ...)` puts it on the bare
  `amazon.com` host), `Secure`, path `/`; a `__Secure-` cookie `Secure`;
  `__Http-` and `__Host-Http-` cookies `Secure` and `HttpOnly` where the
  profile enforces them. A cookie a browser could not hold under its
  prefix would otherwise be sent where no browser sends it (a `__Host-`
  cookie to a subdomain, a `__Secure-` one over plain HTTP).
- `Jar.String(domain)` returns every unexpired, unpartitioned cookie the
  jar holds for that name and the names under it (host-only and Domain
  cookies whose domain is the name or ends in `.name`), of any path, as a
  header string in creation order: the order `SetString` recreates them
  in. `domain` is read as `SetString` reads it; an IP address gives its
  `SetHostString` cookies. Creation order rather than a send order,
  because the cookies of different paths and hosts never go out together.
- `cookies.AnySite` (`"*"`) as the domain stores, or returns, cookies sent
  to every host of every site: a `Cookie` with `Domain` `AnySite`, kept
  in a bucket of its own that every request's lookup also reads. They
  pass the request's SameSite and third-party rules like any cookie (as
  if each site had set it), count towards no site's per-site limit, sort
  with the site's own cookies by the browser's order, survive
  `WriteJSON`/`ReadJSON`, `Delete`, `All` and `Clone`, and are left out
  of `WriteNetscape` (the format cannot name every site). A `__Host-`
  cookie for `AnySite` is an error: it belongs to one host.
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
pin the scoping (`amazon.com`, `www.amazon.com` and `api.amazon.com`
against `walmart.com` for `SetString`, `SetHostString` and `AnySite`; a
URL or `host:port` read as its host and never widened), the prefixes,
the public-suffix and IP-address errors, both of two cookies of one
name, parsing, refusals and the `String` round trip, with the jar's own
`Cookies` as observer. Breaking the `__Host-` rule (no host-only, no
`Secure`, allowed for `AnySite`) or reducing `SetString`'s or
`String`'s name to its registrable domain fails the test.

## Consequences

Porting a tls-client codebase's cookie handling is one call each way.
Nothing changes on the wire unless the caller seeds cookies, and
`AnySite` cookies only where the caller wrote `cookies.AnySite`. The
caller names the scope: the site's registrable domain for the whole
site, a host for that host and below, `SetHostString` for one host; a
string seeded for `www.amazon.com` does not reach `api.amazon.com`. A
source browser's path-scoped or `Secure` cookies lose those attributes
in a string; `WriteJSON` or `Set` keep them. An `AnySite` cookie reaches
trackers, CDNs and every unrelated site the Transport visits, and a site
that sets its own cookie of the same name receives both.

## Revisit when

Users routinely pass a host when they mean its whole site (cookies
missing on `api.` hosts after `SetString("www.…")`): then a separate,
explicitly site-wide call, rather than widening `SetString` again.
