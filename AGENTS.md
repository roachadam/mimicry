# Working in mimicry

mimicry is a Go HTTP client that reproduces real browsers' network fingerprints, matched against captures of the real browsers ([README](README.md)). Accuracy first, then speed.

- **Modules:** three, so their dependencies never reach each other.
  - The library is the repo root (`github.com/roachadam/mimicry`).
  - `internal/harness/` captures the browsers and checks the profiles against them.
  - `internal/bench/` benchmarks mimicry against other clients in Docker ([its README](internal/bench/README.md)).
- **Decisions:** [docs/README.md](docs/README.md) indexes the decision records and concept docs. Read the decision before "fixing" something that looks odd.

## Commands

```sh
moon run :vet :lint :test   # the gate, every project (mimicry:test or harness:test for one)
moon run :test-full         # every supported profile, not only each browser's newest
```

- **Tool versions:** Go and moon are pinned in `.prototools`.
- **golangci-lint** is pinned in `.moon/tasks/go.yml` and run with `go run`, so it is built with the pinned Go. Run it through moon, never a system install.
  - Beyond the standard set, `.golangci.yml` enables (among others) bidichk, asciicheck, durationcheck, nilnesserr, reassign, usetesting, fatcontext, exptostd, mirror, nosprintfhostport, gocheckcompilerdirectives, testableexamples, dupword, thelper, recvcheck, errchkjson, musttag, predeclared, errname and intrange.
- **`:lint`** then runs:
  - gopls's analyzers (`.moon/scripts/gopls-check.sh`, gopls pinned there). Anything an editor underlines fails lint.
  - deadcode (`.moon/scripts/deadcode-check.sh`, pinned there) with `-test`. A function no test or main reaches fails lint if it is in an `internal/` package, except upstream's `internal/quic`. The public API and cmd entry points may be unreached.
- **`:test`** runs `go test -race -timeout 30m ./...` (`:test-full`: 60m).
  - The harness's e2e package alone takes over go test's default 10 minutes.
  - moon replays a cached result only when none of its inputs changed: the Go sources and the data the tests read (`testdata` directories, `profiles/data`; for the harness also the library's sources and profiles).
- **`:test-full`:** run it before a release, after a full refresh, and after a change that could differ between versions of a browser.
- **`MIMICRY_PROFILES`** picks the profiles the harness's tests check (`internal/harness/internal/testprofiles`, decision 0035):
  - unset: each browser's newest on each platform (`chrome-156-windows` beside `chrome-156-macos`);
  - `all` (what `:test-full` sets): every supported one;
  - a comma-separated list of built-in names and profile file paths (`MIMICRY_PROFILES=safari-26-macos`, `MIMICRY_PROFILES=/abs/path/chrome-157-macos.json`).
- **Bench:** `moon run bench:micro` (layers 1-2) and `bench:run` need Docker and are never part of `:test`. `:vet` and `:lint` cover the bench module.
  - `bench:run` is the default run: layers 1-4 and 6, trimmed to many clients with small requests.
  - `BENCH_FULL=1` runs every layer and the full matrix.
  - It writes a report in `internal/bench/reports/`.
- **Node SDK** (`bindings/node`, its [README](bindings/node/README.md)) is outside moon. Its tooling is Bun's (the version in its `package.json`'s `packageManager`), its tests Node's. In `bindings/node`:
  - `bun install --frozen-lockfile`;
  - `bun run check`: tsc, eslint, the generated code's drift;
  - `bun run test`: builds `mimicry-core` with Go first, then `node --test`.
  - With its dependencies installed, the harness's `TestNodeMatchesGo*` run instead of skipping.
- **Python SDK** (`bindings/python`, decision 0069) is a moon project, with tools from uv (pinned in its `uv.lock`).
  - `moon run python:test python:lint`. `python:core` builds the core into the package first; `python:proto` regenerates its messages.
  - Moon skips them in CI, which runs them in `test-python`.
  - The harness's `TestPythonSDKMatchesGo*` run only with `MIMICRY_PYTHON` naming a Python that imports the SDK (`bindings/python/.venv/bin/python`; a relative path is the repository root's).
- **Other platforms:** host lint never loads another OS's build-tagged files. Before pushing platform-specific code, lint it for Linux and Windows ([CI: Linting other platforms](docs/ci.md#linting-other-platforms)).
- **CI** runs on every pull request, push to main, nightly and by hand; `ci-ok` is the check to require. Jobs, harness tiers and shards, `vuln`, CodeQL and Dependabot: [docs/ci.md](docs/ci.md).
  - Adding a slow test, or slowing one down, means updating its line in `.github/harness-shards.txt` ([Shards](docs/ci.md#shards)).
  - Fix a vulnerability `vuln` reports by moving the dependency; never suppress it.
- **Releases:** pushing a `v*` tag runs `.github/workflows/release.yml`, which waits for the maintainer's approval to publish ([docs/releasing.md](docs/releasing.md)). Its jobs never use a cache, and the publish jobs check out no code; keep it so.
- **Long runs on a Mac laptop:** it sleeps on battery with the lid closed, which freezes tests.
  - Run long suites under `caffeinate -i` on AC power;
  - or cross-compile the test binaries (`go test -c`) and run them on another Mac over ssh (it needs no Go).

## Hard rules

- **Evidence.** Browser wire captures are the evidence ([decision 0003](docs/decisions/0003-evidence-rule.md)): capture first, then model.
  - Browser source code and other clients' presets are leads, never evidence.
  - A feature the samples disagree on is modelled or pinned as a known gap. Never pick one sample's value.
- **Unmeasured is an error.** Asking for what the profile never measured fails with an error wrapping `ErrNotMeasured` that names what is missing, and nothing is sent (decision 0008). Never guess a value or an order.
- **Defaults are the browser.** With no options a request is what the browser sends. Every deviation is one the caller asks for explicitly (`HeaderOrderKey`, `Place`, `Exact`, `DisableHTTP3`, `DisableCoalescing`, `MaxConnsPerHost`, ...; decision 0005).
  - The exceptions:
    - timing on QUIC failure paths: faster by default, the browser's with `BrowserQUICFallback` (decision 0036);
    - the delay before a new QUIC connection a request waits for: none by default, the browser's with `BrowserDialDelay` (decision 0050);
    - proxy credentials sent with the first request; the browser's challenge flow with `BrowserProxyAuth` (decision 0039);
    - the 30 s request timeout (`Timeout`, decision 0041);
    - closing connections idle for 90 s (`IdleConnTimeout`, decision 0040);
    - forgetting a proxy that falls out of use (beyond the 1,024 most recent and unused for 10 s), with its TLS sessions and challenge state, so rotating proxies keeps memory bounded (decision 0040).
  - An exception follows decision 0037. A browser behaviour that costs significant performance at a low-ish detection risk defaults to the faster setting. It is documented plainly in the README's "Deviations from the browser", with a `Config` option restoring the browser's.
  - A behaviour that is faster on some networks and slower on others (HTTP/2 coalescing) keeps the browser's default and gets an opt-out.
  - Wire behaviour beyond the caller's own requests is listed in [what-mimicry-does.md](docs/concepts/what-mimicry-does.md), with the option that changes it. A new one is added there.
- **Scope** ([decision 0029](docs/decisions/0029-scope-what-gets-modelled.md)): model a behaviour an anti-bot vendor plausibly checks, or one a user would notice (a stall, a failure). Nothing else is modelled.
  - Record it where its reader looks; there is no backlog file ([0029, amendment](docs/decisions/0029-scope-what-gets-modelled.md#amendment-2026-10-30-no-backlog)):
    - planned work: a [GitHub issue](https://github.com/roachadam/mimicry/issues);
    - a decision, "not planned" included: its decision record (an amendment, with what would reopen it);
    - a limit a caller notices: the matching guide's Limits or Not supported section in `docs/concepts/`;
    - a known fingerprint difference with no planned work: one entry in [`profiles/evidence/gaps.md`](profiles/evidence/gaps.md#not-covered-by-any-claim).
- **Generated profile sections are never hand-edited.** They are derived from captures:
  - hellogen: the ClientHello shape, `tls.quic`, `tls.clientAuth`, `tls.realEch`, `http2.openingRecords`, `http2.goawayReply`, `http1.uploadRecords`, `proxy.https` and `proxy.socks4`;
  - h3gen: `quic` and `http3`;
  - kindsgen: `kinds`, `context`, navigation `variants`, `emptyBody`, `clientHints` and `urls`.
  - Run them in that order, hellogen → h3gen → kindsgen (in `internal/harness/`, `-profile ../../profiles/data/<name>.json`), then `go generate ./profiles`.
  - Each `TestRegenerateReproducesProfiles` fails while a profile differs from what its generator derives. Details: [Deriving a profile](docs/capturing/deriving.md#deriving-a-profile).
- **Profiles.** One complete file per browser version and platform, nothing inherited between files (decision 0004).
  - Captures may be inherited per scenario through `capturesFrom` (decision 0035).
  - A profile without its captures in `internal/harness/testdata/captures/` is not finished.
  - Examples, docs and tests use the newest-version accessors (`profiles.ChromeMacOS()`, `FirefoxMacOS()`, `SafariMacOS()`, `ChromeWindows()`), unless the version matters to what they show. An example whose output prints the user agent keeps its versioned accessor.
  - Adding a sixth version of a browser on a platform marks that platform's oldest `deprecated` in the same commit.
- **Docs in the same commit.**
  - A change that makes, changes or reverses a design decision adds or updates its record in `docs/decisions/`: copy `template.md`, take the next number, add it to the index in `docs/README.md`. Write the reasoning while it is fresh; the options rejected and the evidence matter more than polish.
  - A feature that lands updates its concept doc in `docs/concepts/`.
  - A profile change that adds, removes or re-sources a claim updates its row in the evidence ledger ([`profiles/EVIDENCE.md`](profiles/EVIDENCE.md) indexes its files in `profiles/evidence/`).
  - A gap found or pinned (a new `sampleGaps`-style entry, a known-gap row in the ledger) is recorded as the Scope rule says: usually an entry in `profiles/evidence/gaps.md`, or an issue.
  - A gap closed is removed there, with its ledger row and pin, and its issue closed.
- **Licensing.** mimicry is MIT (`LICENSE`), every module included. Only permissive licences (MIT, BSD, Apache-2.0, zlib, ISC), for dependencies too. Code copied or ported from another project:
  - keeps its upstream licence and copyright notice next to it: a `LICENSE` file in the package, or `LICENSE-<project>` when a package draws on several; per-file headers kept;
  - says in its package comment that it is a copy or an altered version, and of what (`internal/zdeflate`: `deflate.go`; `internal/quic`: upstream's `doc.go`);
  - gets a row in the README's Licence table in the same commit.
- **Clean cutover.** When something replaces something, migrate every caller and delete the old path: no shims, aliases or compatibility wrappers. Deprecated profiles are the one deliberate exception (decision 0035).
- **Git.** The repository is on GitHub (`github.com/roachadam/mimicry`).
  - Never push, tag or open a pull request without the maintainer's explicit go.
  - Stage files by path and read `git status` before committing, never `git add -A`.
  - Before a commit or pull request, read `git diff --stat`. A `Bin` line that is not a capture (`.json.gz`) or an image is a mistake.
  - A new command adds its binary's name to `.gitignore`.

## Package boundaries

- **Public packages:** the root package, `cookies`, `profile`, `profiles` and `cmd/pins`. `cmd/profilegen` writes the `profiles` accessors. Everything else is in `internal/`, flat, one package per concern.
- **Import rules** are `depguard` rules in `.golangci.yml`. A rule says what its packages' non-test files may import, and lint fails on anything else.
  - Most `internal/` packages have one.
  - The root package, `cookies`, `cmd/pins`, `cmd/profilegen`, `internal/headerorder`, `internal/site` and some harness packages have none; only the rule keeping `internal/testutil` out of non-test files applies.
  - Change a rule on purpose, never just to quiet the linter.

Package by package:

- **The root package** is the public API: thin methods over `internal/`, and how a request goes out as its browser sends it (`resolve.go`, `redirect.go`). That covers each kind's lists and variant, the headers the page decides, credentials, partition, redirect hops and header placements.
  - `internal/` never imports it. The root converts its public types to the internal packages' inputs and never aliases their types.
  - The exception is `internal/rpc/server` and `internal/rpc/convert`, `mimicry-core`'s server for the language SDKs, which serve the public API itself (decision 0069).
- **`internal/rpc`** is the language bindings' core (decision 0069). Only these packages import protobuf's runtime.
  - `wire`: the framed protocol;
  - `pb`: the Go code generated from `proto/`, never edited by hand (`moon run mimicry:proto`);
  - `convert`: messages ↔ public types;
  - `server`: the calls;
  - `client`: a Go client of the protocol, for tests.
- **`internal/cors`:** when a CORS preflight is needed, whether a preflight's answer or a redirect allows the request, and the preflight cache.
- **`internal/pins`:** `Config.Pins` parsed, looked up by host and matched against verified chains.
- **uTLS:** outside tests, only `internal/handshake` and `internal/quic` (QUIC's TLS, decision 0027) import `github.com/roachadam/utls`.
- **Request path:** only `internal/engine/native` assembles it (pool → dial → handshake → h1/h2, or QUIC → h3).
- **`profile`** imports the standard library only, and `internal/checked`, through which the root package reads the Spec a `profile.Profile` holds. Public packages never import from `internal/harness/`.
- **`net/http`:** use its types in the public API; do not fork it (decision 0002).
  - Header order comes from the profile (the navigation, or the request's kind and its variant).
  - `HeaderOrderKey` overrides it per request, and `HeaderPlacement` moves single headers within it (decision 0030).
  - Pseudo-header order comes from the profile only.
- **Dialing:** one dial path (`internal/dial`). Every network step honours the context and its deadline. Never hold a lock across network I/O.
- **Profiles are read-only.** A `profile.Profile` holds a checked Spec shared by every Profile of equal content (`profile/share.go`, decision 0044 and its amendment).
  - The root and everything under `internal/` only read it: no writes, no `append` into its slices.
  - To change one, use `Profile.With`, or edit `Profile.Spec()` (a deep copy) in tests of internal packages.

## The uTLS fork

mimicry depends on `github.com/roachadam/utls` at a version tag of its `main` branch (`v1.8.3` on).

- **Moving to newer fork changes:**
  1. Merge them to the fork's `main` with a rebase merge; its `PATCHES.md` keeps one patch per commit.
  2. Tag the merged commit with the next patch version. Use a minor or major bump past any upstream release an upstream sync brings in.
  3. Run `go get github.com/roachadam/utls@<tag>` in every module (root, `internal/harness`, `internal/bench`).
- **Trying an unmerged fork change:** use a local `go.work` that includes `../utls`. It is gitignored and must never be committed.

## Code style

- Configuration is a struct (`Config`), not functional options or boolean parameters.
- Code that depends on the current time takes a `clockwork.Clock` (`github.com/jonboulle/clockwork`): `clockwork.NewRealClock` in production, `clockwork.NewFakeClockAt` in tests. Advance the fake clock instead of sleeping.
- Imports are grouped by goimports with `-local github.com/roachadam/mimicry` (the linter checks it).
- Each package has a short package comment above `package` in its primary file, the one named after the package or its main type.
  - A paragraph or two: what the package is for, and any invariant that isn't obvious.
  - No `doc.go`, except upstream's in `internal/quic`.
- Write function and method bodies on multiple lines, simple accessors and test stubs included: no `func (p *Profile) Name() string { return p.name }`.
- Write struct declarations and populated struct literals one field per line, with the closing brace on its own line.
  - This includes single-field structs, constructor return values and structs passed as arguments.
  - Empty `struct{}` types and empty literals may stay compact.

```go
cfg := mimicry.Config{
    Profile:     profiles.ChromeMacOS(),
    DialTimeout: 30 * time.Second,
}
```

## Writing docs

Docs are read by people scanning for one fact. Write for that.

- **Short sentences**, one fact each. Split a sentence that needs a semicolon or a third clause.
- **Bullets over paragraphs.** A paragraph longer than about five lines becomes a lead sentence and a bullet list. Nest bullets for detail, at most two levels.
- **Headings every screen or so**, so a reader can jump. Link to a heading rather than repeating its content.
- **Tables hold short values.** A cell is a class, a value or a few words. Put sample counts and explanations in numbered notes under the table, and reference them from the cell (`W [1]`).
- **Code formatting is for things a reader would type or search for**: identifiers, field paths, commands, file names, header names. Not for ordinary words, scenario names in running prose, or emphasis.
- **Bold sparingly**: a list item's label, or the one term a paragraph defines.
- **Repeated structure, fixed labels.** A list of like items (gaps, profiles, scenarios) gives each item the same labelled sub-bullets in the same order.
- **No change logs in prose.** What changed and when lives in git and the reports; a doc says what is true now, with a date only where the date matters to the reader.
- **One topic per file.** A file covering several topics, or prose past about 400 lines, is split into a folder with an index file that says what each part covers. A ledger of like items under headings (the gap list, the profile notes) may run longer.

## Tests

- **Tools:** the standard `testing` package with hand-written fakes; no assertion libraries.
- **Deterministic:** inject randomness and clocks rather than depending on them.
- **Prefer e2e and integration tests.**
  - A behaviour browsers show belongs in an e2e replay of its capture (`internal/harness/e2e`), not in a unit test that restates the rule.
  - Profile accuracy is proven there, never by unit tests that restate a profile's values.
  - Each e2e test's comment says what it checks.
- **Unit or raw-peer tests** only for what no capture can reach, and only when the failure would be serious:
  - a crash, a deadlock or stall;
  - a security bug (injection, smuggling, leaked credentials);
  - a hostile server;
  - an error path (`ErrNotMeasured`, refused requests).
  - One table test per such area, not one test per case.
  - QUIC failures no capture reaches are rows of `TestQUICInterop`. A fix there adds a row that fails without it.
  - Behaviour that depends on time is unit-tested with a fake clock against a raw peer (`internal/h2`: Chrome's 5 s credit return, its idle PING).
- **Mutation-check:** before deleting or skipping a unit test because e2e covers it, break the code and check that an e2e test fails.

## Captures and refreshing profiles

- **Refreshing:** bring a profile up to the installed browser with `go run ./cmd/refresh -browser chrome,firefox -mode quick` (in `internal/harness/`; `-dry-run` first).
  - It captures, derives, regenerates and tests, but never touches git.
  - Review its report, update the evidence ledger (`profiles/evidence/`: the profile's notes, and new gaps in `gaps.md`), then commit.
  - See [Refreshing profiles](docs/refreshing-profiles.md).
- **What to refresh:** the weekly `.github/workflows/release-watch.yml` (`cmd/releasewatch`) keeps one issue open while a stable release or Chrome's root store trust anchors are ahead of the newest profiles ([Knowing when to refresh](docs/refreshing-profiles.md#knowing-when-to-refresh)).
- **Captures:** everything about them (the capture tool, scenarios, per-browser setup, the generators, `tracestat`, `diff`, `clientcapture`, `interop`, `realcheck`) is in [docs/capturing.md](docs/capturing.md). Two rules matter here:
  - Commit only captures from visible windows (headless Chrome says `HeadlessChrome`).
  - Never commit `realcheck` output (it holds the public IP), or a Chrome net log recorded for `pageshape` (it holds real cookies; delete it).
