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

Policy (the maintainer's, 2026-10-27; decision 0027's amendment): we diverge
from upstream where it pays. A problem in the copy is fixed here now, not
left for an upstream fix; upstream's changes are reviewed periodically
(the steps below) and taken where relevant, merged around ours. Every
local change still gets its entry under "Local changes" and a `mimicry:`
marker in upstream's files.

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
   `DialConfig` (`OnFirstDatagram`: the send path calls it once the first
   datagram goes out; a `Dial` its context drops returns once the
   connection has stopped, so its caller sees whether it was called),
   `Cork`/`Uncork`, `HoldFinished`/`ReleaseFinished`,
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

6. **Interop fixes** (found by `TestQUICInterop` in `internal/harness/e2e`, each
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
   - `packet_protection.go`: `initVersion` beside `init` on `fixedKeys`
     and `updatingKeys` (which keeps its version for key updates),
     `updateSecretVersion`, `initialKeysVersion`: v2's salt and
     `quicv2 key/iv/hp/ku` labels (`versionLabels`). The upstream
     functions are the version 1 case. `headerKey.init`, `packetKey.init`
     and `updateSecret` are removed: with `initVersion` and
     `updateSecretVersion` nothing calls them (`fixedKeys.init` and
     `updatingKeys.init` stay for the tests).
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
   `loss`, `keepAlive`, `ack`, `credit`, `keyUpdate` and
   `versionNegotiation`; captures h3-blackhole, h3-broken, h3-vn,
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
     new local port once the oldest unacknowledged packet is `afterPtos`
     PTOs old (`DialConfig.NewPath`, a socket the endpoint can swap, the
     server's next connection ID, the old one retired, a PATH_CHALLENGE
     padded to the datagram size); Safari's close (`ErrPathDegraded`)
     after `afterMs` without a packet. Hooks: `conn.go`'s loop (the
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

9. **Stream buffers back to the pool** (`pipe.go`, `stream.go`,
   `crypto_stream.go`). Upstream returns a 4 KB pipe buffer to
   `pipebufPool` only in `discardBefore`, when the data before an offset is
   wholly consumed; the buffer the window ends in is kept, since the window
   may still grow into it, and nothing returns it when the stream is done.
   So every stream of less than 4 KB a direction (every request's
   HEADERS, most responses) allocated a new buffer for each direction and
   dropped it to the GC: 43% of HTTP/3's allocated bytes and 14 times
   net/http's GCs (bench 2026-10-27). Still so in x/net v0.62.0
   (`3de643d`).
   - `pipe.release` returns every buffer of a pipe, leaving its offsets.
   - `inUnlockNoQueue` releases the receive pipe once nothing can read it:
     every byte read (`in.start == insize`), or the side read-closed or
     reset (`handleData` drops what comes after), and no unread bytes left
     in `inbuf`, the lock-free readers' view of the head buffer (a stream
     whose conn closed keeps that readable).
   - `outUnlockNoQueue` releases the send pipe once no byte in it can be
     sent again: closed with every byte acknowledged, or reset; and
     `outbuf`, the lock-free writers' view of the tail's free space, empty.
     `connHasClosed` clears `outbuf` as `Reset` does, so a stream's buffer
     goes back after its conn closes too, and a later `Write` reports the
     closed conn instead of buffering where nothing is sent.
   - `cryptoStream.discardKeys` releases both pipes.
   - Race builds poison a buffer put back (`pipe_poison_race.go`;
     `pipe_poison.go` otherwise), so a use after release shows as wrong
     bytes.
   - Tests: `stream_buffers_test.go` (`TestStreamBuffersReturned`: many
     streams at once over a lossy link, ending each way, contents checked;
     a conn aborted under writers and under readers with data buffered),
     and `internal/h3`'s `TestRequestsReuseStreamBuffers` (bytes allocated
     per request on a warm connection).

10. **FIN of a truncated STREAM frame** (`stream.go`,
    `appendOutFramesLocked`). A frame reaching the end of a closed stream
    carries the FIN only if it fits whole (`appendStreamFrame` drops the
    bit from a truncated one), but upstream marked the FIN sent in that
    packet either way. Normally the rest goes next, FIN and all; a PTO
    probe sends one frame only. So a probe built while the FIN was queued
    and not yet sent (the stream closed between the loop handling the
    PTO's timer and sending the probe), resending the stream from its
    start, marked the FIN sent in a packet without it: once that packet
    was acknowledged nothing was in flight, and the FIN was never sent.
    The peer waited for the stream's end until the idle timeout. Found by
    `TestStreamBuffersReturned` under load (a request's EOF; a few runs in
    a hundred with the root suite alongside, none in 307 after the fix);
    still so in x/net v0.62.0.
    Test: `stream_fin_test.go` (`TestStreamFINAfterTruncatedProbe`, the
    close put between the PTO and the probe on the synthetic peer).

11. **Control messages parsed in place** (`udp_msg.go`: `parseControl`,
    `nextCmsg`). Upstream calls `syscall.ParseSocketControlMessage`, which
    allocates a slice per received datagram, grown twice when the socket
    is unbound (ECN and PKTINFO both on): 60% of the allocations of an
    HTTP/3 100 KB download at 64 at once (bench, 2026-10-27). The walk
    keeps its checks: nothing is taken from a malformed buffer. Test:
    `udp_msg_parse_test.go` (values, a malformed header, no allocation).

12. **Streams table under a mutex** (`conn_streams.go`:
    `streamsState.tableMu`; `conn.go`'s `receiveTransportParameters`,
    `client_params.go`, `client_tls.go`). Upstream's `newLocalStream`
    registers the stream with a round trip to the connection's loop
    (`runOnLoop`), which waits behind every datagram queued for the loop,
    so on a busy connection each HTTP/3 request start (serialized by h3's
    `startMu`) waited for it: about 19 µs at 1 KB and 64 at once (bench
    2026-10-27). Now
    `tableMu` guards `streams`, `peerInitialMaxStreamDataRemote` and
    `tableClosed`; `newLocalStream` reads the window and inserts under it
    from the caller's goroutine, and every loop-side access takes it
    (`streamForID`, `streamForFrame`, the removal in `appendStreamFrames`,
    `appendStreamFramesPTO`, `streamsCleanup`, the transport parameters,
    `clientApplyPeerWindows`, `clientResetFlowControl`). The windows are
    written before the stream limits are raised, so a stream the new
    limit lets open reads them; `clientApplyPeerWindows` and
    `clientResetFlowControl` walk the table under the same lock, which
    keeps change 6's 0-RTT race closed. `streamsCleanup` sets
    `tableClosed` under it, after which `NewStream` fails instead of
    adding a stream that would never hear the connection closed. Off the
    loop the lock is held alone, so it has no order with `sendMu` or the
    streams' gates. Stream IDs are still reserved in order
    (`localStreamLimits.open`). quic-go does the same (a mutex on its
    streams map). The round trip had one effect on the wire: a stream
    opened just after an `Uncork` was registered only after the loop had
    sent what the uncork released. `Conn.Settle` (`client.go`) keeps that
    where it shows: h3 settles after uncorking the streams it opens at
    the handshake, so Chrome's first 0-RTT packet carries the control
    stream ahead of the request (`TestHTTP3MatchesBrowser`, h3-discovery,
    fails without it). No unit test: one racing `NewStream` against the
    connection's close did not catch `tableClosed` removed (3 runs,
    `-race`), so it was not kept; the 0-RTT windows are `TestQUICInterop`'s
    `0rtt-accepted`, `0rtt-window-grows` and `goaway` rows under `-race`.

13. **Batched receive on Linux** (new `udp_batch_linux.go`,
    `udp_batch_darwin.go`; hook in `udp_msg.go`'s `Read`). Upstream reads
    one datagram per `recvmsg`; on Linux `readBatch` reads up to 8 per
    `recvmmsg` (quic-go's batch), with raw `recvmmsg` rather than
    `x/net/ipv4`'s `ReadBatch`, which allocates an address per message.
    Each datagram still goes to the loop alone, with its own control
    messages parsed (change 11), so the ACK rules see the queue they saw.
    macOS keeps `recvmsg`. At 100 KB and 64 at once req/s rose from about
    6,000 to 6,300 (bench 2026-10-27, noisy). While `recvmmsg` finds nothing (EAGAIN), the
    batch's buffers but one go back to the pool, refilled as the traffic
    needs them (change 18): an idle connection's socket held 8 (12 KB)
    while it waited (change 16). Covered by upstream's tests and the e2e
    suite run in a Linux container (`TestHTTP3*MatchesBrowser`,
    `TestQUICInterop`), and by CI, on Linux: on a Mac this path is not
    built.

14. **A client's socket failing ends its connection** (`endpoint.go`'s
    `listen`). Upstream's listen loop returns on a read error and leaves
    its connections deaf until they idle out. A `Dial` endpoint
    (`sole`) whose socket fails while it is not closing it ends the
    connection at once (`abortImmediately`: no CONNECTION_CLOSE, the path
    is gone), with an error wrapping the read's. Needed for HTTP/3
    through a SOCKS5 proxy (decision 0038): the association's socket
    fails when the proxy closes its control connection, and the engine
    must see the connection end to go on TCP. The socket can be any
    `net.PacketConn` already (upstream's `netPacketConn`, used for
    anything but a `*net.UDPConn`: no control messages, one datagram per
    read), so the association needs no other change here. Covered by
    `TestQUICInterop`'s `socks5-teardown` row, which fails without it.

15. **A gate's waiter takes it when it runs** (`gate.go`; `stream.go`'s
    `newStream`). Upstream's gate is two one-slot channels whose token a
    blocked `waitAndLock` receives directly: a goroutine woken that way
    owned the gate before it was scheduled. The conn's loop, delivering
    the next STREAM frame for a stream whose reader it had just woken,
    then blocked in `handleData`'s `ingate.lock` until that reader had run
    and unlocked, so under HTTP/3 load the whole connection ran on about
    one core. The gate
    is now a `sync.Mutex`, the condition under it, and a one-slot wake
    channel that `unlock` signals when it sets the condition: a woken
    `waitAndLock` takes the mutex once it runs and looks again, as a
    `sync.Mutex` waiter does; `lockIfSet` stays non-blocking (`TryLock`).
    A gate holds a mutex, so `newStream` locks its two gates in place
    instead of copying locked ones (`newLockedGate` is gone). At 1 KB and
    64 at once (nginx in Docker): 38,000 to 65,000 requests/s; the receive
    buffer was tuned after it (decision
    [0045](../../docs/decisions/0045-udp-receive-buffer-default.md)). The
    profile's wire rules are unchanged (datagram sizes, ACK frequency,
    credit, PINGs); only timing moves: under load the loop, no longer
    stalled, finds more requests queued for each packet. Covered by upstream's `gate_test.go` and every stream test.

16. **Idle streams hold no buffer** (`pipe.go`: `releaseIfEmpty`;
    `stream.go`: `releaseIdleIn`, `outUnlockNoQueue`, `Read`;
    `crypto_stream.go`). Change 9 returns a stream's buffers once it is
    done; `discardBefore` still keeps the buffer the window ends in while
    it is open, and HTTP/3's control and QPACK streams (up to three each
    way) and the 1-RTT crypto stream stay open for the connection's life,
    a 4 KB buffer each. Now a `Read` about to wait (nothing readable, so
    `lockIfSet` fails) discards what the lock-free path read and returns
    the receive buffer when it holds no byte (nothing out of order either);
    `outUnlockNoQueue` returns the send buffer once every byte written so
    far is acknowledged and `outbuf` is empty; the crypto stream returns
    either once consumed or acknowledged. The next frame or `Write` takes
    another from the pool. With change 13's batch, the heap an idle HTTP/3
    connection holds (Chrome against nginx, Linux, the benchmark's
    method) fell from 102.5 to 74.8 KB. Covered by `stream_buffers_test.go`
    (many streams over a lossy link, contents checked, released buffers
    poisoned under `-race`), whose readers now wait with every buffer
    returned.

17. **Tests in current Go idiom** (the package's `_test.go` files,
    `qlog/json_writer_test.go`; marked `mimicry:`). gopls' hints
    (`-severity=hint`), fixed in the tests only. The four benchmarks'
    timed loops use `b.Loop()` (setup stays before it). `b.Loop` leaves
    `b.N` at 0 until the loop ends, so the server goroutines of
    `BenchmarkThroughput` and `BenchmarkStreamCreation` no longer count
    `b.N` streams: they accept until a context canceled when the benchmark
    returns. The two `time.Sleep(1)` in `idle_test.go` (fake time, one
    step past the timeout) read `time.Sleep(time.Nanosecond)`; same
    duration. The rest is the modernize analyzer's own rewrite
    (`go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize
    -fix -test`, its non-test edits discarded), one `mimicry: modernize`
    marker per site: range over int, `WaitGroup.Go`, `bytes.SplitSeq`,
    no `test := test` copies, no embedded field type in `conn_id_test.go`'s
    literals; plus a tagged switch in `conn_test.go`. Hints left: in the
    tests, unused helpers (`wantDatagram`, `setSmoothedRTT`,
    `newPacerTest`, `sendDelay`, `testSidesAndStreamTypesSynctest`), kept
    as upstream has them; in non-test code, the same kinds the tests had
    (range over int in `packet_protection.go`, embedded field types in
    `conn_id.go`, `string +=` in a loop in `frame_debug.go`) and unused
    parameters, left as upstream's.

18. **A batch takes the buffers the traffic needs** (`udp_batch_linux.go`).
    Change 13 armed all 8 slots each time the socket woke and returned 7
    to the pool at the EAGAIN that ends the wake-up: a warm HTTP/3
    request (two wake-ups, client and server, two datagrams) took 16
    buffers from `datagramPool` for the 2 it used. Nothing allocated
    without `-race`, but `-race`'s `sync.Pool` drops a quarter of what is
    put back, so on Linux `internal/h3`'s `TestRequestsReuseStreamBuffers`
    measured 24.4 KB and 59 objects a request (18.9 KB and 53 on macOS)
    and `internal/engine/native`'s `TestWarmRequestAllocations/http3` 68
    allocations (61 on macOS); the profile put the difference on
    `datagramPool`'s `New` (7.6 MB over the test against 1.0 MB on
    macOS). Now a read leaves the buffers it did not fill at the front for
    the next read; after a full read the next may return twice as many
    (up to 8), after a short one (the socket emptied) only what is left;
    and a wake-up arms one more than the previous wake-up read, so the
    read that takes them all is short and leaves the buffer the EAGAIN
    read uses. An idle socket still
    holds one. Warm requests (Linux container, 20,000 in a row): 2.08
    pool gets a request (16.02 before), the same 4 `recvmmsg` calls (2 of
    them EAGAIN), 61 allocations and 18.8 KB with `-race` as on macOS, 52
    and 7.1 KB without (unchanged, either OS); time per request within
    noise. Bulk (`BenchmarkThroughput`'s transfer, 4 MiB): gets per
    datagram 1.19 to 1.04, `recvmmsg` calls per datagram 0.18-0.19 to
    0.18-0.20 as a burst larger than the last ramps up, throughput within
    noise (370-405 MB/s both). Covered by those two tests under `-race`
    on Linux, which fail without it.

19. **No GODEBUG packet logger** (`log.go` deleted; `conn_recv.go`,
    `conn_send.go`, one `mimicry:` marker each; `client_send.go`). Under
    `GODEBUG=quiclogpackets=1` upstream prints every packet sent and
    received, with its decrypted frames, to stdout; a SOCKS5 proxy
    password carried in a stream could land there. CodeQL flagged it
    (go/clear-text-logging). qlog covers debugging, so the logger and its
    `if logPackets` sites are gone; `frame_debug.go`'s `parseDebugFrame`
    stays, qlog uses it.

20. **A local close's cause on its streams** (`stream.go`,
    `connHasClosed`). Upstream resets a stream with the connection's
    error only when the peer closed it; closed locally, a read failed
    with `read from closed stream` and a write with `write to reset
    stream: stream error code 0`. So a request sent before the handshake
    completed (0-RTT) failed, once the handshake timed out, without its
    cause (`ErrHandshakeTimeout`, or CONNECTION_REFUSED "handshake
    timeout" without a profile). A local close with a recorded cause
    (the timeout, `abort`'s error) now resets the stream with it as the
    peer's close does; a direction the caller closed first, and a close
    without a cause (`Abort`, `Close`), report as before until the drain
    period ends. Then the connection is done and `connHasClosed` runs
    again as for a peer's close, upstream's too.
    Test: `stream_close_cause_test.go` (`TestStreamErrorAfterLocalClose`).

21. **The server's `disable_active_migration`** (`conn.go`'s
    `receiveTransportParameters`: stored for a client, its TODO removed;
    `client_path.go`: `clientPathState.migrationDisabled`,
    checked in `clientMove`). Upstream parses the parameter and ignores
    it. A client whose server sent it never moves to a new port on a
    degrading path: RFC 9000, section 18.2 forbids it, and Chrome 102
    and later does not (`MaybeMigrateToDifferentPortOnPathDegrading`,
    "Migration disabled by config"; source, not captured). The path is
    still marked degraded, so the watch does not fire again until the
    server answers; keep-alives, probes and the idle timeout are
    unchanged. Only a profile that migrates (Chrome) is affected.
    Test: `client_path_test.go` (`TestClientPathDegradingMove`).

22. **STREAMS_BLOCKED by the browser's rule** (`profile.QUIC`'s
    `streamsBlocked`; capture h3-streams-blocked). Upstream never sends
    the frame (`localStreamLimits.open`'s TODO) and panics if a sent
    packet carrying one is acknowledged or lost. New file
    `client_streams.go`; with no `Config.client`, or a profile without
    the rule, every path is upstream's.
    - `Conn.ExpectStream` counts a request from when it is made: HTTP/3
      opens its requests' streams one at a time, so at most one waits in
      `NewStream`. A request made with the streams the limit allows all
      taken queues one with the limit (each such request, or the first
      at a limit), sent in a packet of its own (`conn_send.go`'s
      `appendFrames`: only into a packet with nothing ack-eliciting yet,
      and nothing after it).
    - `stream_limits.go`: `localStreamLimits.blocked`; `open` counts the
      request as given its stream (`streamOpened`), or gone if it fails,
      and returns what its opening carries: a waiting request that takes
      the last stream a raised limit allows sends one with that limit,
      right after its first STREAM frame (Chrome), or right before it
      while another request still waits (Firefox). `conn_streams.go`'s
      `newLocalStream` hands it to the stream; `stream.go`:
      `Stream.blockedMax`/`blockedDue`, appended in
      `appendOutFramesLocked` (in the next packet, first, if the STREAM
      frame filled this one; `outUnlockNoQueue` keeps such a stream
      queued).
    - Loss (`packet_writer.go`: the frame records its limit;
      `conn_loss.go`'s `handleAckOrLoss`): a lost one is sent again, in
      a packet of its own, while the limit is still the server's and a
      request still waits, as upstream resends STREAM_DATA_BLOCKED (RFC
      9000, section 13.3). No capture lost one: not measured.
    - `conn.go`: `newConn` sets the rule for a client's bidirectional
      streams. Unidirectional streams send none (no browser did).

23. **The UDP tests' dual-stack port** (`udp_test.go`: `runUDPTest`,
    `listenDualStackForIPv4`, `udpTest.recv`; marked `mimicry:`). On macOS
    an ephemeral dual-stack bind (`ListenUDP("udp", nil)`) ignores IPv4
    sockets: it takes a port another socket, any process's, has on
    127.0.0.1 or 0.0.0.0 (with 4,000 such sockets held, 4,924 of 20,000
    binds did), and a datagram to 127.0.0.1 on that port goes to the IPv4
    socket. Every other bind avoids taken ports, and Linux checks this one
    too. So `udp4/udp/unspec` waited forever on a datagram another socket
    had read (issue #25): with three root suites running in parallel
    (load average 13-29 on 12 cores), 16 of 400 runs of the package's
    tests hung, all in that subtest (9 `TestUDPSourceUnspecified`, 2
    `TestUDPSourceSpecified`, 5 `TestUDPECN`); with every fourth port of
    the range held on 127.0.0.1, 8 of 20 runs of the `TestUDP` tests
    (each port a missing datagram was sent to was a held one). Resending
    would not help: every copy goes to the same socket. That subtest's
    socket now gets its port from an IPv4 wildcard bind held until the
    dual-stack socket is bound beside it; once bound, macOS refuses the
    port to IPv4 binds (Linux refuses the bind beside, and the ephemeral
    bind is used, as before). Under the same loads: 0 of 400, and 20 of 20
    pass. The tests wait 10 s for a datagram, then fail naming the
    address, instead of hanging until the test binary's timeout.

## Sockets

`udp_packetconn.go` is upstream's, unchanged: `netPacketConn` wraps any
`net.PacketConn`. It reads one datagram per `ReadFrom` into a buffer from
`datagramPool`, writes with `WriteTo`, and reports the socket's bound
address as its local one (none when that is unspecified); a datagram
whose sender address does not parse is dropped. It has no control
messages (no ECN, no local address per datagram) and no batched receive
(change 13). Upstream calls it mostly useful for tests.

`Dial`, and a migration's new path (`wrapPacketConn` in `client_path.go`),
use it for every socket that is not a `*net.UDPConn`, as `NewEndpoint`
does. The engine's direct connections pass a `*net.UDPConn` and take
`udp_msg.go`'s path. Two callers go through `netPacketConn` instead: the
SOCKS5 association (`proxy.Association`, change 14) and the interop
command (`internal/harness/cmd/interop`), whose `tap` wraps its UDP
socket to impair and count datagrams. The interop command's results
therefore do not cover the control messages or the batched receive.

## Tests

The tests are upstream's own, kept as the proof that the copy and the TLS
swap work: `go test -race ./internal/quic/...`. They are allowed under
AGENTS.md's tests policy for that reason, not as a model for new tests.
None were pruned: all pass on uTLS. `client_version_test.go` is mimicry's
(change 7): v2 has no upstream tests, and the browser client's handshake
cannot be checked anywhere but on the synthetic peer. So are
`stream_buffers_test.go` (change 9): a buffer released while a stream still
uses it corrupts data silently, and only many streams at once, ending every
way, reach the paths; `stream_fin_test.go` (change 10): a stall that
needs the close to fall between a PTO and its probe;
`udp_msg_parse_test.go` (change 11): the receive path's allocation bound;
`stream_close_cause_test.go` (change 20): a handshake that stops short of
confirmed; and `client_path_test.go` (change 21): no capture server can
send `disable_active_migration` (quic-go has no setting for it), so
`TestQUICInterop` cannot reach it.

## Lint

`.golangci.yml` excludes `internal/quic/` from every linter but depguard
(hundreds of findings in upstream's style: unchecked `Close` errors,
`math/rand`, `%v` on errors, unused test helpers, `init` functions; and
about 4,200 of wsl's and nlreturn's blank lines).
Formatters still run on it. mimicry's own lines here are checked by hand:
run the linter with that exclusion removed and confirm no finding is on a
line this file lists or marks `mimicry:`. Code mimicry adds later goes in
its own files where it can, so the check stays small.

How: a copy of `.golangci.yml` in the repo root without the
`^internal/quic/` rule (so `${config-path}` still resolves), never
committed, passed with `--config` to the pinned golangci-lint on
`./internal/quic/...`, with `GOOS=linux` too (`udp_batch_linux.go`); and
gopls `check` (the version `.moon/scripts/gopls-check.sh` pins) on the
package's files. mimicry's lines are the files upstream lacks and the
lines a diff against upstream's at the recorded commit adds or changes.

Last check: 2026-10-28, at `c1d31df`. 898 findings on mimicry's lines
(darwin), fixed: 889 blank lines (wsl, nlreturn), two switches made
exhaustive, two loops as `slices.Contains`, the type assertion on
`closeReply` as `errors.As`, the `unsafe` header read in `nextCmsg` given
its reason; and on Linux `nextCmsg`'s length check without a conversion
that is redundant there, and `udp_batch_linux.go`'s blank lines, its
`unsafe` reads given their reasons (the port's now without one) and its
fallback's nil error. Six `//nolint:gosec` directives were unused (G115
is off everywhere) and are gone. Now none on mimicry's lines, on either
platform; gopls reports none there either.
