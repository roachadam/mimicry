# 0009: Our own cookie jar, driven by the transport

- Status: Accepted
- Date: 2026-10-23 (designed), 2026-10-24 (built: `a09ce1f`, `31f727b`; review fixes 2026-10-24)

## Problem

Cookies are where every other client differs from browsers
(`internal/harness/reports/clients-2026-10-23.md`). Browser cookie rules depend on
the request's kind and page (SameSite, third-party policies, partitioned
cookies), which `http.Client.Jar` never sees. `net/http` also changes values
before a jar sees them: it re-quotes values with spaces or commas and drops
ones with backslashes, UTF-8 or embedded quotes (verified on Go 1.27).

## Decision

Package `mimicry/cookies`, wired into the transport by `NewClient`: it reads
raw `Set-Cookie` lines and writes the `Cookie` header byte for byte, with
the request's kind and page in hand. Strict browser rules: a cookie the
browser drops is dropped, or sending it is a tell. RFC 6265bis rules are in
code (prefixes, `__Host-` needing `Path=/` as sent, nameless cookies that
hide a prefix, the 400-day cap on `Max-Age` and `Expires`, escaped-path
matching, the last attribute winning); where browsers differ, the
profile's `cookies` section decides (0012). `New` validates those rules
and returns an error.

API: every attribute plus the raw line; `Set` without a URL; `All`;
`Delete` by match; `Clear`, `ClearExpired`, `ClearSession`; `Clone`; JSON
and Netscape import/export; `Cookies(Request)` (what a request would send,
in browser order); an `OnSetCookie` hook reporting each rejection and why.
Cookies are stored per registrable domain. It also implements
`http.CookieJar` as an adapter, documented as lossy. Leniency per rule,
once planned, was not built: nothing has asked for it.

Per-browser behaviour is measured (the kinds and cookies captures record
every cookie each browser sent on every request), not assumed.

## Revisit when

Go's cookie path stops altering values and gains request context.
