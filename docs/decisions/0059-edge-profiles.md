# 0059: Edge profiles, captured on their own

- Status: Accepted
- Date: 2026-11-06
- Commits: the ones that close [#65](https://github.com/roachadam/mimicry/issues/65)

**Summary.** `edge-157-macos` and `edge-157-windows` are Microsoft Edge
on macOS and Windows, captured like every other profile: the full set of
scenarios and replays, three samples each, on the second Mac and the
Windows PC. Edge is Chromium, but it does not send Chrome's first request:
no Trust Anchor IDs, no `image/jxl` in `accept`, its own brands and full
versions, and a different packet number length. So it gets its own
profiles and inherits nothing from Chrome's. Edge on the two platforms is
the same but for the platform and the machine.

## Problem

Edge is the second most used desktop browser on Windows, and decision
0047 deferred it. Whether Chrome's wire with Edge's user agent and
brands is right was unknown: Chrome's Trust Anchor IDs come from the
Chrome Root Store, Edge runs its own field trials, and the two update on
different schedules. What other clients offered for Edge (their sources,
2026-11-06):

- **uTLS** (`u_common.go`): `HelloEdge_85` and `HelloEdge_106`, with
  `HelloEdge_Auto` set to Edge 85 ("HelloEdge_106 seems to be
  incompatible with this library"); TLS only.
- **curl_cffi / curl-impersonate** (`requests/impersonate.py`): `edge99`
  and `edge101`, `edge` meaning Edge 101.
- **bogdanfinn/tls-client** (`profiles/`): no Edge profile. Its Chrome
  profiles send `0xca34` with Chrome's trust anchors, so a Chrome profile
  given an Edge user agent, the usual way to get Edge there, pairs
  Edge's headers with Chrome's hello.
- **fphttp** (as #65 describes it): Edge 119–155 as Chrome's wire with
  Edge's user agent and brands, Chrome's 28 Trust Anchor IDs included.

So every Edge on offer was years old, or Chrome's hello under Edge's
name. The captures below show both are detectable.

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

- Profiles `edge-<major>-<platform>` (`profiles.EdgeMacOS()`, `profiles.EdgeWindows()`), refreshed
  by `cmd/refresh -browser edge -mini <alias>` (and `edge-windows` with
  `-windows`). `releasewatch` reads Edge Stable from Microsoft's
  `edgeupdates` feed.
- The capture tooling treats Edge as a Chromium browser
  (`capture.Chromium`): Chrome's launch flags, warm-up, DevTools driving,
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
  Chrome's 17, JA4 `t13d1516h2_8daaf6152771_806a8c22fdea` where Chrome's
  is `t13d1517h2_8daaf6152771_cb7bf5808d99`. A Chrome hello under Edge's
  user agent is therefore told apart by its JA4 alone. That Edge
  verifies with the system's store rather than the Chrome Root Store is
  [INFERENCE] from the absence.
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
- **Windows:** Edge 157.0.4341.42 on Windows 10, the same day (updated
  from 156 with Microsoft's MSI), 71 captures with the PC's Edge policies
  lifted. The profile is `edge-157-macos`'s but for the platform strings,
  the machine's client hints and the fallback's TCP delay (303–313 ms):
  the same TLS hello and JA4, headers, QUIC transport parameters and
  `shared_second_long` (114 of 123 second Initials in 2 bytes, the 9
  others with a token). The first probe's flight split by the recorder in
  one `h3-blackhole` sample is pinned. `realcheck`: no difference.

## Consequences

- Edge has a profile of its own on macOS and Windows. On Windows, the
  PC's Edge policies (`HKLM` and `HKCU` `SOFTWARE\Policies\Microsoft\Edge`,
  about 35 settings from a debloat tool, `ConfigureDoNotTrack` among
  them) made Edge add `dnt: 1` to every request after its first few, which
  no default Edge sends, so the first Windows capture (2026-11-06) was
  discarded. The keys were exported, lifted for the recapture and for
  realcheck, and imported again after; the captures carry no `dnt`.
- Edge refreshes are full captures until a second Edge version inherits
  from the first.
- The 8 one-byte second Initials are a known gap until a capture shows
  what decides them.

## Revisit when

Edge starts sending Trust Anchor IDs or `image/jxl`, a capture explains
the second Initial's length, or Edge on one platform starts to differ from
the other beyond the platform and the machine.
