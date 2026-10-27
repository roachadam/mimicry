# Refreshing profiles

Browsers ship a new version every few weeks. `profiles.Chrome()`,
`profiles.Firefox()` and `profiles.Safari()` always mean the newest
built-in version, the newest five versions per browser are supported, and
older ones stay in the repository, deprecated
([decision 0035](../decisions/0035-version-policy-and-capture-inheritance.md)). Keeping up is one
command, run in `harness/`:

```sh
go run ./cmd/refresh -browser chrome,firefox -mode quick   # both at once, on the Mac mini
go run ./cmd/refresh -browser safari -mode quick           # Safari 27, on this Mac
go run ./cmd/refresh -browser safari-mini -mode full       # Safari 26, on the mini
go run ./cmd/refresh -browser chrome -dry-run              # the plan only
```

## Two modes

A new version rarely changes more than its version strings, and most of
what a profile describes (cookie rules, CORS, redirects, proxies, HTTP/3
failure handling, connection reuse) changes far less often than the
fingerprint anti-bot vendors read. So there are two kinds of refresh.

**Quick** recaptures only the fingerprint layer, `server.QuickScenarios`:

| Scenario | Samples | What it shows |
|---|---|---|
| default page (`page`) | 3 | TLS ClientHello, HTTP/2 preface and SETTINGS, the navigation's headers |
| `http1` | 1 | the navigation over HTTP/1.1, casing and order |
| `kinds`, `kinds-http1`, `kinds-plain` | 1 each | every request kind's headers over HTTP/2, HTTP/1.1 and plain HTTP |
| `websocket`, `websocket-h2` | 1 each | WebSocket handshakes |
| `h3-discovery`, `h3-page` | 3 each | the QUIC ClientHello, transport parameters and HTTP/3 SETTINGS |

Three samples where the browser picks something per connection (extension
order, GREASE values, ECH lengths), one elsewhere. Chrome and Firefox take
`h3-discovery` and `h3-page` twice, without and with `-h3-local-roots`, as
their committed captures do. Every other scenario is inherited from the
browser's previous version: the profile's `captures_from` names, per
scenario, the profile holding its captures, and every generator and e2e
test reads those instead, with the version tokens in header values
rewritten to the new version's (anything else that differs still fails).
A quick refresh of one browser takes about 20 minutes of capturing
(estimate; the report records the measured times).

**Full** recaptures every scenario and replay, three samples each: before a
release, when a quick refresh shows a change beyond version strings, or
periodically. About two hours per browser. The risk the quick mode takes,
and why full runs are still needed, is in decision 0035: a rule that
changes in a version that was only refreshed quickly goes unnoticed until
the next full refresh.

Tests split the same way: by default the e2e suite runs the newest profile
per browser; a full run (`moon run :test-full`) covers every supported one.

## Where each browser runs

| `-browser` | Machine | Notes |
|---|---|---|
| `chrome`, `firefox` | the Mac mini (`-mini`, your ssh alias) | at once in quick mode (`-serial` to not), one after the other in full mode |
| `safari` | this Mac | opens tabs in your own Safari window |
| `safari-mini` | the Mac mini | Safari 26: the mini stays on macOS 26 |

The mini has no Go: the tool cross-compiles `cmd/capture` (and later
`cmd/realcheck`) for darwin/arm64, copies them to
`~/mimicry-capture/refresh-<stamp>/` there and runs them over ssh. Browsers
opened over ssh show on the session logged in at the mini's screen, so its
user must be logged in there (Screen Sharing is enough).

Chrome and Firefox at once on one Mac do not share anything a capture
depends on: each capture tool process has its own servers on free ports;
the DNS stub and the proxy script on fixed ports serve only Safari; no quick
scenario clicks or needs a dialog answered (Chrome's and Firefox's clicks
and proxy credentials go over DevTools and WebDriver BiDi anyway, not the
system's mouse). Not yet checked: whether a browser window entirely covered
by the other's throttles its page's timers, which would stretch the pauses
of `h3-discovery` and the WebSocket pages; if their e2e tests fail on timing
after a parallel run, recapture them with `-serial`.

## What a run does

1. **Preflight.** It runs `capture -preflight` where each browser runs: the
   installed version (Info.plist), the console user, whether the capture CA
   is trusted (the capture tool's own check; Safari needs it, Firefox's
   pages across hosts need it, and Chrome ignores Alt-Svc without it),
   `/etc/resolver/test` (Safari), python3 with aioquic (`h3-v2`, full mode),
   Safari's proxy script (full mode), and whether Gatekeeper still has to
   approve a freshly installed browser's first launch (a dialog nobody at an
   ssh session can answer). The version names the profile: a new major is a
   new profile copied from the browser's newest; the same major refreshes
   that profile in place. It prints the plan (scenarios, samples, estimated
   times, inherited scenarios), the manual steps and the problems, with the
   commands that fix them, and asks before capturing (`-yes` does not;
   `-dry-run` stops here). The plan is saved as `plan.json` in the staging
   directory.
2. **Capture**, into `harness/.refresh/<stamp>/<browser>/captures/`
   (gitignored); committed captures are not touched. The capture tool takes
   per-scenario sample counts (`-scenarios page:3,http1:1`) and writes a
   summary line per scenario (`-summary`: profile name, user agent, samples,
   seconds). The run is refused if any capture names another profile or
   user agent, or the installed version differs afterwards: Chrome and
   Firefox update themselves, and an update applied between two launches
   would split the run.
3. **Derive.** The previous profile is copied and renamed; from the new
   captures it takes the ClientHello (`tls` but for what hellogen derives),
   the HTTP/2 preface (`settings`, `window_update`, `priorities`,
   `headers_priority`, `pseudo_header_order`, `first_stream_id`,
   `stream_window_update`), the navigation's `headers` and the HTTP/1.1
   navigation's `http1.headers`. Headers the request fills in (slots:
   `cookie`, `referer`, `content-length`, `Host`) keep their places after
   the same header as before. A shuffling browser's extensions keep the
   previous profile's order when the types are the same, so only a real
   change shows. `captures_from` lists every scenario not recaptured. Then
   `hellogen`, `h3gen` and `kindsgen` run on the staged profile over an
   overlay of the committed captures with the staged ones on top. A
   generator that refuses a sample stops the run with its message; pin the
   gap or fix the rule, then `-resume <staging dir>`.
   `TestDeriveReproducesProfiles` (in `cmd/refresh`) derives every built-in
   profile from its own captures and requires it unchanged.
4. **Install.** The staged captures go to `testdata/captures` (in place:
   the recaptured scenarios' old files first), the profile to
   `profiles/data`; beyond five supported versions of the browser the
   oldest is marked `deprecated`; `go generate ./profiles`.
5. **Verify.** `MIMICRY_PROFILES=<name>` e2e and regenerate tests (under
   `caffeinate -i`), then `cmd/realcheck` with the browser where it runs
   (it takes over that machine's screen for a few minutes).
6. **Report**, `harness/reports/refresh-<name>-<date>.md`: the measured
   time of every scenario against the estimate, the inherited scenarios,
   the version strings, every value that differs from the previous profile
   once its version tokens are rewritten (from the captures, generated, or
   hand-written), deprecations, the test and realcheck results, and what is
   left to do by hand (`profiles/EVIDENCE.md`, `docs/backlog.md`).

`-capture-only` stops after step 2 and `-derive-only` after step 3 (its
report goes to the staging directory); nothing in the repository changes
before step 4. `-scenarios name:samples,...` replaces the mode's set, to
try the tool on one scenario.

## What stays manual

- Reviewing the report and any generator refusal; new gap entries.
- The evidence ledger and the backlog.
- Safari's proxy-credential scenarios (a person types the credentials, with
  the device profile installed) and, on the mini, its click scenario (no
  Accessibility over ssh): left out of Safari's full refresh, so the
  profile keeps or inherits them.
- New Safari versions come only with macOS updates; updating the mini past
  macOS 26 ends Safari 26's captures.
- Committing: the tool never touches git.
