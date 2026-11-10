# 0067: Brave profiles, captured on their own; Brave's Shields modelled

- Status: Accepted
- Date: 2026-11-09
- Commits: the one that adds `brave-157-macos`, `brave-157-windows` and `brave-157-linux` (#109)

**Summary.** `brave-157-macos`, `brave-157-windows` and `brave-157-linux`
are Brave 1.99.55 (Chromium 157) on macOS, Windows and Linux, captured
like Edge (decision 0059): every scenario and replay, three samples each,
nothing inherited, the first seeded from Chrome's profile and the others
from Brave's macOS one. Brave sends Chrome's user agent word for word but
differs on the wire, and three of its Shields defaults change what goes
out: a randomised (farbled) Accept-Language q value per top-level page
origin, third-party cookies kept per top-level site, and no cross-site
beacons. Each is a profile rule derived from the captures. Opera is not
part of this decision: it has no captures and no profile.

## Problem

tls-client ships Brave profiles; mimicry had Chrome and Edge as its
Chromium browsers. A Chrome profile under Brave's name is what other
clients offer, and the captures show it is told apart at once (the TLS
hello, the brands, `sec-gpc`, the accept list). Brave also changes what
it sends per session (Shields' fingerprinting protection), so its
samples disagree where every other browser's agree, which the evidence
rule (decision 0003) forbids settling by picking one sample.

## Options

- **Chrome's profile with Brave's brands** (tls-client's way): no
  captures, and wrong on the hello (Trust Anchor IDs), the accept list
  and every Shields rule.
- **Capture Brave, leave the farbled Accept-Language as a known gap**
  with one value in the lists: picks one sample's value, and a fleet of
  clients all sending one q is itself a fingerprint.
- **Capture Brave on its own and model what Shields do** (chosen): the
  generators derive each rule from Brave's captures; mimicry applies them.

## Decision

- Profiles `brave-<major>-<platform>` (`profiles.BraveMacOS()`,
  `profiles.BraveWindows()`, `profiles.BraveLinux()`), the major being
  Chromium's (Brave reports itself as 157.1.99.55), refreshed by
  `cmd/refresh -browser brave` (`-mini`), `brave-windows` (`-windows`)
  and `brave-linux` (`-linux`, `-wsl`). `releasewatch` reads Brave's
  release channel from `versions.brave.com`.
- The capture tooling treats Brave as a Chromium browser
  (`capture.Chromium`). Since its user agent is Chrome's, a capture is
  named after the browser launched (`capture.Capture.Identify`'s
  `named`), not its user agent. Client-certificate scenarios set
  `AutoSelectCertificateForUrls` under `com.brave.Browser` (macOS),
  `HKCU\Software\Policies\BraveSoftware\Brave` (Windows) and
  `/etc/brave/policies/managed` (Linux). Nothing of Brave is switched
  off: no flag or policy beyond Chrome's capture flags.
- **Accept-Language farbling: `context.acceptLanguageQ`.** The q values
  the last language's q is drawn from (Brave's 0.5 to 0.9), one per
  top-level page origin, kept for the session; the lists carry the value
  Brave sends unfarbled (0.9, as its CORS preflights do). mimicry draws
  one per Transport and page origin: a navigation and its redirects take
  their first URL's, every other request its page's (`Request.From`, or
  the last navigation), a WebSocket handshake its page's; CORS
  preflights and a caller's own Accept-Language are left alone. kindsgen
  derives the set from every Brave capture as sent. The generators and
  the e2e comparisons read Brave's captures unfarbled
  (`capture.Unfarble*`: every q replaced by 0.9) and test mimicry with
  the farbling off; `TestAcceptLanguageFarbling` checks the farbling
  itself against the samples as sent and against mimicry's draws.
- **Third-party cookies: `cookies.thirdParty: partition_all`**, as
  Firefox's: cookies of a cross-site context are kept apart per
  top-level site (Brave's ephemeral storage), so cookies set first-party
  never go cross-site. Hand-written, checked by the cookie e2e tests.
- **Cross-site beacons: `context.blocksCrossSiteBeacons`.** Brave never
  sends a `navigator.sendBeacon` to another site; a cross-site `Beacon`
  request is an error. kindsgen derives it from the kinds pages.
- **HTTP/3 fallback.** Brave on macOS and Linux races TCP after 2 to 5 ms
  where Chrome waits about 300, Brave on Windows after 305 to 313 as
  Chrome does (`tcpDelayMs`); h3gen now reads a race whose 0-RTT attempt
  holds its request (`zeroRttHolds`) as a race whatever the delay. A
  0-RTT attempt holds its request until it is dialled, and fast fallback
  gives a resumed connection at least the first probe timeout.
- **Client hints.** Brave sends a smaller set (no `sec-ch-ua-full-version`,
  device memory, DPR, viewport, form factors, preferences or network
  hints) and reduced versions (157.0.0.0 for every brand). kindsgen keeps
  the seed machine's values for hints a browser never sends; no Brave
  request carries them.

## Evidence

Brave 1.99.55 (157.1.99.55, Chromium 157.0.8103.39), 2026-11-09: on the
second Mac (macOS 26.2, an update from 154.1.96.60), on the Windows PC
(Windows 10 22H2, a per-user install by Brave's installer run as the
console user) and in WSL Ubuntu-24.04 (Brave's apt repository): every
scenario of the full set and the four `proxy-https*` ones, three samples
each. A trial on this Mac (macOS 27.0.1) showed the same headers.

- **TLS:** Chrome's cipher suites and extensions but Trust Anchor IDs
  (`0xca34`), as Edge: 16 extensions, JA4
  `t13d1516h2_8daaf6152771_806a8c22fdea`, Edge's. HTTP/2 settings, window
  update, priorities and pseudo-header order are Chrome's (Akamai
  `1:65536;2:0;4:6291456;6:262144|15663105|0|m,a,s,p`).
- **Headers:** `sec-ch-ua: "Not-A.Brand";v="99", "Brave";v="157",
  "Chromium";v="157"`; the user agent is Chrome's; `sec-gpc: 1` on every
  request, after `accept`; the navigation's `accept` keeps `image/jxl`
  but has no `application/signed-exchange;v=b3;q=0.7`.
- **Accept-Language:** `en-US,en;q=0.N`, N one of 5 to 9 (all five
  seen), the same on every request of a page's tab whatever host it goes
  to, another per sample; a redirect hop keeps the first URL's, the new
  page's requests take its own; http:// and https:// of one host draw
  apart; CORS preflights send `en-US,en;q=0.9`; a page's own `OPTIONS`
  fetch is farbled. Brave's source (`brave_reduce_language_network_
  delegate_helper.cc`, `kFakeQValues`; a lead, not evidence) agrees.
- **Cookies:** `cookies` shows no unpartitioned `SameSite=None` cookie
  on a cross-site request (`c-part.cs`, `c-third.so`), where Chrome sends
  them; with `partition_all` every cookie, CORS, redirect and WebSocket
  scenario matches.
- **Beacons:** the kinds pages' beacon to `cdn.kinds-b.test` is absent
  in all 9 samples (kinds, kinds-http1, kinds-plain); the same-site ones
  all went.
- **QUIC:** the same transport parameters as Edge; second Initials in 2
  bytes in 143 of 150 connections on macOS (138 of 150 on Windows, 148
  of 156 on Linux), the rest with a token: Edge's `shared_second_long`.
  Path degrading after about 3.3–3.9 PTOs on macOS and Windows
  (`afterPtos` 3), 3.9 on Linux (4, as Chrome and Edge). One connection
  on macOS acknowledged the server's Initial late and numbered that
  packet after the Handshake and 1-RTT ones it led (pinned,
  `coalescedOrder`).
- **HTTP/3 fallback:** after a GOAWAY the first request on the
  blackholed host waited 3–5 ms for QUIC on macOS, 2–5 ms on Linux and
  305–313 ms on Windows (Chrome 305, Edge 313); the 0-RTT attempt after
  the server's close held its request 4.0 s, as Chrome's.
- **Background traffic:** Brave's own requests (`star-randsrv.bsg.brave.com`,
  `usage-ping.brave.com`) appear only through the capture proxy, which
  refuses and does not record them.

## Consequences

- Brave has a profile on macOS, Windows and Linux. Brave on the three
  platforms is the same but for the platform, the machine and the QUIC
  timings above.
- A caller who sets Accept-Language (Config.Header) sends their own value,
  unfarbled, as Brave leaves a page's explicit value alone.
- A cross-site `Beacon` with a Brave profile returns an error rather than
  a request no Brave sends.
- Requests from a cross-site frame take the frame's origin for their q
  (`Request.From`); Brave takes the tab's. Not measured, listed in
  EVIDENCE.md.
- Brave's Linux launcher script runs the browser without `exec`, so
  stopping the script left Brave and its helpers running after every
  sample; past about 60 launches new ones exited at once ("The platform
  failed to initialize"). The capture tool now starts each browser in a
  process group of its own and ends the whole group, and takes again a
  sample whose browser exited before it reached the page
  (`browser.ErrWarmupExit`, with the browser's output in the error).

## Revisit when

Brave changes its Shields defaults (farbling off, cookies, beacons),
starts sending Trust Anchor IDs or `sec-ch-ua-full-version`, or a
capture shows the farbled q keyed by something else than the page
origin.
