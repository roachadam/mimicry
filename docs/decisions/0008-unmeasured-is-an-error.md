# 0008: Asking for what was never measured is an error

- Status: Accepted
- Date: 2026-10-24
- Commits: `f445dc1`; extended in `ea769bc`

## Problem

Some combinations were never captured: `fetchpriority=high` on a script, a
kind a profile has no data for, a form POST from a same-site page.

## Options

- Guess (copy another kind's or browser's values). Silent, possibly wrong.
- Fall back to the navigation. Certainly wrong.
- Fail, and say what is missing.

## Decision

`RoundTrip` returns an error wrapping `mimicry.ErrNotMeasured` (test with
`errors.Is`) that names the profile, kind and what is missing. Where a
measured fallback exists, it is used and documented instead: an uncaptured
order condition takes the kind's default order (0006), but only if that
order can place every header the request computes.

It is an error, too, when:
- the variant a request takes was measured over HTTP/2 only and the
  connection is HTTP/1.1 (the connection refuses it before writing);
- the request is one no browser can make: a no-cors fetch with a method
  other than GET, HEAD or POST, a beacon that is not a POST, a stylesheet
  on anything but a font or image, a navigation that is not a GET without
  the page whose form sends it (`Request.From`).

A profile cannot leave out what every request needs: `http2.hpack` when it
speaks HTTP/2 (no built-in "Chromium-like" default), the `cookies` section,
and `http1` on every kind once the profile has `http1` (a secure variant may
omit it, meaning not measured over HTTP/1.1). `Validate` checks these,
the ClientHello the handshake can send, HTTP/2 setting ranges and header
syntax, so a bad profile fails at `NewTransport`, not on every request.

## Consequences

Callers find out at once instead of being fingerprinted later. Closing a
gap means capturing it, which updates the ledger.

## Revisit when

A common need hits an error a quick capture cannot close.
