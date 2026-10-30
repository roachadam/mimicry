# 0002: `net/http` types in the API, no forked `net/http`

- Status: Accepted
- Date: 2026-10-20

## Problem

Users want a client that works with the Go ecosystem; header order and
pseudo-header order are not expressible in `net/http`.

## Options

- Fork `net/http` (tls-client, CycleTLS, azuretls, httpcloak): custom
  `Request` types, incompatible with middleware and libraries.
- Keep `net/http` types and carry what they cannot express on the side
  (imroc/req is the only reviewed client that does).

## Decision

mimicry is an `http.RoundTripper` (`Transport`) and `NewClient` returns an
`*http.Client`. Header order comes from the profile; `HeaderOrderKey`, a
header key with an invalid name (so it can never be sent), overrides it
per request. Request kinds travel on the request context (`As`, `With`,
`Exact`). Pseudo-header order comes only from the profile.

## Consequences

Anything taking an `http.Client` works. Some behaviour has to work around
`net/http`: `http.Client` sets `Referer` on redirects (mimicry discards it,
see 0005), and the stdlib cookie path loses raw cookie values (see 0009).

## Revisit when

A browser behaviour cannot be reproduced through a `RoundTripper` at all.
