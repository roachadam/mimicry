# 0008: Asking for what was never measured is an error

- Status: Accepted
- Date: 2026-10-24
- Commits: `f445dc1`

## Problem

Some combinations were never captured: `fetchpriority=high` on a script, a
kind a profile has no data for (Safari 26 has no kinds).

## Options

- Guess (copy another kind's or browser's values). Silent, possibly wrong.
- Fall back to the navigation. Certainly wrong.
- Fail, and say what is missing.

## Decision

`RoundTrip` returns an error wrapping "not measured for this profile" that
names the profile, kind and option. Where a measured fallback exists, it is
used and documented instead: an uncaptured order condition takes the kind's
default order (0006).

## Consequences

Callers find out at once instead of being fingerprinted later. Closing a
gap means capturing it, which updates the ledger.

## Revisit when

A common need hits an error a quick capture cannot close.
