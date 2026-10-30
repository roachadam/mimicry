# Working in mimicry

mimicry is a Go HTTP client that reproduces real browsers' network fingerprints, matched against captures of the real browsers ([README](README.md)). Accuracy first, then speed. The library module is the repo root (`github.com/roachadam/mimicry`); `internal/harness/` is a separate module that captures the browsers and checks the profiles against them; `internal/bench/` is another, benchmarking mimicry against other clients in Docker ([its README](internal/bench/README.md)), so their dependencies never reach the library or the harness.

[docs/README.md](docs/README.md) indexes the decision records and concept docs. Read the decision before "fixing" something that looks odd.

## Commands

Go and moon are pinned in `.prototools`. golangci-lint is pinned in `.moon/tasks/go.yml` and run with `go run`, so it is built with the pinned Go: run it through moon, never a system install. `:lint` then runs gopls's analyzers (`.moon/scripts/gopls-check.sh`, gopls pinned there): anything an editor underlines fails lint. Last, `.moon/scripts/deadcode-check.sh` runs deadcode (pinned there) with `-test`: a function no test or main reaches fails lint if it is in an `internal/` package (except upstream's `internal/quic`); the public API and cmd entry points may be unreached. Beyond the standard set, `.golangci.yml` enables (among others) bidichk, asciicheck, durationcheck, nilnesserr, reassign, usetesting, fatcontext, exptostd, mirror, nosprintfhostport, gocheckcompilerdirectives, testableexamples, dupword, thelper, recvcheck, errchkjson, musttag, predeclared, errname and intrange.

```sh
moon run :vet :lint :test   # the gate, every project (mimicry:test or harness:test for one)
moon run :test-full         # every supported profile, not only each browser's newest
```

- `:test` runs `go test -race -timeout 30m ./...` (`:test-full` 60m): the harness's e2e package alone takes over go test's default 10 minutes. moon replays a cached result only when none of its inputs changed: the Go sources and the data the tests read (`testdata` directories, `profiles/data`; for the harness also the library's sources and profiles).
- `moon run bench:micro` (layers 1-2) and `bench:run` (the default run, layers 1-4 and 6 trimmed to many clients with small requests; `BENCH_FULL=1` for every layer and the full matrix; a report in `internal/bench/reports/`) need Docker and are never part of `:test`; `:vet` and `:lint` cover the bench module.
- Host lint never loads another OS's build-tagged files (`*_linux.go`, `*_windows.go`), and CI lints on Linux (the harness also for Windows). Before pushing platform-specific code, build the pinned golangci-lint for the host (`GOBIN=/tmp/gclbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<version in .moon/tasks/go.yml>`) and run `GOOS=linux` and `GOOS=windows` `golangci-lint run ./...`, with `go build ./...` and `go vet ./...`, in each module the code is in.
- `MIMICRY_PROFILES` picks the profiles the harness's tests check (`internal/harness/internal/testprofiles`, decision 0035): unset, each browser's newest on each platform (`chrome-156-windows` beside `chrome-156-macos`); `all` (what `:test-full` sets), every supported one; or a comma-separated list of built-in names and profile file paths (`MIMICRY_PROFILES=safari-26-macos`, `MIMICRY_PROFILES=/abs/path/chrome-157-macos.json`).
- Run `:test-full` before a release, after a full refresh, and after a change that could differ between versions of a browser. Pushing a `v*` tag runs it in CI (`.github/workflows/release.yml`) and opens a draft GitHub Release.
- CI (`.github/workflows/ci.yml`) runs on every pull request and push to main: `lint` (`moon run :vet :lint`, then the harness built, vetted and golangci-linted for Windows), `test-root` (the library's race tests; bench builds and vets) and `test-harness (1)` to `(5)`; `ci-ok` passes when they all do, and is the check to require. A pull request touching only `*.md`, `docs/` or `internal/bench/reports/` skips them. Each job must finish in 15 minutes, so the harness's tests are split into shards by `.github/scripts/harness-shard.sh`: every top-level test is a unit weighed by its seconds in `.github/harness-shards.txt` (2 if unlisted; a `Test/regex` line splits a test's subtests), dealt heaviest first to the least-loaded shard. Adding a slow test, or slowing one down, means adding or updating its line there and checking the split with `bash .github/scripts/harness-shard.sh plan 5`.
- A Mac laptop sleeps on battery with the lid closed, which freezes tests: run long suites under `caffeinate -i` on AC power, or cross-compile the test binaries (`go test -c`) and run them on another Mac over ssh (it needs no Go).

## Hard rules

- **Evidence.** Browser wire captures are the evidence ([decision 0003](docs/decisions/0003-evidence-rule.md)): capture first, then model. Browser source code and other clients' presets are leads, never evidence. A feature the samples disagree on is modelled or pinned as a known gap; never pick one sample's value.
- **Unmeasured is an error.** Asking for what the profile never measured fails with an error wrapping `ErrNotMeasured` that names what is missing, and nothing is sent (decision 0008). Never guess a value or an order.
- **Defaults are the browser.** With no options a request is what the browser sends; every deviation is one the caller asks for explicitly (`HeaderOrderKey`, `Place`, `Exact`, `DisableHTTP3`, `DisableCoalescing`, `MaxConnsPerHost`, ...; decision 0005). The exceptions are timing on QUIC failure paths, faster by default and the browser's with `BrowserQUICFallback` (decision 0036); proxy credentials sent with the first request, the browser's challenge flow with `BrowserProxyAuth` (decision 0039); the 30 s request timeout (`Timeout`, decision 0041); and closing connections idle for 90 s (`IdleConnTimeout`, decision 0040). An exception follows the rule of decision 0037: a browser behaviour that costs significant performance at a low-ish detection risk defaults to the faster setting, documented plainly in the README's "Deviations from the browser", with a `Config` option restoring the browser's; a behaviour that is faster on some networks and slower on others (HTTP/2 coalescing) keeps the browser's default and gets an opt-out. Wire behaviour beyond the caller's own requests is listed in [what-mimicry-does.md](docs/concepts/what-mimicry-does.md) with the option that changes it; a new one is added there.
- **Scope** ([decision 0029](docs/decisions/0029-scope-what-gets-modelled.md)): model a behaviour an anti-bot vendor plausibly checks, or one a user would notice (a stall, a failure). Everything else goes to [docs/backlog.md](docs/backlog.md).
- **Generated profile sections are never hand-edited.** The ClientHello shape, `tls.quic` and `http2.openingRecords` (hellogen), `quic` and `http3` (h3gen), `kinds`, `context` and navigation `variants` (kindsgen) are derived from captures. Run them in that order, hellogen → h3gen → kindsgen (in `internal/harness/`, `-profile ../../profiles/data/<name>.json`), then `go generate ./profiles`; each `TestRegenerateReproducesProfiles` fails while a profile differs from what its generator derives. Details: [docs/capturing.md](docs/capturing.md#deriving-a-profile).
- **Profiles.** One complete file per browser version and platform, nothing inherited between files (decision 0004); captures may be inherited per scenario through `capturesFrom` (decision 0035). A profile without its captures in `internal/harness/testdata/captures/` is not finished. Examples, docs and tests use the newest-version accessors (`profiles.Chrome()`, `Firefox()`, `Safari()`, `ChromeWindows()`) unless the version matters to what they show (an example whose output prints the user agent keeps its versioned accessor). Adding a sixth version of a browser on a platform marks that platform's oldest `deprecated` in the same commit.
- **Docs in the same commit.** A change that makes, changes or reverses a design decision adds or updates its record in `docs/decisions/` (copy `template.md`, next number, add it to the index in `docs/README.md`); write the reasoning while it is fresh: the options rejected and the evidence matter more than polish. A feature that lands updates its concept doc in `docs/concepts/`. A profile change that adds, removes or re-sources a claim updates its row in `profiles/EVIDENCE.md`. A gap found or pinned (a new `sampleGaps`-style entry, a known-gap row in EVIDENCE.md) is added to `docs/backlog.md`; a gap closed is removed there, with its ledger row and pin.
- **Licensing.** mimicry is MIT (`LICENSE`), every module included. Code copied or ported from another project keeps its upstream licence and copyright notice next to it (a `LICENSE` file in the package, or `LICENSE-<project>` when a package draws on several; per-file headers kept), says in its package comment that it is a copy or an altered version and of what (`internal/zdeflate`: `deflate.go`; `internal/quic`: upstream's `doc.go`), and gets a row in the README's Licence table in the same commit. Only permissive licences (MIT, BSD, Apache-2.0, zlib, ISC), for dependencies too.
- **Clean cutover.** When something replaces something, migrate every caller and delete the old path: no shims, aliases or compatibility wrappers. (Deprecated profiles are the one deliberate exception, decision 0035.)
- **Git.** The repository is on GitHub (`github.com/roachadam/mimicry`). Never push, tag or open a pull request without the maintainer's explicit go.

## Package boundaries

The public packages are the root package, `cookies`, `profile`, `profiles` and `cmd/pins`; `cmd/profilegen` writes the `profiles` accessors; everything else is in `internal/`, flat, one package per concern. Import boundaries are `depguard` rules in `.golangci.yml`: a rule says what its packages' non-test files may import, and lint fails on anything else. Most `internal/` packages have one; the root package, `cookies`, `cmd/pins`, `cmd/profilegen`, `internal/headerorder`, `internal/site` and some harness packages have none (only the rule keeping `internal/testutil` out of non-test files applies). Change a rule on purpose, never just to quiet the linter.

- The root package is the public API, thin methods over `internal/`, and how a request goes out as its browser sends it (`resolve.go`, `redirect.go`): each kind's lists and variant, the headers the page decides, credentials, partition, redirect hops, header placements. `internal/` never imports it: the root converts its public types to the internal packages' inputs and never aliases their types.
- `internal/cors`: when a CORS preflight is needed, whether a preflight's answer or a redirect allows the request, and the preflight cache.
- `internal/pins`: `Config.Pins` parsed, looked up by host and matched against verified chains.
- Outside tests, only `internal/handshake` and `internal/quic` (QUIC's TLS, decision 0027) import `github.com/roachadam/utls`.
- Only `internal/engine/native` assembles the request path (pool → dial → handshake → h1/h2, or QUIC → h3).
- `profile` imports the standard library only. Public packages never import from `internal/harness/`.
- Use `net/http` types in the public API; do not fork `net/http` (decision 0002). Header order comes from the profile (the navigation, or the request's kind and its variant); `HeaderOrderKey` overrides it per request and `HeaderPlacement` moves single headers within it (decision 0030). Pseudo-header order comes from the profile only.
- One dial path (`internal/dial`), and every network step honours the context and its deadline. Never hold a lock across network I/O.
- Profiles are read-only once a Transport holds them: Transports built from equal profiles share one decoded copy (`profile.go`), so the root and everything under `internal/` only read it (no writes, no `append` into its slices); copy first to change anything.

## The uTLS fork

mimicry depends on `github.com/roachadam/utls` at a pseudo-version of its `main` branch. To move to a newer fork commit, merge it there and run `go get github.com/roachadam/utls@main`. To try an unmerged fork change, use a local `go.work` that includes `../utls`; it is gitignored and must never be committed.

## Code style

- Configuration is a struct (`Config`), not functional options or boolean parameters.
- Code that depends on the current time takes a `clockwork.Clock` (`github.com/jonboulle/clockwork`): `clockwork.NewRealClock` in production, `clockwork.NewFakeClockAt` in tests. Advance the fake clock instead of sleeping.
- Imports are grouped by goimports with `-local github.com/roachadam/mimicry` (the linter checks it).
- Each package has a short package comment (a paragraph or two: what the package is for and any invariant that isn't obvious) above `package` in its primary file, the one named after the package or its main type. No `doc.go`, except upstream's in `internal/quic`.
- Write function and method bodies on multiple lines, simple accessors and test stubs included: no `func (p *Profile) Name() string { return p.name }`.
- Write struct declarations and populated struct literals one field per line with the closing brace on its own line, single-field structs, constructor return values and structs passed as arguments included. Empty `struct{}` types and empty literals may stay compact.

```go
cfg := mimicry.Config{
    Profile:     profiles.Chrome(),
    DialTimeout: 30 * time.Second,
}
```

## Tests

- The standard `testing` package with hand-written fakes; no assertion libraries. Tests are deterministic: inject randomness and clocks rather than depending on them.
- Prefer e2e and integration tests. A behaviour browsers show belongs in an e2e replay of its capture (`internal/harness/e2e`), not in a unit test that restates the rule. Profile accuracy is proven there, never by unit tests that restate a profile's values. Each e2e test's comment says what it checks.
- Write a unit or raw-peer test only for what no capture can reach, and only when the failure would be serious: a crash, a deadlock or stall, a security bug (injection, smuggling, leaked credentials), a hostile server, or an error path (`ErrNotMeasured`, refused requests). One table test per such area, not one test per case. QUIC failures no capture reaches are rows of `TestQUICInterop`; a fix there adds a row that fails without it. Behaviour that depends on time is unit-tested with a fake clock against a raw peer (`internal/h2`: Chrome's 5 s credit return, its idle PING).
- Mutation-check: before deleting or skipping a unit test because e2e covers it, break the code and check that an e2e test fails.

## Captures and refreshing profiles

Bring a profile up to the installed browser with `go run ./cmd/refresh -browser chrome,firefox -mode quick` (in `internal/harness/`; `-dry-run` first). It captures, derives, regenerates and tests, but never touches git: review its report, update `profiles/EVIDENCE.md` and `docs/backlog.md`, then commit. See [Refreshing profiles](docs/refreshing-profiles.md).

Everything about captures (the capture tool, scenarios, per-browser setup, the generators, `tracestat`, `diff`, `clientcapture`, `interop`, `realcheck`) is in [docs/capturing.md](docs/capturing.md). Two rules matter here:

- Commit only captures from visible windows (headless Chrome says `HeadlessChrome`).
- Never commit `realcheck` output (it holds the public IP) or a Chrome net log recorded for `pageshape` (it holds real cookies; delete it).
