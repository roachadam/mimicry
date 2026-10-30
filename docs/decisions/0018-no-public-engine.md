# 0018: No public Engine interface

- Status: Accepted; amended 2026-10-29 by [0044](0044-profile-fingerprint.md) (the validated copy is shared between equal profiles)
- Date: 2026-10-24
- Commits: `ea769bc`

## Problem

`Config.Engine` let a caller replace the pure-Go engine. Nothing outside
the module could implement it usefully:
- the header plan and the connection partition travel on internal
  context keys (`internal/headerorder`, `internal/pool`), so an outside
  engine got no header lists, computed headers or priorities, and saw
  `Header-Order:` keys and nil-valued headers instead;
- `DialContext` and `Proxy` were ignored too;
- with an engine set, the profile was never validated.

The only user was one root test. The planned reason for the seam, a
cgo/BoringSSL engine, does not exist.

## Options

- **Keep it and document its limits.** It stays a public promise that
  cannot be kept.
- **Export a plan type** so outside engines get everything. That fixes the
  request description (lists, slots, partitions, priorities, preflight
  rules) as public API before WebSockets and HTTP/3 change it.
- **Remove it**, and keep an unexported field for tests.

## Decision

Remove it. Tests set an unexported field of `Config`. `NewTransport` gives
the native engine a validated copy of the profile.

## Consequences

The public surface is smaller. The engine and pool can change shape for
WebSockets and HTTP/3 without breaking callers. A caller who wants another
TLS stack has no hook.

## Revisit when

A second engine is actually built. Design the public plan type then, from
what both engines need.

## Amendment 2026-10-29: one validated copy per profile

Transports built from equal profiles now share one validated copy, found
by a fingerprint of the profile's content
([0044](0044-profile-fingerprint.md)).
