# 0007: The navigation stays the profile's top-level request

- Status: Accepted
- Date: 2026-10-24
- Commits: `f841faa`

**Summary.** The maintainer's decision is to keep the navigation as the
profile's top-level base request (`headers`, `http1`, `http2`) instead of
moving it into `kinds.navigate`, because every capture, tool and comparison
already treats it as the profile's request. For a library user nothing
changes: `Navigate` is the default kind and behaves like any other kind,
with its own variants such as a form POST.

## Problem

With request kinds in the profile, the navigation could move into
`kinds.navigate` so every kind looks alike. That was the plan.

## Options

- Move it: one representation for all kinds. Every capture file, the
  capture server, the diff tool and the profile generator treat the
  navigation as "the profile's request" and would all change.
- Keep `headers`, `http1` and `http2.headersPriority` as the navigation and
  put other kinds in `kinds`.

## Decision

Keep it. The navigation is the profile's base request, captured on every
scenario; other kinds are described relative to it. The navigation gets
`variants` like a kind (a form POST). This is not a compatibility shim:
there is one place for the navigation and one for every other kind.

## Consequences

The resolver handles `Navigate` slightly differently (its lists are
top-level). Everything that compares the first request keeps working.

## Revisit when

A second "base" request appears that makes the asymmetry confusing.
