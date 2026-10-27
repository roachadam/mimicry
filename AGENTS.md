# Working in mimicry

mimicry is a Go HTTP client that reproduces real browsers' network fingerprints, matched against captures of the real browsers ([README](README.md)). Accuracy first, then speed. The library module is the repo root (`github.com/roachadam/mimicry`); `harness/` is a separate module that captures the browsers and checks the profiles against them.

[docs/README.md](docs/README.md) indexes the decision records and concept docs. Read the decision before "fixing" something that looks odd.

## Commands

Go and moon are pinned in `.prototools`. golangci-lint is pinned in `.moon/tasks/go.yml` and run with `go run`, so it is built with the pinned Go: run it through moon, never a system install. `:lint` then runs gopls's analyzers (`.moon/scripts/gopls-check.sh`, gopls pinned there): anything an editor underlines fails lint.

```sh
moon run :vet :lint :test   # the gate, every project (mimicry:test or harness:test for one)
moon run :test-full         # every supported profile, not only each browser's newest
```

- `:test` runs `go test -race ./...`. moon replays a cached result only when none of its inputs changed: the Go sources and the data the tests read (`testdata` directories, `profiles/data`; for the harness also the library's sources and profiles).
- `MIMICRY_PROFILES` picks the profiles the harness's tests check (`harness/internal/testprofiles`, decision 0035): unset, each browser's newest; `all` (what `:test-full` sets), every supported one; or a comma-separated list of built-in names and profile file paths (`MIMICRY_PROFILES=safari-26-macos`, `MIMICRY_PROFILES=/abs/path/chrome-157-macos.json`).
- Run `:test-full` before a release, after a full refresh, and after a change that could differ between versions of a browser.
- The MacBook sleeps on battery with the lid closed, which freezes tests: run long suites under `caffeinate -i` on AC power, or cross-compile the test binaries (`go test -c`) and run them on the Mac mini (your ssh alias), which has no Go.

## Hard rules

- **Evidence.** Browser wire captures are the evidence ([decision 0003](docs/decisions/0003-evidence-rule.md)): capture first, then model. Browser source code and other clients' presets are leads, never evidence. A feature the samples disagree on is modelled or pinned as a known gap; never pick one sample's value.
- **Unmeasured is an error.** Asking for what the profile never measured fails with an error wrapping `ErrNotMeasured` that names what is missing, and nothing is sent (decision 0008). Never guess a value or an order.
- **Defaults are the browser.** With no options a request is what the browser sends; every deviation is one the caller asks for explicitly (`HeaderOrderKey`, `Place`, `Exact`, `DisableHTTP3`, ...; decision 0005). Wire behaviour beyond the caller's own requests is listed in [what-mimicry-does.md](docs/concepts/what-mimicry-does.md) with the option that changes it; a new one is added there.
- **Scope** ([decision 0029](docs/decisions/0029-scope-what-gets-modelled.md)): model a behaviour an anti-bot vendor plausibly checks, or one a user would notice (a stall, a failure). Everything else goes to [docs/backlog.md](docs/backlog.md).
- **Generated profile sections are never hand-edited.** The ClientHello shape and `tls.quic` (hellogen), `quic` and `http3` (h3gen), `kinds`, `context` and navigation `variants` (kindsgen) are derived from captures. Run them in that order, hellogen → h3gen → kindsgen (in `harness/`, `-profile ../profiles/data/<name>.json`), then `go generate ./profiles`; each `TestRegenerateReproducesProfiles` fails while a profile differs from what its generator derives. Details: [docs/capturing.md](docs/capturing.md#deriving-a-profile).
- **Profiles.** One complete file per browser version and platform, nothing inherited between files (decision 0004); captures may be inherited per scenario through `captures_from` (decision 0035). A profile without its captures in `harness/testdata/captures/` is not finished. Examples, docs and tests use the newest-version accessors (`profiles.Chrome()`, `Firefox()`, `Safari()`) unless the version matters to what they show (an example whose output prints the user agent keeps its versioned accessor). Adding a sixth version of a browser marks the oldest `deprecated` in the same commit.
- **Docs in the same commit.** A change that makes, changes or reverses a design decision adds or updates its record in `docs/decisions/` (copy `template.md`, next number, add it to the index in `docs/README.md`); write the reasoning while it is fresh: the options rejected and the evidence matter more than polish. A feature that lands updates its concept doc in `docs/concepts/`. A profile change that adds, removes or re-sources a claim updates its row in `profiles/EVIDENCE.md`. A gap found or pinned (a new `sampleGaps`-style entry, a known-gap row in EVIDENCE.md) is added to `docs/backlog.md`; a gap closed is removed there, with its ledger row and pin.
- **Licensing.** mimicry is MIT (`LICENSE`), both modules, `harness/` included. Code copied or ported from another project keeps its upstream licence and copyright notice next to it (a `LICENSE` file in the package, or `LICENSE-<project>` when a package draws on several; per-file headers kept), says in its package comment that it is a copy or an altered version and of what (`internal/zdeflate`: `deflate.go`; `internal/quic`: upstream's `doc.go`), and gets a row in `THIRD_PARTY_NOTICES.md` in the same commit. Only permissive licences (MIT, BSD, Apache-2.0, zlib, ISC); a new or changed Go dependency updates the dependency tables there too.
- **Clean cutover.** When something replaces something, migrate every caller and delete the old path: no shims, aliases or compatibility wrappers. (Deprecated profiles are the one deliberate exception, decision 0035.)
- **Git.** Never push and never tag; the repository stays local until its owner decides otherwise.

## Package boundaries

The public packages are the root package, `cookies`, `profile`, `profiles` and `cmd/pins`; everything else is in `internal/`, flat, one package per concern. Import boundaries are `depguard` rules in `.golangci.yml`: each says what a package's non-test files may import, and lint fails on anything else. Change a rule on purpose, never just to quiet the linter.

- Only `internal/handshake` and `internal/quic` (QUIC's TLS, decision 0027) import `github.com/roachadam/utls`.
- Only `internal/engine/native` assembles the request path (pool → dial → handshake → h1/h2, or QUIC → h3).
- `profile` imports the standard library only. Public packages never import from `harness/`.
- Use `net/http` types in the public API; do not fork `net/http` (decision 0002). Header order comes from the profile (the navigation, or the request's kind and its variant); `HeaderOrderKey` overrides it per request and `HeaderPlacement` moves single headers within it (decision 0030). Pseudo-header order comes from the profile only.
- One dial path (`internal/dial`), and every network step honours the context and its deadline. Never hold a lock across network I/O.

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
- Prefer e2e and integration tests. A behaviour browsers show belongs in an e2e replay of its capture (`harness/e2e`), not in a unit test that restates the rule. Profile accuracy is proven there, never by unit tests that restate a profile's values. Each e2e test's comment says what it checks.
- Write a unit or raw-peer test only for what no capture can reach, and only when the failure would be serious: a crash, a deadlock or stall, a security bug (injection, smuggling, leaked credentials), a hostile server, or an error path (`ErrNotMeasured`, refused requests). One table test per such area, not one test per case. QUIC failures no capture reaches are rows of `TestQUICInterop`; a fix there adds a row that fails without it. Behaviour that depends on time is unit-tested with a fake clock against a raw peer (`internal/h2`: Chrome's 5 s credit return, its idle PING).
- Mutation-check: before deleting or skipping a unit test because e2e covers it, break the code and check that an e2e test fails.

## Captures and refreshing profiles

Bring a profile up to the installed browser with `go run ./cmd/refresh -browser chrome,firefox -mode quick` (in `harness/`; `-dry-run` first). It captures, derives, regenerates and tests, but never touches git: review its report, update `profiles/EVIDENCE.md` and `docs/backlog.md`, then commit. See [Refreshing profiles](docs/concepts/refreshing-profiles.md).

Everything about captures (the capture tool, scenarios, per-browser setup, the generators, `tracestat`, `diff`, `clientcapture`, `interop`, `realcheck`) is in [docs/capturing.md](docs/capturing.md). In brief:

- `moon run harness:capture` never overwrites a capture: delete one to retake it. Commit only captures from visible windows (headless Chrome says `HeadlessChrome`). `-preflight` reports what this Mac still needs.
- Once per Mac: trust the capture CA, create `/etc/resolver/test` (Safari), install python3 with aioquic (`h3-v2`). The Mac mini also needs its user logged in at its screen and each new browser's first launch approved.
- Safari opens tabs in your own window: no content blocker or other extension enabled, default privacy settings. Capture hosts are under `.test`, never `.localhost`.
- Chrome and Firefox refuse QUIC to the capture CA unless the capture runs with `-h3-local-roots`, and Chrome ignores Alt-Svc unless the system trusts the CA. Safari's proxy scenarios need the PAC script (turn it off afterwards); its proxy-credential scenarios need a device profile and a person typing; the click scenario moves the mouse and needs Accessibility, so not over ssh. Captures and `realcheck -browsers` take over the screen.
- Never commit `realcheck` output (it holds the public IP) or a Chrome net log recorded for `pageshape` (it holds real cookies; delete it).
