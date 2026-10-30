# 0004: One complete profile file per browser version, no inheritance

- Status: Accepted; amended 2026-10-26 by [0035](0035-version-policy-and-capture-inheritance.md) (values are still never inherited; a scenario's captures can be)
- Date: 2026-10-23

**Summary.** The maintainer's decision is one complete, self-contained JSON
profile per browser, version and platform, with no inheritance between
files, because inheritance hides what a profile sends and lets unmeasured
values ride along. For a library user, every value a profile sends is in its
own file and checked against captures; since 0035 a scenario's captures
(never its values) can come from an earlier version until its own are taken.

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
errors); accessor functions are generated from them (`profiles.gen.go`).

## Consequences

Inheritance hides what a profile sends and lets unmeasured values ride
along silently; complete files keep every claim visible and checked
against that version's own capture. Files are long (Safari 27 with its
request kinds is about 11,800 lines), which is fine for data.

A new version starts as a copy of the previous one; whatever a capture
cannot show stays marked copied in the ledger until its own captures pass.
`cmd/refresh` (in `internal/harness/`) does the copy: it takes the hand-copied
fields from the new capture, sets `capturesFrom` for the scenarios it did
not recapture (decision 0035) and runs the generators.

## Revisit when

Maintaining many versions by copy becomes error-prone despite the tooling.
