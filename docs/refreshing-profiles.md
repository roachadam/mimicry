# Refreshing profiles

Browsers ship a new version every few weeks. `profiles.Chrome()`,
`profiles.Firefox()` and `profiles.Safari()` always mean the newest
built-in version. The newest five versions per browser are supported.
Older ones stay in the repository, marked deprecated. Keeping up is one
command, run in `internal/harness/`:

```sh
go run ./cmd/refresh -browser chrome,firefox -mode quick   # both at once, on the second Mac
go run ./cmd/refresh -browser safari -mode quick           # Safari 27, on the Mac you run it on
go run ./cmd/refresh -browser safari-mini -mode full       # Safari 26, on the second Mac
go run ./cmd/refresh -browser chrome -dry-run              # the plan only
go run ./cmd/refresh -browser chrome-windows -windows <alias>   # Chrome on a Windows machine
```

The setup assumes two Macs: the one you work on, and a second Mac kept on
macOS 26, reachable over ssh. Pass its ssh alias with `-mini` (default
`mac-mini`). Chrome on Windows needs a Windows machine reachable over
ssh: pass your ssh alias for it with `-windows` (no default).

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

Scenarios where the browser picks something per connection (extension
order, GREASE values, ECH lengths) take three samples; the rest take one.
Chrome and Firefox take `h3-discovery` and `h3-page` twice, without and
with `-h3-local-roots`, as their committed captures do.

Every other scenario is inherited from the browser's previous version. The
profile's `capturesFrom` field names, per scenario, the profile holding its
captures. Generators and e2e tests read those captures instead, with the
version tokens in header values rewritten to the new version. Any other
difference still fails. A quick refresh of one browser takes under ten
minutes of capturing.

**Full** recaptures every scenario and replay, three samples each. Run it
before a release, when a quick refresh shows a change beyond version
strings, or periodically. It takes about two hours per browser. The risk of
quick mode: a rule that changes in a quickly refreshed version goes
unnoticed until the next full refresh.

Tests split the same way. By default the e2e suite runs the newest profile
per browser; a full run (`moon run :test-full`) covers every supported one.

## Where each browser runs

| `-browser` | Machine | Notes |
|---|---|---|
| `chrome`, `firefox` | the second Mac (`-mini`) | at once in quick mode (`-serial` to not), one after the other in full mode |
| `safari` | the Mac you run it on | opens tabs in your own Safari window |
| `safari-mini` | the second Mac | Safari 26, since that Mac stays on macOS 26 |
| `chrome-windows` | the Windows machine (`-windows`) | profiles `chrome-<major>-windows`, for `profiles.ChromeWindows()`; the first one captures the full set |

The second Mac needs no Go. The tool cross-compiles `cmd/capture` and
`cmd/realcheck` for darwin/arm64, copies them over and runs them over ssh.
Browsers opened over ssh show on the session logged in at that Mac's
screen, so its user must be logged in there (Screen Sharing is enough).
Each Mac's one-time setup (capture CA, resolver, aioquic, Gatekeeper) is in
[Capturing browsers](capturing.md#browser-setup).

It is not yet checked whether a fully covered window throttles its page's
timers ([backlog](backlog.md#tooling-and-tests)). If `h3-discovery` or
WebSocket e2e tests fail on timing after a parallel run, recapture them
with `-serial`.

### The Windows machine

It needs, once: OpenSSH Server (PowerShell as its shell is fine), Chrome,
and an administrator account that is also logged in at the machine's
screen (the console). Go is not needed. The preflight checks each of these.

The tool cross-compiles for windows/amd64, copies the binaries to
`%USERPROFILE%\mimicry-capture\refresh-<stamp>\` and runs each step there
through `powershell -NoProfile`. `capture` opens a visible Chrome on the
logged-in desktop through a one-shot scheduled task, trusts the run's
capture CA in the machine's Root store only while it runs (`certutil`),
and maps the test hosts with `--host-resolver-rules`: no DNS or hosts-file
change. However the run ends, the tool then runs `capture -cleanup` (the
CA, leftover scheduled tasks) and removes the directory (`-keep` keeps
it).

Profiles inherit captures only from the same browser and platform, so the
first `chrome-windows` refresh captures every scenario and replay whatever
`-mode` (unless `-scenarios` is given), starting from the macOS profile of
the same version. Later ones follow `-mode`.

## What a run does

1. **Preflight.** On each machine it checks the installed browser version,
   the console user, and whether the capture CA is trusted. It also checks
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
replaces the mode's scenario set, to try the tool on one scenario.

## What stays manual

- Reviewing the report and any generator refusal; new gap entries.
- The evidence ledger (`profiles/EVIDENCE.md`) and the backlog.
- Safari's proxy-credential scenarios (a person types the credentials) and,
  on the second Mac, its click scenario (no Accessibility over ssh). Safari's
  full refresh leaves them out, so the profile keeps or inherits them.
- New Safari versions come only with macOS updates. Updating the second Mac
  past macOS 26 ends Safari 26's captures.
- Committing: the tool never touches git.

What the tool runs underneath, and how to capture by hand:
[Capturing browsers](capturing.md).

Background: [decision 0035](decisions/0035-version-policy-and-capture-inheritance.md),
[example refresh report](../internal/harness/reports/refresh-chrome-156-macos-2026-10-26.md).
