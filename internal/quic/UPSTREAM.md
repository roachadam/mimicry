# Upstream

`internal/quic` is a copy of the Go team's QUIC implementation, taken per
[decision 0027](../../docs/decisions/0027-quic-base.md).

- Module: `golang.org/x/net` v0.61.0
- Commit: `c1d31df183e51b1075a2fd670d6f6f4efb871773` (`c1d31df`)
- Copied: 2026-10-25
- License: x/net's BSD licence and patent grant, kept as `LICENSE` and
  `PATENTS` here.

| Upstream path | Here |
|---|---|
| `quic/` | `internal/quic/` |
| `quic/qlog/` | `internal/quic/qlog/` |
| `internal/quic/quicwire/` | `internal/quic/quicwire/` |
| `internal/testcert/` | `internal/quic/testcert/` (tests only) |

Every `.go` file was copied, tests included. Not copied: upstream's interop
runner (`internal/quic/cmd/interop`), which the package does not import.

## Syncing

1. Diff upstream between the recorded commit and the newer one:
   `git diff c1d31df <new> -- quic internal/quic/quicwire internal/testcert`
   in a clone of `https://go.googlesource.com/net`. Also check whether the
   package imports any new x/net internal package.
2. Apply the relevant hunks to the paths above. New code gets the same
   changes as the old (import paths, `crypto/tls`; see below). A newly
   imported x/net internal package is copied as a subpackage here.
3. `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 fmt ./internal/quic/...`
   (the version pinned in `.moon/tasks/go.yml`), then
   `go test -race ./internal/quic/...`.
4. Update the commit, date and local changes in this file.

## Local changes

Changes in upstream's files are marked `mimicry:` in the code, apart from
the mechanical import rewrites.

1. **Import paths.** `golang.org/x/net/quic/qlog`,
   `golang.org/x/net/internal/quic/quicwire` and
   `golang.org/x/net/internal/testcert` point at the subpackages here
   (15 files).
2. **TLS: uTLS instead of `crypto/tls`.** Every `"crypto/tls"` import is
   `tls "github.com/roachadam/utls"` (21 files). The fork mirrors
   crypto/tls's QUIC API, so the alias leaves every `tls.` reference as
   upstream wrote it. As a result `Config.TLSConfig` is a `*utls.Config`.
   - `tls.go`: a `tlsConn` interface (the six methods `Conn` calls:
     `Start`, `NextEvent`, `HandleData`, `SetTransportParameters`,
     `ConnectionState`, `Close`) that both `*utls.UQUICConn` and
     `*utls.QUICConn` satisfy. The client is
     `tls.UQUICClient(qconfig, tls.HelloGolang)`: crypto/tls's ClientHello
     for now; browser presets come with the profiles. The server stays on
     `tls.QUICServer` (uTLS's copy of crypto/tls's server; uTLS has no
     server-side fingerprinting), used by `Endpoint.Listen` and the tests.
   - `conn.go`: `Conn.tls` is a `tlsConn` instead of `*tls.QUICConn`.
   - The tests' TLS peers (`conn_test.go`, `endpoint_test.go`,
     `retry_test.go`) keep `tls.QUICClient`/`tls.QUICServer`, now uTLS's.
3. **Formatting.** goimports with mimicry's local prefix regrouped import
   blocks, and gofmt `-s` shortened one literal (`qlog_test.go`:
   `[]byte{1, 2, 3, 4}` to `{1, 2, 3, 4}`).
4. **Package docs.** `doc.go` gains a "mimicry" section stating what the
   package may import (AGENTS.md's package convention). `qlog`, `quicwire`
   and `testcert` each gain a `doc.go` with the same statement; their
   package comments stay upstream's.

## Tests

The tests are upstream's own, kept as the proof that the copy and the TLS
swap work: `go test -race ./internal/quic/...`. They are allowed under
AGENTS.md's tests policy for that reason, not as a model for new tests.
None were pruned: all pass on uTLS.

## Lint

`.golangci.yml` excludes `internal/quic/` from the linters (about 500
findings in upstream's style: unchecked `Close` errors, `math/rand`, `%v`
on errors, unused test helpers, `init` functions). Formatters still run on
it. mimicry's own lines here are checked by hand: run the linter with that
exclusion removed and confirm no finding is on a line this file lists or
marks `mimicry:`. Code mimicry adds later goes in its own files where it
can, so the check stays small.
