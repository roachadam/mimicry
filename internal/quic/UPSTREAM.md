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
4. **Package docs.** `doc.go` gains a short "mimicry" section pointing
   here. `qlog`, `quicwire` and `testcert` are unchanged.
5. **Browser clients (`Dial`).** New files `client.go` (`Dial`,
   `DialConfig`, `Cork`/`Uncork`, `HoldFinished`/`ReleaseFinished`,
   `EarlyData`, `HandshakeComplete`, `FinishedSent` (closed once the
   datagram with the client's Finished went: a request held for the
   handshake that must not share it waits for it), `Done`/`Err`,
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
     first, and after `handshakeDone` closes a client's `FinishedSent` if
     the Finished has gone already (`noteFinishedSent`, also called after
     each datagram `maybeSendClient` sends).
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
   - PTO probes of a client: two probe packets per PTO (`loss.go`:
     `lossState.ptoProbes`, set by the hook in `conn.go`'s timer case),
     and every unacknowledged CRYPTO byte queued again (`conn_send.go`:
     the client sends it as unsent data). Upstream's one probe resends the
     CRYPTO stream's first unacknowledged range only, so the end of a
     ClientHello longer than a packet (every browser's) waited for loss
     detection, which never came while the server's ACKs were lost (row
     `handshake-ack-loss`). A profile that lays its probes out replaces
     this (change 8).
   - Keep-alive while a request is outstanding (`Conn.KeepAlive`): a
     receiving client stayed silent after the server's probe of its new
     path was lost, until the idle timeout (row `rebinding-probe-loss`).
     Its period is the profile's since change 8, at most half the
     negotiated idle timeout.
   - 0-RTT flow control (`client_tls.go`, hook in `conn.go`'s
     `receiveTransportParameters`): streams opened in 0-RTT, on the
     remembered windows, take the server's larger ones when its transport
     parameters arrive (`clientApplyPeerWindows`; row
     `0rtt-window-grows`); on rejected 0-RTT the send side starts again,
     limited by the new parameters only: connection credit, every
     stream's window and sent offset reset, everything flushed unsent
     (`clientResetFlowControl`, `Stream.resetSendWindow`; row
     `0rtt-rejected-smaller-windows`). Upstream has no 0-RTT.

   - Key updates (`conn.go`): a profile client starts none of its own
     (`QUICKeyUpdate.Initiate` false for every browser: capture
     h3-download, 50 MiB, each only follows quic-go's), where upstream
     starts one after 100 packets; aioquic cannot follow one in v2 (it
     derives v2's with `quic ku`). It still follows the server's.
     `TestHTTP3DownloadMatchesBrowser` checks it.

7. **QUIC v2** (RFC 9369) by compatible version negotiation (RFC 9368),
   for a client whose profile lists v2 in `version_information` (Firefox).
   Upstream has no v2: it speaks version 1 only and its server never
   upgrades. Every connection starts in version 1; its wire does not
   change unless the server answers in v2.
   - `packet_protection.go`: `initVersion` beside `init` on `headerKey`,
     `packetKey`, `fixedKeys` and `updatingKeys` (which keeps its
     version for key updates), `updateSecretVersion`,
     `initialKeysVersion`: v2's salt and `quicv2 key/iv/hp/ku` labels
     (`versionLabels`). The upstream functions are the version 1 case.
   - `packet.go`: `getPacketType` reads v2's rotated type bits when the
     Version field is v2; `longPacketTypeBits` encodes them
     (`packet_writer.go`).
   - `retry.go`: v2's Retry integrity key and nonce;
     `retryPacket.version`. The server still sends version 1 only.
   - `transport_params.go`: `version_information` (0x11) parsed into
     `transportParameters.versionInfo`, and encoded when set (tests'
     servers only).
   - `client_version.go` (new): `clientUpgrade` (called first in
     `conn_recv.go`'s `handleDatagram`) switches to v2 on the server's
     first v2 Initial that v2's Initial keys open, before the
     ServerHello, only when the profile lists v2;
     `clientCheckVersionInfo` (from `receiveTransportParameters`)
     closes with VERSION_NEGOTIATION_ERROR when the server's chosen
     version is not the one in use or was not offered, or it sent none
     after an upgrade or after a Version Negotiation packet, or (after
     one) Available Versions that would not have led to the same
     version. The error code is 0x53f8, the draft codepoint Firefox
     sends (capture h3-vn), not RFC 9368's 0x11 (`errors.go` names it).
     A Version Negotiation packet (hook in `conn_recv.go`'s
     `handleVersionNegotiation`) ends a profile client at once with a
     `VersionNegotiationError` carrying its versions (`errors.Is`
     `ErrVersionNegotiation`); `RetryVersion` is the version Firefox
     starts its next connection in (v2), and
     `DialConfig.AfterVersionNegotiation` marks that connection, which
     ignores further such packets. Upstream's clients keep upstream's
     handling.
   - `conn_recv.go`: the long header version check and Retry use the
     connection's version (`Conn.quicVersion`); a Retry in another
     version is ignored; the Retry's connection ID is kept for v2's
     keys. `tls.go`: Handshake and 1-RTT keys get the version's labels.
   - `client_send.go`: Initial and Handshake packets in the version in
     use; 0-RTT stays version 1 (RFC 9369, section 4.1).
     `client_tls.go`: a v2 connection's tickets carry its version
     (`rememberedVersion`); `TicketVersion` reads it and
     `DialConfig.Version` starts the resuming connection in v2: Initial
     keys, every long header, 0-RTT, and `version_information`'s chosen
     version (`client_params.go`), as Firefox does (capture h3-v2). No
     NEW_TOKEN token from a v2 connection is passed on (RFC 9369,
     section 5; Firefox's reuse unmeasured). The engine and the interop
     prober set `Version` from the ticket (`handshake.Ticket.Extra`).
   - Tests: `client_version_test.go` (upgrade, mismatched or missing
     server `version_information`, no upgrade for Chrome and Safari, RFC
     9369 appendix A vectors, a connection starting in v2, Version
     Negotiation and the connection after it), on upstream's `testConn`, which gains
     `testConn.version` (the peer's Version field and key labels), h3 as
     the synthetic server's ALPN for a profile client, and
     `cmp` in `conn_test.go`'s imports.

8. **Failure and bulk behaviour** as each browser's (`profile.QUIC`'s
   `loss`, `keep_alive`, `ack`, `credit`, `key_update` and
   `version_negotiation`; captures h3-blackhole, h3-broken, h3-vn,
   h3-download). New files `client_loss.go`, `client_path.go`,
   `client_flow.go`; with no `Config.client` every path is upstream's.
   - Probes and give-up (`client_loss.go`; `loss.go`:
     `lossState.firstPTO`, the profile's first PTO until an RTT sample,
     and `ptoAckDelay`, max_ack_delay in every PTO, both in
     `ptoBasePeriod`): a resumed connection starts from the RTT cached
     where the profile does; a probe of an unanswered first flight is
     one Initial with the CRYPTO data of the oldest packet not yet probed,
     chaos-protected again (Chrome), or the first flight's packets as
     they were (Firefox); one packet number skipped before each probe
     (Chrome; `clientPacketNumber` in `client_send.go`). The handshake
     timeout is the profile's give-up (or the idle timeout), and ends
     silently or with its close (`idle.go`'s hook: `clientGiveUp`),
     padded to its datagram size; `ErrHandshakeTimeout`;
     `Conn.WaitHandshake`. `Conn.Abandon` drops an attempt silently or
     with a 0-RTT close alone (Safari); a `Dial` whose context ends drops
     its attempt without a word.
   - Paths (`client_path.go`): keep-alive PINGs while a request waits,
     counted from the last ack-eliciting packet sent, the idle timer's
     restart or the last packet received as the profile says (`idle.go`:
     upstream's keep-alive off for a client, `appendKeepAlive` hook;
     `conn_send.go`: `packetSent` records the sends); Chrome's move to a
     new local port once the oldest unacknowledged packet is `after_ptos`
     PTOs old (`DialConfig.NewPath`, a socket the endpoint can swap, the
     server's next connection ID, the old one retired, a PATH_CHALLENGE
     padded to the datagram size); Safari's close (`ErrPathDegraded`)
     after `after_ms` without a packet. Hooks: `conn.go`'s loop (the
     timer, `clientPathAdvance`), `path.go` (the client's PATH_RESPONSE
     checked, not a protocol violation), `conn_send.go` (PATH_RESPONSE
     padded to the datagram size).
   - ACKs (`acks.go`: `ackState.every` and the warm-up, in
     `mustAckImmediately`; `conn.go`: a 64-message queue for a client,
     and only a batching one reads every queued datagram before
     answering once the handshake is confirmed).
   - Credit (`client_flow.go`; hooks in `stream.go`: a client counts
     what it consumes in the steps its STREAM frames arrived in
     (`inends`, `clientConsumed`), so a Read of any size changes nothing,
     and sets MAX_STREAM_DATA at the step that crosses the profile's
     share (`inlimit`); Safari's MAX_DATA with its MAX_STREAM_DATA;
     `conn_flow.go`: the connection's threshold, its MAX_DATA likewise set
     at the crossing, not topped up with what was read since; no consumed
     credit for Safari, whose limit is the bytes received plus the stream
     window).
   - Version negotiation (`client_version.go`): `RetryVersion` only where
     the profile retries; the downgrade error is the profile's (0x53f8
     for Firefox), RFC 9368's 0x11 otherwise.

## Tests

The tests are upstream's own, kept as the proof that the copy and the TLS
swap work: `go test -race ./internal/quic/...`. They are allowed under
AGENTS.md's tests policy for that reason, not as a model for new tests.
None were pruned: all pass on uTLS. `client_version_test.go` is mimicry's
(change 7): v2 has no upstream tests, and the browser client's handshake
cannot be checked anywhere but on the synthetic peer.

## Lint

`.golangci.yml` excludes `internal/quic/` from every linter but depguard
(about 500 findings in upstream's style: unchecked `Close` errors,
`math/rand`, `%v` on errors, unused test helpers, `init` functions).
Formatters still run on it. mimicry's own lines here are checked by hand:
run the linter with that exclusion removed and confirm no finding is on a
line this file lists or marks `mimicry:`. Code mimicry adds later goes in
its own files where it can, so the check stays small.
