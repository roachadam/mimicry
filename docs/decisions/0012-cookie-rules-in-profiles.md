# 0012: Browser cookie rules as a profile section

- Status: Accepted
- Date: 2026-10-24

## Problem

The jar (0009) must reproduce each browser's cookie behaviour. The
captures (`harness/reports/cookies-2026-10-24.md`) show the browsers differ
in kind, not only in degree:

- an unset SameSite is Lax in Chrome (with a two-minute exception for
  cross-site top-level POSTs), None in Firefox and Safari;
- `SameSite=None` without `Secure`: rejected by Chrome and Firefox, kept
  by Safari;
- third-party cookies: Chrome sends unpartitioned `SameSite=None` cookies
  cross-site and keys `Partitioned` ones by top-level site; Firefox keeps
  every cookie of a cross-site context in a jar per top-level site; Safari
  allows only `Partitioned` cookies there;
- redirects: Chrome judges SameSite by the request URL alone, Firefox by
  the whole chain, Safari sends nothing after a cross-site hop;
- a cookie without `=`: an empty name in Chrome (sent as `knoeq`), a name
  in Firefox (`knoeq=`), dropped by Safari;
- Safari re-encodes UTF-8 values as if Latin-1;
- order: RFC 6265 (longer path, then creation) in Chrome and Firefox;
  longer path, then stored domain, then name in Safari.

## Options

- Hard-code three browsers in the jar. A new browser or version means
  code.
- Free-form booleans for every observed difference. Combinations no
  browser has become expressible and untested.
- **A small profile section of named policies**, one per dimension, each
  value a browser's measured behaviour.

## Decision

`profile.Cookies` (`cookies` in JSON), replacing `cookie_order`:

| Field | Values |
|---|---|
| `unset_samesite` | `lax`, `none` |
| `lax_allows_unsafe_seconds` | Chrome's exception window (120) |
| `none_requires_secure` | bool |
| `third_party` | `samesite` (Chrome), `partition_all` (Firefox), `partitioned_only` (Safari) |
| `redirects` | `ignore` (Chrome), `chain` (Firefox), `block_after_cross_site` (Safari) |
| `nameless` | `value` (Chrome), `name` (Firefox), `drop` (Safari) |
| `latin1_values` | bool (Safari) |
| `order` | `rfc` (Chrome, Firefox), `domain_name` (Safari) |

Rules every browser shares stay in code (RFC 6265bis: domain and path
matching, public suffixes, `__Host-` and `__Secure-` prefixes, the 4096-byte
limit, expiry, Secure only over HTTPS, Lax/Strict refused from cross-site
responses).

The jar orders cookies itself; the connections only split them into HTTP/2
fields. A caller's own `Cookie` header is sent as written.

## Evidence

The `kinds` and `cookies` captures, three samples each. The end of Chrome's
two-minute window is Chromium's documented behaviour, not captured (only a
seconds-old cookie was). Whether Safari refuses to store third-party
unpartitioned cookies or only to send them is not observable here; the jar
refuses both.

## Consequences

Profiles stay data. A browser with a new policy needs a new value and code
for it, deliberately.

## Revisit when

A capture shows a behaviour no value describes.
