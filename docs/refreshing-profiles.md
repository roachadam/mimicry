# Refreshing profiles

Browsers ship a new version every few weeks. `profiles.ChromeMacOS()`,
`profiles.FirefoxMacOS()` and `profiles.SafariMacOS()` always mean the newest
built-in macOS version, `profiles.ChromeWindows()` and
`profiles.FirefoxWindows()` the newest Chrome and Firefox on Windows, and
`profiles.ChromeLinux()` and `profiles.FirefoxLinux()` on Linux. The
newest five versions per browser and platform are supported.
Older ones stay in the repository, marked deprecated. Keeping up is one
command, run in `internal/harness/`:

```sh
go run ./cmd/refresh -browser chrome,firefox -mode quick -mini <alias>   # both at once, on the second Mac
go run ./cmd/refresh -browser safari -mode quick           # Safari 27, on the Mac you run it on
go run ./cmd/refresh -browser safari-mini -mode full -mini <alias>       # Safari 26, on the second Mac
go run ./cmd/refresh -browser chrome -dry-run -mini <alias>           # the plan only
go run ./cmd/refresh -browser chrome-windows,firefox-windows -windows <alias>   # Chrome, then Firefox, on a Windows machine
go run ./cmd/refresh -browser edge -mini <alias>             # Edge, on the second Mac
go run ./cmd/refresh -browser edge-windows -windows <alias>  # Edge, on the Windows machine
go run ./cmd/refresh -browser chrome-linux,firefox-linux -linux <alias> -wsl Ubuntu-24.04 -wsl-user capture   # Chrome, then Firefox, in WSL on that machine
go run ./cmd/refresh -browser safari-ios -listen <mac-ip>   # Safari on an iPhone, served from this Mac
go run ./cmd/refresh -browser chrome-ios -listen <mac-ip>   # Chrome on an iPhone, served from this Mac
go run ./cmd/refresh -browser firefox-ios -listen <mac-ip>  # Firefox on an iPhone, served from this Mac
```

The setup assumes two Macs: the one you work on, and a second Mac kept on
macOS 26, reachable over ssh. Pass your ssh alias for it with `-mini` (no
default; chrome, firefox, edge and safari-mini need it). Chrome, Firefox
and Edge on Windows need a Windows machine reachable over ssh: pass your
ssh alias for it with `-windows` (no default). Chrome and Firefox on Linux need a Linux
machine reachable over ssh (`-linux`), or a WSL distribution on the Windows
machine (`-linux` its alias, `-wsl` the distribution). Safari, Chrome and Firefox on iOS need an iPhone
connected to this Mac by cable and on its Wi-Fi network: pass this Mac's
IPv4 address on that network with `-listen`.

## Knowing when to refresh

A weekly workflow, [Release watch](../.github/workflows/release-watch.yml)
(also run by hand from the Actions tab), runs `go run ./cmd/releasewatch` in
`internal/harness/` and reports two things the profiles fall behind on:

- A stable release newer than the newest profile of its browser on its
  platform: Chrome on macOS, Windows and iOS (a new major; chromiumdash),
  Edge on macOS, Windows and Linux (a new major; Microsoft's update feed,
  `edgeupdates.microsoft.com/api/products`, its Stable releases),
  Firefox on macOS and Windows (a new major; Mozilla's product-details) and
  Safari on macOS and iOS (any newer version with release notes, so 27.2
  is newer than a profile at 27.0.1; Apple's Safari release notes). Mozilla
  publishes no Firefox for iOS version, so nothing watches `firefox-*-ios`.
- The newest Chrome profiles' trust anchors extension (0xca34) against the
  `trust_anchor_id`s of Chromium's
  `net/data/ssl/chrome_root_store/root_store.textproto` at that platform's
  stable tag, naming the IDs added and removed.

The alert is one open issue titled "Release watch: a browser release or
Chrome trust anchors the profiles are behind on", labelled `fingerprint`.
The workflow opens it, rewrites its description (with a comment, so
subscribers hear of it) when the findings change, and closes it once it
finds nothing. It never changes a profile: refresh as below, and the next
run closes the issue. A feed it cannot read fails the run, which GitHub
reports like any failed scheduled workflow. Run the command locally to see
the same report on stdout.

## Two modes

A new version rarely changes more than its version strings. Most of what a
profile describes (cookie rules, CORS, redirects, proxies, HTTP/3 failure
handling, connection reuse) changes far less often than the fingerprint
anti-bot vendors read. So there are two kinds of refresh.

**Quick** recaptures only the fingerprint layer, `server.QuickScenarios`:

| Scenario | Samples | What it shows |
|---|---|---|
| default page (`page`) | 3 | TLS ClientHello, HTTP/2 preface and SETTINGS, the navigation's headers |
| `http1` | 1 | the navigation over HTTP/1.1, casing and order |
| `kinds`, `kinds-http1`, `kinds-plain` | 1 each | every request type's headers over HTTP/2, HTTP/1.1 and plain HTTP |
| `websocket`, `websocket-h2` | 1 each | WebSocket handshakes |
| `h3-discovery`, `h3-page` | 3 each | the QUIC ClientHello, transport parameters and HTTP/3 SETTINGS |
| `hrr` | 3 | the ClientHello sent again after a HelloRetryRequest with a cookie: where the cookie goes |

Scenarios where the browser picks something per connection (extension
order, GREASE values, ECH lengths) take three samples; the rest take one.
Chrome and Firefox take `h3-discovery` and `h3-page` twice, without and
with `-h3-local-roots`, as their committed captures do.

Every other scenario is inherited from the browser's previous version. The
profile's `capturesFrom` field names, per scenario, the profile holding its
captures. Generators and e2e tests read those captures instead, with the
version tokens in header values (and the navigation's `accept-encoding`) rewritten to the new version. Any other
difference still fails: a browser that starts sending its version
elsewhere by default (a new client hint) fails every inherited comparison
until recaptured or taught to `capture.Rewriter`. A quick refresh of one browser takes under ten
minutes of capturing.

**Full** recaptures every scenario and replay, three samples each. Run it
before a release, when a quick refresh shows a change beyond version
strings, or periodically. It takes about two hours per browser. The risk of
quick mode: a rule that changes in a quickly refreshed version goes
unnoticed until the next full refresh.

Tests split the same way. By default the e2e suite runs the newest profile
per browser and platform; a full run (`moon run :test-full`) covers every
supported one.

## Where each browser runs

| `-browser` | Machine | Notes |
|---|---|---|
| `chrome`, `firefox`, `edge` | the second Mac (`-mini`) | at once in quick mode (`-serial` to not), one after the other in full mode; a first profile alone |
| `safari` | the Mac you run it on | opens tabs in your own Safari window |
| `safari-mini` | the second Mac | Safari 26, since that Mac stays on macOS 26 |
| `chrome-windows` | the Windows machine (`-windows`) | profiles `chrome-<major>-windows`, for `profiles.ChromeWindows()`; the first one captures the full set |
| `firefox-windows` | the Windows machine (`-windows`) | profiles `firefox-<major>-windows`, for `profiles.FirefoxWindows()`; the first one captures the full set; after `chrome-windows` when both are named, never at once |
| `edge-windows` | the Windows machine (`-windows`) | profiles `edge-<major>-windows`; the first one captures the full set; Windows' browsers take turns |
| `chrome-linux` | the Linux machine (`-linux`, with `-wsl` a WSL distribution) | profiles `chrome-<major>-linux`, for `profiles.ChromeLinux()`; Google's own build; the first one captures the full set |
| `firefox-linux` | the Linux machine (`-linux`, with `-wsl` a WSL distribution) | profiles `firefox-<major>-linux`, for `profiles.FirefoxLinux()`; Mozilla's own build; the first one captures the full set |
| `safari-ios` | the iPhone, served from this Mac (`-listen`) | profiles `safari-<major>-ios`; the iPhone set whatever `-mode`, the rest inherited from macOS Safari |
| `chrome-ios` | the iPhone, served from this Mac (`-listen`) | profiles `chrome-<major>-ios`; the iPhone set whatever `-mode`; one after the other with `safari-ios` when both are named, never at once |
| `firefox-ios` | the iPhone, served from this Mac (`-listen`) | profiles `firefox-<major>-ios`; as `chrome-ios` |

Edge (`edge`, `edge-windows`; profiles `edge-<major>-macos` and
`edge-<major>-windows`) is Chromium: the capture tool launches and drives
it as it does Chrome. Its profiles never inherit Chrome's captures
(decision 0035): each is checked against Edge's own. The first Edge
profile on a platform starts from Edge's macOS profile when there is one
(as Chrome and Firefox on Windows do), else from the newest Chrome profile
of its platform, else Chrome's on macOS: copied, renamed and given
browser `edge`, it captures the full set whatever `-mode` and inherits
nothing. What the generators do not redo from those captures is Chrome's
until reviewed: the report lists it under "Hand-written, copied from the
previous profile" (cookies, proxy, redirects, connections, websocket, the
rest of http2, tls.resumption), and the e2e tests check it against Edge's
captures.

The second Mac needs no Go. The tool cross-compiles `cmd/capture` and
`cmd/realcheck` for darwin/arm64, copies them over and runs them over ssh.
Browsers opened over ssh show on the session logged in at that Mac's
screen, so its user must be logged in there (Screen Sharing is enough).
Each Mac's one-time setup (capture CA, resolver, aioquic, Gatekeeper) is in
[Capturing browsers](capturing.md#browser-setup).

It is not yet checked whether a fully covered window throttles its page's
timers. If `h3-discovery` or
WebSocket e2e tests fail on timing after a parallel run, recapture them
with `-serial`.

### The Windows machine

It needs, once: OpenSSH Server (PowerShell as its shell is fine), the
browsers (Chrome; Firefox from Mozilla's installer, with its updates turned
off, below), Python with aioquic (`python -m pip install --user aioquic`,
for the scenarios that need it), and an administrator account that is also
logged in at the machine's screen (the console). Go is not needed. The
preflight checks each of these, and that `certutil` and `schtasks` are on
PATH.

The tool cross-compiles for windows/amd64, copies the binaries to
`%USERPROFILE%\mimicry-capture\refresh-<stamp>\` and runs each step there
through `powershell -NoProfile`. `capture` opens a visible Chrome or
Firefox on the logged-in desktop through a one-shot scheduled task, trusts
the run's capture CA in the machine's Root store only while it runs
(`certutil`, which asks nothing on screen; Firefox imports it from there,
`security.enterprise_roots.enabled`, as on macOS from the keychain), and
maps the test hosts with Chrome's
`--host-resolver-rules` or, in Firefox's throwaway profile,
`network.dns.localDomains` (the macOS Firefox profile, unchanged): no DNS
or hosts-file change. Firefox's `firefox.exe` is a launcher that starts
the browser process and exits, so the tool starts it with
`-wait-for-browser` and ends the launcher with its process tree. However
the run ends, the tool then runs `capture -cleanup` (the CA, leftover
scheduled tasks) and removes the directory (`-keep` keeps it). Each
capture run removes the CA as it ends, so the PC's browsers take turns,
in quick mode too.

Browser policies apply to the captured browser too, throwaway profile or
not, so the browsers must run on their defaults. Check
`HKLM\SOFTWARE\Policies` and `HKCU\SOFTWARE\Policies` (`Google\Chrome`,
`Microsoft\Edge`, `Mozilla\Firefox`) before a run: telemetry switches
change nothing on the wire, but others do. Edge's `ConfigureDoNotTrack`
made it send `dnt: 1` on every request but its first few (decision 0059),
so lift such keys for the run (export them with `reg export` first) and
import them again after.

Firefox updates itself between launches, which splits a run (the tool then
refuses it). Install the exact version from Mozilla's archive
(`archive.mozilla.org/pub/firefox/releases/<version>/win64/en-US/`,
checked against the release's `SHA256SUMS`) silently, with an INI file
turning off the maintenance service and the default-browser agent
(`"Firefox Setup <version>.exe" /S /INI=<file>`), then add an enterprise
policy, `distribution\policies.json` in its install directory:

```json
{
  "policies": {
    "DisableAppUpdate": true
  }
}
```

To capture a new version, install it over the old one the same way; the
policy stays.

Profiles inherit captures only from the same browser and platform, so a
browser's first refresh on Windows (`chrome-windows`, `firefox-windows`,
`edge-windows`) captures every scenario and replay whatever `-mode`
(unless `-scenarios` is given), starting from the macOS profile of the
same version (Edge's first anywhere: from Chrome's, above). Later ones
follow `-mode`.

Two things differ in what the Windows captures record, whatever the
browser does: the recorder's clock there moves in steps of about 0.5 ms,
so a packet and the other side's answer often share a timestamp (tests
that order both sides take the server's first on a tie), and quic-go's
NEW_TOKEN tokens are 89 or 90 bytes by the loopback RTT they encode (tests
compare whether a token is sent, not its length). The report names the
machines by role (this Mac, the second Mac, the Windows PC), never by their
ssh aliases.

### The Linux machine

A Linux profile is one per browser, not per distribution: Google's Chrome
`.deb`/`.rpm` and Mozilla's Firefox are the same programs on every
distribution (Ubuntu 24.04 and Fedora 45 sent identical first requests,
[decision 0058](decisions/0058-linux-profiles.md)). Use those builds: a
distribution's own Firefox or Chromium is another browser.

The machine needs, once: Chrome from Google's package (`google-chrome-stable`)
and Firefox from Mozilla (packages.mozilla.org's apt repository, or the
tarball with `distribution/policies.json` turning updates off, as on
Windows; never the snap, whose private `/tmp` the tool cannot reach); NSS's
`certutil` (`libnss3-tools`; Fedora `nss-tools`); Xvfb (`xvfb`; Fedora
`xorg-x11-server-Xvfb`); python3 with aioquic 1.0 or later, for QUIC
version 2 (`python3 -m pip install --user --break-system-packages
'aioquic>=1.0'` as that user: Ubuntu 24.04's `python3-aioquic` is
0.9.25, too old); and a
user that is not root, since Chrome refuses root without its sandbox
(ssh in as another user, or in WSL pass `-wsl-user`). Go is not needed.
The preflight finds the browsers, `certutil`, Xvfb (when the session has
no display, and always in WSL), aioquic (when a planned scenario needs
it), the user and the lowest port a user may bind (below); the plan
refuses to run while one is missing. Firefox is whatever `firefox` is on
`PATH`; the preflight cannot tell Mozilla's build from a snap or a
distribution's.

A scenario whose pages name https://'s default port (`urls`) needs the
capture tool, running as that user, to listen on port 443, which Linux
allows a user only from `net.ipv4.ip_unprivileged_port_start`, 1024 by
default. On a Linux machine of its own, lower it yourself (`sudo sysctl
-w net.ipv4.ip_unprivileged_port_start=443`, and in `/etc/sysctl.d/` to
keep it): the plan refuses otherwise. In WSL, refresh lowers it to 443 as
root before each capture step that needs it, only if it is higher; it
lasts until the WSL VM restarts.

The tool cross-compiles for linux/amd64 and runs each step in
`~/mimicry-capture/refresh-<stamp>/`. `capture` opens the browser on
Xvfb, a virtual X display it starts for the run, unless the session has a
display that answers: an ordinary windowed browser, not headless, on no
screen. Each browser trusts the run's capture CA in its own throwaway
profile (Chrome's NSS database under a `HOME` that is that profile;
Firefox's profile database, on pages across hosts), so nothing outside the
run's directory changes but, in WSL, the port setting above, and nothing
needs cleaning up.

**In WSL on the Windows machine** (`-linux <its alias> -wsl <distribution>
-wsl-user <user>`), each step runs through `wsl.exe -d <distribution> -u
<user>` over ssh to that machine, its command line base64-encoded so
neither PowerShell nor cmd quotes anything in it, and files go in and out
as tar streams (scp cannot reach a distribution). The distribution lives
as long as each step; nothing is configured on Windows, and in the
distribution only the port setting. WSLg's display is
never used: on the PC measured it accepted connections and never answered,
or its Wayland socket, which Firefox prefers, hung; the tool uses Xvfb
inside the distribution instead. Linux and Windows runs on one machine
take turns.

### Client-certificate scenarios

A full refresh (or a browser's first on a platform) captures
`client-cert-ec`, `client-cert-rsa` and their `-tls12` pair, from which
`hellogen` derives `tls.clientAuth`; a quick one inherits them, and
hellogen reads the inherited captures. Each needs the browser to hold an
identity and send it unasked; the capture tool installs it for the
scenario and removes it after ([Capturing](capturing.md#client-certificate-scenarios)):
a temporary keychain and Chromium policy entries on macOS, the user's
store and policy entries on Windows, the throwaway profile's NSS
database elsewhere. Firefox on macOS needs NSS's `pk12util` (`brew
install nss`). On Linux, Chrome's and Edge's policy file is root's: in
WSL refresh writes it as root around each capture step that needs it and
removes it after; on a Linux machine of its own the plan refuses until
you write it (`capture -scenarios client-cert-ec` there prints it).
Safari and the iPhone browsers are skipped (`Scenario.UnsupportedBy`):
their profiles have no `tls.clientAuth`. On Windows Firefox took 7 to 11
s over each handshake it answered from the user's store, so these pages
take about 90 s a sample there.

### The iPhone

Safari on iOS is captured from an iPhone (not an iPad, whose Safari sends
a Mac user agent) connected to this Mac by cable and on the same Wi-Fi
network ([decision 0051](decisions/0051-ios-safari-over-the-network.md)).
The capture tool runs here, serving the captures on `-listen` with a DNS
stub on port 53 for the run, and opens each sample's URL in the phone's
own Safari through devicectl over USB. Its one-time setup (the capture
CA, Safari's settings) and what each run needs (the phone's DNS pointed
at this Mac, unlocked, Auto-Lock Never) are in
[Capturing browsers](capturing.md#safari-on-an-iphone); the plan lists
them. Safari's version is the phone's iOS version, which the preflight
reads with `xcrun devicectl list devices` (`-ios-device <udid>` picks one
of several iPhones).

Whatever `-mode`, the iPhone captures `server.IOSScenarios`, three
samples each: the fingerprint layer, the scenarios the generators derive
from and the HTTP/3 failure pair (`h3-blackhole`, `h3-broken`; h3gen
takes their structure, not their timing over Wi-Fi), unattended, then
`proxy-connect` and `proxy-plain`, for which the capture tool asks
someone to set the phone's Wi-Fi proxy and waits for Enter on refresh's
standard input (see [Capturing browsers](capturing.md#safari-on-an-iphone));
about a quarter of an hour, someone at the phone only for the proxy settings.
A scenario is inherited from the captures of the macOS Safari profile it
starts from (`capturesFrom`, `internal/platform.Stack`) only where the
iPhone's own captures show no difference in its area;
`Scenario.NotInheritedOnIOS` keeps the others out, and the profile does
not measure them. realcheck is not run: it drives browsers on this Mac.
The report names the phone by role (the iPhone), never by its device
name or UDID.

`-browser chrome-ios` refreshes Chrome on the same iPhone the same way:
the same setup and the same `server.IOSScenarios`, opened through
devicectl in Chrome's own URL scheme. Its version is the installed Chrome
app's, which the preflight reads with `xcrun devicectl device info apps`.
Someone must be at the phone during `kinds-plain` to tap "Send anyway" on
Chrome's insecure-form warnings; the plan says so, and the capture tool
says when. Details are in
[Capturing browsers](capturing.md#chrome-on-an-iphone).

`-browser firefox-ios` refreshes Firefox on the same iPhone the same way,
opened through devicectl with `firefox://open-url`; nothing is asked at the
phone but the proxy settings. Its redirect scenarios may stall a sample
(Firefox sometimes does not open the next URL after "too many
redirects", even restarted): take them again as single-sample runs and join the trace files, as
[Capturing browsers](capturing.md#firefox-on-an-iphone) says.

## What a run does

1. **Preflight.** On each machine it checks the installed browser version,
   the console user, and whether the capture CA is trusted (on Windows,
   which trusts the CA only during a capture: administrator rights,
   `certutil` and `schtasks` instead). It also checks
   `/etc/resolver/test` (Safari), python3 with aioquic and Safari's proxy
   script (full mode). Finally it checks whether Gatekeeper still has to
   approve a fresh browser's first launch, a dialog nobody at an ssh
   session can answer. The version names the profile: a new major version
   gets a new profile copied from the browser's newest; the same major
   refreshes that profile in place. It prints the plan, the manual steps
   and any problems with the commands that fix them, then asks before
   capturing. `-yes` skips the question; `-dry-run` stops here. The plan is
   saved as `plan.json` in the staging directory.
2. **Capture** into `internal/harness/.refresh/<stamp>/<browser>/captures/`
   (gitignored). Committed captures are not touched. The run is refused if
   any capture names another profile or user agent, or if the installed
   version changed afterwards: Chrome and Firefox update themselves, and an
   update between two launches would split the run. A sample whose page
   moved to another connection before finishing is taken again, up to
   twice.
3. **Derive.** The previous profile is copied and renamed. From the new
   captures it takes the ClientHello, the HTTP/2 preface, and the
   navigation's headers over HTTP/2 and HTTP/1.1. Headers the request fills
   in (`cookie`, `referer`, `content-length`, `Host`) keep their places.
   When a browser shuffles its TLS extensions and the set is unchanged, the
   previous order is kept, so only a real change shows. `capturesFrom`
   lists every scenario not recaptured. Then `hellogen`, `h3gen` and
   `kindsgen` run on the staged profile. A generator that refuses a sample
   stops the run with its message; record the gap or fix the rule, then
   rerun with `-resume <staging dir>`.
4. **Install.** The staged captures go to `testdata/captures` and the
   profile to `profiles/data`. Beyond five supported versions, the oldest is
   marked `deprecated`. Then `go generate ./profiles` runs.
5. **Verify.** It runs the e2e and regenerate tests for the new profile
   (`MIMICRY_PROFILES=<name>`), then `cmd/realcheck` on the browser's
   machine. Realcheck takes over that machine's screen for a few minutes.
6. **Report** to `internal/harness/reports/refresh-<name>-<date>.md`: time
   per scenario against the estimate, inherited scenarios, version strings,
   every value that differs from the previous profile beyond version
   tokens, deprecations, test and realcheck results, and what is left to do
   by hand.

`-capture-only` stops after step 2 and `-derive-only` after step 3. Nothing
in the repository changes before step 4. `-scenarios name:samples,...`
replaces the mode's scenario set, to try the tool on one scenario, or, with
`-capture-only`, to add one scenario to existing profiles.
`-chrome-app` and `-firefox-app` run another build on every machine of the
run (Chrome for Testing, an older Firefox; [capturing](capturing.md)), and
the preflight reads that build's version, so the plan picks its profile.

## What stays manual

- Reviewing the report and any generator refusal; new gap entries.
- The evidence ledger (`profiles/EVIDENCE.md`), new gaps in its Not
  covered by any claim section included.
- Safari's proxy-credential scenarios (a person types the credentials) and,
  on the second Mac, its click scenario (no Accessibility over ssh). Safari's
  full refresh leaves them out, so the profile keeps or inherits them.
- New Safari versions come only with macOS updates. Updating the second Mac
  past macOS 26 ends Safari 26's captures.
- Committing: the tool never touches git.

What the tool runs underneath, and how to capture by hand:
[Capturing browsers](capturing.md).

Background: [decision 0035](decisions/0035-version-policy-and-capture-inheritance.md),
[decision 0047](decisions/0047-windows-profiles.md),
[example refresh report](../internal/harness/reports/refresh-chrome-156-macos-2026-10-26.md),
[Windows refresh report](../internal/harness/reports/refresh-chrome-156-windows-2026-10-29.md).
