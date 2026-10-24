# 0009: Our own cookie jar, driven by the transport

- Status: Accepted (design); building in step 9.3
- Date: 2026-10-23

## Problem

Cookies are where every other client differs from browsers
(`harness/reports/clients-2026-10-23.md`). Browser cookie rules depend on
the request's kind and page (SameSite, third-party policies, partitioned
cookies), which `http.Client.Jar` never sees. `net/http` also changes values
before a jar sees them: it re-quotes values with spaces or commas and drops
ones with backslashes, UTF-8 or embedded quotes (verified on Go 1.27).

## Decision

Package `mimicry/cookies`, wired into the transport by `NewClient`: it reads
raw `Set-Cookie` lines and writes the `Cookie` header byte for byte, with
the request's kind and page in hand. Strict browser rules by default (a
cookie the browser drops must be dropped, or sending it is a tell),
leniency opt-in per rule. Full API: every attribute plus the raw line, set
without a URL, list and filter, delete by match, clear, clone, JSON and
Netscape import/export, `For(req)` (what would be sent, in browser order),
an `OnSetCookie` hook reporting each rejection and why. It also implements
`http.CookieJar` as an adapter, documented as lossy.

Per-browser behaviour is measured (the kinds capture records every cookie
each browser sent on every request), not assumed.

## Revisit when

Go's cookie path stops altering values and gains request context.
