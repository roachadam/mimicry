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
     `*utls.QUICConn` satisfy. Upstream's own clients (the tests) are
     `tls.UQUICClient(qconfig, tls.HelloGolang)`: crypto/tls's
     ClientHello; clients from `Dial` apply the profile's spec (change 5).
     The server stays on `tls.QUICServer` (uTLS's copy of crypto/tls's
     server; uTLS has no server-side fingerprinting), used by
     `Endpoint.Listen` and the tests.
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
5. **Browser clients (`Dial`).** New files `client.go` (`Dial`,
   `DialConfig`, `Cork`/`Uncork`, `EarlyData`, `Done`/`Err`,
   `SmoothedRTT`, `ErrIdleTimeout`, the profile's draws, close reply,
   extension frames), `client_send.go` (the client send path:
   first-flight CRYPTO plans, packet numbers, coalescing, padding, spin
   bit, ACK-only rule), `client_tls.go` (the uTLS preset, 0-RTT keys,
   sessions with the server's transport parameters, rejected early data,
   NEW_TOKEN) and `client_params.go` (transport parameters from the
   profile). Hooks in upstream's files, each marked `mimicry:`; with no
   `Config.client` (every upstream path and test) behaviour is upstream's:
   - `config.go`: `Config.client`.
   - `conn.go`: `Conn.client`, `Conn.cork`; `newConn` takes the profile's
     datagram size, first Initial packet number and ACK delay and starts
     TLS with `startClientTLS`; registers a sole endpoint's connection
     before its loop starts; `loop` calls `maybeSendClient`, skips the
     loss and ACK timers while a cork holds (they would spin the loop),
     and processes every queued datagram before sending;
     `confirmHandshake` closes the client's `Conn.Confirmed` channel.
   - `tls.go`: `handleTLSEvents` passes events to `handleClientTLSEvent`
     first.
   - `acks.go`: `ackState.maxDelay`, the full max_ack_delay the client
     sent (upstream: 25 ms less the timer granularity).
   - `conn_send.go`: a close reply (`closeReply`) carries an ACK and
     names the peer's frame type; the ACK Delay field is the profile's
     constant when it has one; ACK-only packets follow the profile's rule
     (`clientAckOnly`) instead of a PING every 20th.
   - `conn_recv.go`: 1-RTT packets parsed with the local connection ID
     length; the server's spin bit recorded; NEW_TOKEN passed on; the
     peer's CONNECTION_CLOSE answered per profile (`clientPeerClosed`);
     unknown frame types offered to `handleExtensionFrame` (DATAGRAM,
     ACK_FREQUENCY, IMMEDIATE_ACK, when the client announced them).
   - `conn_id.go`: the client's connection IDs from the profile (zero
     length: none issued); as many issued as the profile does; the peer's
     limit is the active_connection_id_limit sent (`newRandomConnIDLen`).
   - `conn_streams.go`: a peer-opened stream's receive window is the
     transport parameter for its type.
   - `endpoint.go`: `Endpoint.sole`/`soleConn`: every datagram goes to the
     one connection, whose end closes the socket.
   - `packet_writer.go`: `packetWriter.spin`.

6. **Interop fixes** (found by `TestQUICInterop` in `harness/e2e`, each
   with the row that fails without it):
   - `conn_streams.go`: `newLocalStream` reads the peer's initial stream
     window on the loop. A 0-RTT connection opens streams before the
     server's transport parameters arrive, and the loop writes the window
     then: a data race (`-race`, rows `0rtt-accepted`, `goaway`).
   - PTO probes of a client (`clientPTOExpired` in `client_send.go`):
     two probe packets per PTO (`loss.go`: `lossState.ptoProbes`, set by
     the hook in `conn.go`'s timer case), and every unacknowledged CRYPTO
     byte queued again (`conn_send.go`: the client sends it as unsent
     data). Upstream's one probe resends the CRYPTO stream's first
     unacknowledged range only, so the end of a ClientHello longer than a
     packet (every browser's) waited for loss detection, which never came
     while the server's ACKs were lost (row `handshake-ack-loss`).
   - Keep-alive while a request is outstanding (`client.go`:
     `Conn.KeepAlive`, `clientKeepAlive`; `idle.go`: the period comes
     from it, and an expired keep-alive sends nothing once the last
     request ended). Upstream's is a fixed `Config.KeepAlivePeriod`, off
     here as browsers send nothing on an idle connection; without one a
     receiving client stayed silent after the server's probe of its new
     path was lost, until the idle timeout (row `rebinding-probe-loss`).

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
