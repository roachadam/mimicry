# 0004: One complete profile file per browser version, no inheritance

- Status: Accepted
- Date: 2026-10-23

## Problem

Profiles share most values across versions and platforms. Inheritance would
make files small.

## Options

- httpcloak: delta JSONs chained about ten deep via `based_on`, rooted in Go
  code and uTLS `ClientHelloID`s, with merge, loop-guard and zero-value rules.
- tls-client: 47 complete Go literals.
- One complete JSON per browser, version and platform.

## Decision

`profiles/data/<browser>-<version>-<platform>.json`, complete and
self-contained, validated by `profile.Parse` (strict: unknown fields are
errors); typed Go variables are generated from them.

## Consequences

Inheritance hides what a profile sends and lets unmeasured values ride
along silently; complete files keep every claim visible and checked
against that version's own capture. Files are long (Safari 27 with its
request kinds is ~4,700 lines), which is fine for data.

A new version starts as a copy of the previous one; whatever a capture
cannot show stays marked copied in the ledger until its own captures pass.
Planned: `profilegen derive --from <previous> --capture <new>`.

## Revisit when

Maintaining many versions by copy becomes error-prone despite the tooling.
