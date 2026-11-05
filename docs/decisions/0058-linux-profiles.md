# 0058: Linux profiles, one per browser, captured in WSL

- Status: Accepted
- Date: 2026-11-05
- Commits: the one that closes [#64](https://github.com/roachadam/mimicry/issues/64)

**Summary.** `chrome-157-linux` and `firefox-159-linux` are Google's Chrome
and Mozilla's Firefox on Linux, captured like every other profile: the full
set of scenarios and replays, three samples each. One profile per browser,
not per distribution: those builds are the same programs everywhere, and
Ubuntu 24.04 and Fedora 45 sent identical first requests. The captures were
taken in WSL2 on the Windows PC, the browsers on Xvfb, a virtual display:
windowed, never headless.

## Problem

Most clients run on Linux servers, but every profile said macOS, Windows
or iOS, so a Linux host's own traffic (its TCP SYN, #46) disagreed with
the profile it sent. Linux desktops come in many distributions, which
raised two questions: does each need its own profile, and can one be
captured without a Linux machine?

## Options

- **No Linux profiles.** Leaves every Linux user's mismatch in place.
- **A profile per distribution.** Many profiles that would mostly be
  copies, each needing its own machine.
- **One profile per browser, for the vendor's own build** (chosen). Google
  ships one Chrome `.deb`/`.rpm` with its own TLS (BoringSSL) and network
  code; Mozilla's Firefox bundles its own NSS. A distribution's own build
  (Fedora's Firefox on the system NSS, Debian's ESR, any packaged Chromium)
  is another browser, not covered.
- **Where to capture:** a Linux VM on this Mac cannot run Chrome (Google
  ships no arm64 Linux Chrome); WSL2 on the Windows PC runs both browsers
  on x86-64 and needs no new machine (chosen).

## Decision

- Profiles `chrome-<major>-linux` and `firefox-<major>-linux`
  (`profiles.ChromeLinux()`, `profiles.FirefoxLinux()`), refreshed by
  `cmd/refresh -browser chrome-linux,firefox-linux -linux <alias>`, with
  `-wsl <distribution> -wsl-user <user>` when the alias is a Windows PC.
- In WSL each step runs through `wsl.exe` over ssh, its command line
  base64-encoded so neither PowerShell nor cmd quotes it; files move as
  tar streams. Nothing is configured on Windows.
- The browsers open on Xvfb when there is no display that answers, and
  always in WSL: WSLg's display on that PC accepted connections and never
  replied (Chrome stalled before its first request, even started from the
  logged-in desktop), and on Fedora its Wayland socket, which Firefox
  prefers, hung while its X display answered. Xvfb's browsers are the
  windowed ones (`Chrome/…`, never `HeadlessChrome`); nothing on the wire
  depends on the display.
- Each browser trusts the capture CA in its throwaway profile: Chrome an
  NSS database under a `HOME` that is that profile, Firefox its profile's
  database, on pages across hosts. Nothing outside the run changes.
- The `urls` scenario forwards port 443; Linux lets a user bind it only
  below `net.ipv4.ip_unprivileged_port_start`, which refresh lowers to 443
  as root in WSL before each step (the setting dies with the VM), and
  asks for on a machine of its own.
- An HTTP/1.1 page's capture takes the first connection's ClientHello, as
  a page across hosts already did: Firefox on Linux sometimes asked for
  `/done` on a connection that had resumed another's session.
- The `h3-v2` scenario needs aioquic 1.0 or later (QUIC version 2);
  Ubuntu 24.04's package is 0.9, so the capture user installs it with pip.
- `clientHints.machine.platformVersion` is `""` on Linux, as Chrome sends
  it, and required everywhere else; `Machine.Validate`, which has no
  platform, accepts both forms.
- What the Linux captures taught the generators:
  - `http3.requestsAfterConfirmed` was read from a full handshake's first
    request, which shows waiting only when it happens to be ready within
    20 ms. A request held out of 0-RTT is ready before the handshake ends,
    so one that leaves before the handshake is confirmed now makes the
    rule false. Chrome on Linux sent its held requests before confirmation
    in all six `h3-early` and `h3-too-early` samples, while its full
    handshakes suggested waiting.
  - `http3.streamsWithFinished` and `http3.priorityUpdate` take the
    majority, as the QUIC rules do, so a timing exception can be pinned.
  - Chrome's `downlink` estimate can move during a run (`downlinkGaps`).
    Its per-host noise is fixed at 0.90–1.10, yet one host got 1.3 and
    1.75 Mbps in one sample. The profile keeps the centre of the values
    sent (1.55).
  - A sample, or a give-up attempt, pinned in h3gen's `sampleGaps` is
    left out of the rule, so a pinned odd sample can no longer set it.

## Evidence

Captures of 2026-11-05 on the Windows PC (Windows 10 x64, WSL2 kernel
6.18, Ubuntu 24.04), Chrome 157.0.8103.38 and Firefox 159.0.1:

- Trial, default page, one sample, against `chrome-156-windows`/`-macos`
  and `firefox-159-windows`/`-macos`: identical cipher suites, extensions
  and their contents, HTTP/2 settings, pseudo-header and header order;
  only the user agent (`X11; Linux x86_64`) and `sec-ch-ua-platform:
  "Linux"` differ.
- Ubuntu 24.04 against Fedora 45, three samples each: Chrome identical in
  every field (extensions compared by type: it shuffles them); Firefox
  identical but for which ECH GREASE cipher suites the samples happened to
  show (one is picked per connection; the other platforms' profiles list
  both).
- The full set, three samples of every scenario and replay (71 per
  browser), six of Firefox's `h3-broken`. Chrome's profile differs from
  `chrome-156-macos` only in the platform strings, the machine's client
  hints (`platformVersion` `""`, x86, the Xvfb screen), `downlink`, the
  fallback's TCP delay (302–303 ms) and the fewest frames added to the
  QUIC ClientHello's packet. Firefox's differs from `firefox-159-macos`
  only in the user agent and HTTP/3 timing: dial and race delays, the
  fallback's revalidation span and the first upload DATA frame's size.
- Firefox on Linux opened its burst of side connections, as on macOS and
  Windows, in about half the samples. In those it sent no GET or HEAD in
  0-RTT, raced no TCP after the server's close, and its resumed attempt
  after the close ended silently; the samples without it behave as on
  macOS: GET, HEAD and OPTIONS in 0-RTT, the race, and a give-up 5.0 s in
  (4 of 4). The burst's samples are pinned, as on the other platforms.

## Consequences

- Linux hosts can send a profile whose platform matches their own.
- Each Linux refresh is a full capture until a second version inherits
  from the first; Chrome and Firefox take turns on the PC, with its
  Windows runs.
- Distribution-built browsers and Linux on arm64 are not covered. The
  machine's client hints are WSL's under Xvfb (one screen, light theme);
  a caller on a real desktop sets its own (`Config.ClientHints`).

## Revisit when

A distribution's build of Chrome or Firefox is wanted, a Linux machine of
its own replaces WSL, or Chrome or Firefox start sending something that
depends on the distribution.
