# 0059: Edge profiles, captured on their own

- Status: Accepted
- Date: 2026-11-06
- Commits: the one that closes [#65](https://github.com/roachadam/mimicry/issues/65) for macOS

**Summary.** `edge-157-macos` is Microsoft Edge on macOS, captured like
every other profile: the full set of scenarios and replays, three samples
each, on the second Mac. Edge is Chromium, but it does not send Chrome's
first request: no Trust Anchor IDs, no `image/jxl` in `accept`, its own
brands and full versions, and a different packet number length. So it
gets its own profiles and inherits nothing from Chrome's. Edge on Windows
follows when its machine can capture.

## Problem

Edge is the second most used desktop browser on Windows, and decision
0047 deferred it. Libraries that ship Edge reuse Chrome's wire with
Edge's user agent and brands. Whether that is right was unknown:
Chrome's Trust Anchor IDs come from the Chrome Root Store, Edge runs its
own field trials, and the two update on different schedules.

## Options

- **Chrome's profile with Edge's user agent and brands.** No captures,
  but wrong wherever Edge differs.
- **Inherit Chrome's captures** for the scenarios where they agree, by
  widening decision 0035's rule to another browser on the same engine (as
  0053 did for Chrome on iOS). Fewer captures, but every inherited row
  rests on Chrome's traffic, and the trial already showed differences in
  the first request.
- **Capture Edge on its own** (chosen). Its first profile on a platform
  is seeded from Chrome's on that platform, then every generated section
  is derived from Edge's captures and every copied section checked against
  them; nothing is inherited.

## Decision

- Profiles `edge-<major>-<platform>` (`profiles.EdgeMacOS()`), refreshed
  by `cmd/refresh -browser edge -mini <alias>` (and `edge-windows` with
  `-windows`). `releasewatch` reads Edge Stable from Microsoft's
  `edgeupdates` feed.
- The capture tooling treats Edge as a Chromium browser
  (`platform.Chromium`): Chrome's launch flags, warm-up, DevTools driving,
  NSS trust on Linux and QUIC conditions.
- A browser's first profile on a platform starts from its own profile on
  another platform; Edge's very first, from Chrome's on the same platform.
  Either way it captures the full set and inherits nothing (decision
  0035's rule is unchanged).
- Two profile fields are new, for what Edge does differently:
  - `clientHints.chromiumFullVersion`: the Chromium brand's version in
    `sec-ch-ua-full-version-list`, which Edge reports apart from its own
    (157.0.8103.39 beside 157.0.4341.42).
  - `quic.packetNumbers` `shared_second_long`: Chrome's shared counter,
    but the second Initial's packet number always in 2 bytes.

## Evidence

Edge 157.0.4341.42 on macOS 26.2, 2026-11-06 (updated from 156 with
Microsoft's package before capturing), 71 captures:

- **TLS:** the same cipher suites and extensions as Chrome 157 but Trust
  Anchor IDs (`0xca34`), which Edge never sends: 16 extensions to
  Chrome's 17. The issue's question answered: Edge verifies with the
  system's store, not the Chrome Root Store.
- **Headers:** `accept` without `image/jxl`, in navigations and image
  requests; `sec-ch-ua` with `"Microsoft Edge"`; the user agent ends
  `Edg/157.0.0.0`. HTTP/2 settings, priorities, header order and QUIC
  transport parameters as Chrome's.
- **Client hints:** `sec-ch-ua-full-version` is Edge's version;
  `sec-ch-ua-full-version-list` gives the Chromium brand Chromium's.
- **QUIC packet numbers:** Chrome sends its second Initial's number in 2
  bytes exactly when it has no token (283 of 283 connections across three
  profiles). Edge sends 2 bytes in 115 of 123, token or not; the 8
  one-byte ones all carry a token, and nothing on the wire tells them
  apart (scenario, timing, `initial_rtt`, hello size). Pinned.
- **Timing:** the fallback's TCP delay 313–315 ms (Chrome 305–306); the
  server's Initial acknowledged in 4 of 12 full handshakes (Chrome 157 on
  Linux 2 of 12).
- `realcheck` against the real Edge: no difference on the echo endpoints
  (TLS, HTTP/2, QUIC, HTTP/3); browserleaks' HTTP/2 page also logged
  Edge's own `/favicon.ico` request.

## Consequences

- Edge on macOS has a profile of its own; Edge on Windows needs the
  Windows PC's capture run.
- Edge refreshes are full captures until a second Edge version inherits
  from the first.
- The 8 one-byte second Initials are a known gap until a capture shows
  what decides them.

## Revisit when

Edge starts sending Trust Anchor IDs or `image/jxl`, a capture explains
the second Initial's length, or Edge on another platform differs from
Edge on macOS.
