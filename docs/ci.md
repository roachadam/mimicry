# CI

For contributors; users don't need this page.

How the checks on pull requests, main and releases are run, and how to reproduce them locally.

## Workflows

`.github/workflows/ci.yml` runs on every pull request, every push to main, nightly and by hand (`workflow_dispatch`). A pull request runs the jobs its changes can affect ([What a pull request runs](#what-a-pull-request-runs)); every other run runs them all.

| Job | What it runs |
|---|---|
| `lint` | `moon run :vet :lint`, then the harness built, vetted and golangci-linted for Windows |
| `test-root` | The library's race tests; bench builds and vets |
| `vuln` | govulncheck (pinned in the job), in each of the three modules |
| `zizmor` | Audits the workflows and the setup action [1], then checks the planner's cases (`bash .github/scripts/ci-plan-test.sh`) |
| `test-harness (1)` to `(N)` | The harness's tests, in shards ([Harness tiers and shards](#harness-tiers-and-shards)) |
| `test-node` | The Node SDK (`.github/workflows/node.yml`) [2] |
| `test-python` | The Python SDK (`.github/workflows/python.yml`) [3] |
| `ci-ok` | Passes when every job `changes` planned passed and the others were skipped. It is the check to require. |

1. Fails on a finding of medium severity or more. Run it locally: `GH_TOKEN=$(gh auth token) uvx zizmor@<version in ci.yml> --min-severity medium .github/workflows .github/actions`.
2. Its tests on Linux with the oldest and newest supported Node LTS lines (22, 26), and on macOS and Windows with the newest. Also the harness's `TestNodeMatchesGo*` and its npm packages.
3. Its tests on Linux with the oldest and newest supported Python (3.11 with the oldest allowed protobuf, 3.15), and on macOS and Windows with the newest. Also ruff and mypy, `TestPythonSDKMatchesGo*` in the harness, and its wheels built.

- **Why only the newest on macOS and Windows:** the version floor is a language and API question, the same on every OS; the platform code (the core binary, its pipes) does not vary by Python or Node version. Fewer macOS jobs also means fewer runs lost to GitHub's macOS arm64 runner capacity.
- **macOS off pull requests:** the macOS SDK jobs run on main, nightly, by hand and in release gates, not on pull requests. GitHub's macOS arm64 runners often never pick a job up ("not acquired by Runner"), which failed `ci-ok` with no test run; the SDK's macOS path is the POSIX one Linux tests.
- **Releases:** pushing a `v*` tag runs `.github/workflows/release.yml` ([Releasing](releasing.md)).
  - It runs `:test-full`, builds and attests the SDKs' wheels and npm packages, and opens a draft GitHub Release.
  - It waits for the maintainer to approve publishing them.
  - The release jobs never use a cache, and the publish jobs check out no code. Keep it so.

## What a pull request runs

The `changes` job (`.github/scripts/ci-plan.sh`) sorts the pull request's changed paths by owner and runs what each owner can affect. Its log lists every path's owner.

| Changed paths | Owner | Jobs |
|---|---|---|
| `*.md`, `docs/`, `internal/bench/reports/` | docs | None |
| Go files and directories at the root, `cmd/`, `cookies/`, `internal/` (not the harness or the bench), `profile/`, `profiles/`, `proto/`, `go.mod`, `go.sum` | library | `lint`, `test-root`, `vuln`, the harness's tiers, `test-node`, `test-python` |
| `internal/harness/` | harness | `lint`, `vuln`, the harness's tiers, and the SDK jobs that run the harness: `node (harness, byte-identical)` and `python (ubuntu-latest, 3.15)` |
| `internal/bench/` | bench | `lint`, `test-root` (it builds and vets the bench), `vuln` |
| `bindings/node/` | node | `test-node` |
| `bindings/python/` | python | `test-python` |
| Anything else (`.github/`, `.moon/`, `.prototools`, `.golangci.yml`, `bindings/build-core.sh`, a new path) | other | Every job, `zizmor` included |

- **Why it is safe:** each module is tested on its own. The library never imports the harness or the bench, and `go test ./...` at the root skips them. The SDKs run the core built from the library. The harness's tests of the SDKs (`TestNodeMatchesGo*`, `TestPythonSDKMatchesGo*`) run when either side changes.
- **A path no rule names runs every job.** Adding a module, a top-level directory or a file one owner's tests read from another's means adding its rule to `ci-plan.sh` and a case to `ci-plan-test.sh`.
- **Every job, every pull request:** set the repository variable `CI_SELECTION` to `full` (Settings → Secrets and variables → Actions → Variables). The docs rule still applies.
- Main, nightly, by hand and the release run every job and tier, whatever changed.

## Harness tiers and shards

The harness's tests come in tiers, set per test in `.github/harness-shards.txt`. `.github/scripts/ci-plan.sh` has the paths each tier watches; a pull request runs the harness's tiers when it changes the library or the harness.

| Tier | Tests | Runs on |
|---|---|---|
| `core` | Headers, hellos, cookies, redirects, kinds, proxies, WebSockets, resumption, HPACK/QPACK, profiles and the harness's unit tests [1] | Every pull request |
| `protocol` | `TestConnectionMatchesBrowserTrace`, `TestChainedPrioritiesMatchBrowserTraces`, the HTTP/3 download and match tests | Pull requests touching protocol code [2] |
| `gen` | The hellogen, h3gen and kindsgen tests | Pull requests touching the generators, captures, capture parsing or profiles |
| `main` | `TestHTTP3EdgeMatchesBrowser`, `TestQUICInterop` | Main, nightly and by hand only [3] |

1. A test not listed is core.
2. The HTTP/2, HTTP/3, QUIC or engine code, the profiles, the e2e tests, the scenario server or the trace decoders, or the Go and module versions.
3. So their regressions show after the merge.

### Shards

- **Counts:**
  - Main, nightly, by hand and the release run every tier in 8 shards.
  - A pull request runs 2 shards for core, plus 3 for protocol and 1 for gen.
- **Time limit:** each job must finish in 15 minutes, so the tests are split into shards.
- **How tests are dealt:** `.github/scripts/harness-shard.sh <tiers> <shard> <total>` does it.
  - Every top-level test is a unit, weighed by its seconds on the runner in `.github/harness-shards.txt`.
  - An unlisted test weighs 2.
  - A `Test/regex` line splits a test's subtests.
  - Units are dealt heaviest first to the least-loaded shard.
- **How shards run:** `go test -race -v`, so their logs carry each test's runner duration.
  - Without `-race` where a test's line says `norace`.
  - With `-parallel N` where it says `parallel=N`.
- **Adding a slow test**, or slowing one down, means adding or updating its line in `.github/harness-shards.txt`, with its tier. Then check the splits:
  - `bash .github/scripts/harness-shard.sh all plan 8`: no shard above about 9 minutes.
  - For a pull request's tiers, e.g. `bash .github/scripts/harness-shard.sh core,protocol plan 5`: about 250 s of tests a shard.

## Linting other platforms

- Host lint never loads another OS's build-tagged files (`*_linux.go`, `*_windows.go`).
- CI lints on Linux, and the harness also for Windows.
- Before pushing platform-specific code, in each module the code is in:
  - Build the pinned golangci-lint for the host: `GOBIN=/tmp/gclbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<version in .moon/tasks/go.yml>`.
  - Run `GOOS=linux` and `GOOS=windows` `golangci-lint run ./...`, with `go build ./...` and `go vet ./...`.

## Vulnerabilities and dependencies

- **`vuln`** fails only on a vulnerability the code reaches (a call path to it).
  - Ones it lists as imported or required but not called pass.
  - Run it locally in a module: `go run golang.org/x/vuln/cmd/govulncheck@<version in ci.yml> ./...` (`GOWORK=off` in `internal/bench`).
  - Fix a reachable one by moving the dependency to the fixed version. Never suppress it.
  - In the bench, a competitor's dependency moves only as an indirect bump; the competitor's own version stays.
- **CodeQL** (`.github/workflows/codeql.yml`) analyses the Go modules and the workflows.
  - Go: a traced `go build ./...` in each module, Linux only.
  - Runs on every pull request, every push to main and weekly.
  - Its alerts are in the Security tab. It is not part of `ci-ok`.
- **Dependabot** (`.github/dependabot.yml`) bumps only the GitHub Actions, weekly, in one pull request.
  - Go modules move only through its security updates or by hand (see [SECURITY.md](../SECURITY.md)).
