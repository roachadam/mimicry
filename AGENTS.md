# Repository working conventions

## What this is

mimicry is a Go HTTP client that reproduces real browsers' network fingerprints. Accuracy first, then speed. The library module is the repo root (`github.com/roachadam/mimicry`); `harness/` is a separate module that captures real browsers and diffs mimicry's profiles against them.

## Commands

Tool versions are pinned in `.prototools` (Go, moon). golangci-lint v2 is installed separately.

```sh
moon run :test :vet :lint   # every project
moon run mimicry:test       # the library only
moon run harness:test       # the harness only
```

## Package boundaries

The public surface is small: the root package (`Transport`, `Client`, `Config`, `Engine`), `profile` and `profiles`. Everything else is in `internal/`, flat, one package per concern. Each package's `doc.go` states what it may import; keep to it.

- Only `internal/handshake` imports `github.com/roachadam/utls`.
- Only `internal/engine/native` assembles the request path (pool → dial → handshake → h1/h2).
- `profile` imports the standard library only.
- Public packages never import from `harness/`.
- Use `net/http` types in the public API. Header order comes from the profile, with `HeaderOrderKey` / `PHeaderOrderKey` as a per-request override; do not fork `net/http`.
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

Profile accuracy is proven by the harness against real browser captures, not by unit tests that restate a profile's values.
