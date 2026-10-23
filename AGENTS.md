# Repository working conventions

## What this is

mimicry is a Go HTTP client that reproduces real browsers' network fingerprints. Accuracy first, then speed. The library module is the repo root (`github.com/roachadam/mimicry`); `harness/` is a separate module that captures real browsers and diffs mimicry's profiles against them.

## Commands

Tool versions are pinned in `.prototools` (Go, moon). golangci-lint is pinned in `.moon/tasks/go.yml` and run with `go run`, so it is built with the pinned Go; run it through moon rather than a system install.

```sh
moon run :test :vet :lint   # every project
moon run mimicry:test       # the library only
moon run harness:test       # the harness only
```

## Package boundaries

The public surface is small: the root package (`Transport`, `NewClient`, `Config`, `Engine`, `HeaderOrderKey`), `profile` and `profiles`. Everything else is in `internal/`, flat, one package per concern. Each package's `doc.go` states what it may import; keep to it.

- Only `internal/handshake` imports `github.com/roachadam/utls`.
- Only `internal/engine/native` assembles the request path (pool → dial → handshake → h1/h2).
- `profile` imports the standard library only.
- Public packages never import from `harness/`.
- Use `net/http` types in the public API. Header order comes from the profile, with `HeaderOrderKey` as a per-request override; pseudo-header order comes from the profile only. Do not fork `net/http`.
- One dial path (`internal/dial`), and every network step honours the context and its deadline.
- Never hold a lock across network I/O.
- Configuration is a struct (`Config`), not functional options or boolean parameters.

## The uTLS fork

mimicry depends on `github.com/roachadam/utls` at a pseudo-version of its `main` branch. To move to a newer fork commit, merge it there and run `go get github.com/roachadam/utls@main`. To try an unmerged fork change, use a local `go.work` that includes `../utls`; it is gitignored and must never be committed.

## Go clocks in tests

Use `github.com/jonboulle/clockwork` for code that depends on the current time. Inject a `clockwork.Clock`, use `clockwork.NewRealClock` in production and `clockwork.NewFakeClockAt` in tests. Advance the fake clock instead of sleeping.

## Go formatting

Write Go function and method bodies on multiple lines, including simple accessors and test stubs. Do not use one-line declarations such as `func (p *Profile) Name() string { return p.name }`.

Write struct declarations and populated struct literals on multiple lines, with each field on its own line and the closing brace on a separate line. This includes single-field structs, constructor return values, and structs passed as function arguments. Empty `struct{}` types and empty literals may stay compact.

```go
type Plan struct {
    hello *utls.ClientHelloSpec
}

cfg := mimicry.Config{
    Profile: profiles.Chrome155Windows,
    Timeout: 30 * time.Second,
}
```

## Tests

Use the standard `testing` package with hand-written fakes; no assertion libraries. Tests must be deterministic: inject randomness and clocks rather than depending on them.

Profile accuracy is proven by the harness against real browser captures, not by unit tests that restate a profile's values: `harness/e2e` sends every built-in profile through mimicry to the capture server and requires no differences from its browser's capture.

## Built-in profiles

Built-in profiles are JSON files in `profiles/data/`, one per browser version and platform; the typed Go variables are generated from them. Every built-in profile must pass `profile.Parse`, enforced twice: the generator refuses to generate from an invalid file, and a test in `profiles` parses every embedded file. A profile without a matching capture in `harness/testdata/captures/` is not finished.

## Capturing browsers

`moon run harness:capture` launches every installed browser (Chrome, Firefox, Safari) against an in-process capture server and writes new captures to `harness/testdata/captures/`. It never overwrites; delete a capture to retake it. Use visible windows for captures you commit: headless Chrome reports `HeadlessChrome` in its user agent. moon skips this task when `CI=true`.

Each browser is sampled three times (`-samples`), each on a fresh server and port so no connection or TLS session carries over. If the samples differ only in extension order, the capture records `"extension_order": "shuffled"` (Chrome), otherwise `"fixed"`. Any other difference between samples fails the capture: it is randomness a profile cannot describe and needs investigating.

`go run ./cmd/diff WANT.json GOT.json` (in `harness/`) lists every difference between two captures or profiles, comparing extension order as WANT's `extension_order` says, and exits 1 if there are any.

- Chrome and Firefox run with throwaway profiles and resolve `capture.mimicry.test` to 127.0.0.1 themselves; Chrome ignores certificate errors and Firefox gets a certificate exception. Nothing on the system changes.
- Safari cannot be isolated: it opens a tab in the user's own window on `https://localhost:<port>/` (closed afterwards) and must trust the capture CA. The CA lives in `<user config dir>/mimicry/capture-ca/`, outside the repo, and is name-constrained to `capture.mimicry.test` and `localhost` with IP addresses excluded, so it cannot vouch for any real site. Trust it once; the capture command prints this when needed:

  ```sh
  security add-trusted-cert -r trustRoot -p ssl -k ~/Library/Keychains/login.keychain-db \
    ~/Library/Application\ Support/mimicry/capture-ca/ca.pem
  ```

  To undo: `security remove-trusted-cert` with the same file, then delete the directory.
