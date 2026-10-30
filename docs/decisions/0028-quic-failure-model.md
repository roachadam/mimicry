# 0028: QUIC and HTTP/3 fail as each browser's do

- Status: Accepted; amended 2026-10-28 by [0036](0036-fast-quic-fallback.md) (holds shortened by default); amended 2026-10-28 (`brokenAfter` counts only attempts that sent a datagram)
- Date: 2026-10-25
- Commits: `4e8c207`; amendments `1d9e67f` (0036), `f5772cb` (`brokenAfter`)

**Summary.** Each browser's QUIC and HTTP/3 failure behaviour (when it
races TCP, gives up and closes, migrates or closes a dead path, keeps
alive, and when it treats an origin as broken) is profile data derived from
the edge captures, so a server sees the browser's own reaction and requests
no longer stall behind a dead QUIC dial. An origin marked broken stays on
TCP for the Transport's life, and no key update is started by the client.
Since 0036 the long holds on unanswered or failed attempts are shortened by
default, with this record's exact timing kept as
`Config.BrowserQUICFallback`; and only attempts that put a datagram on the
network count toward `brokenAfter`.

## Problem

Until now mimicry's QUIC failed as x/net/quic does (decision
[0027](0027-quic-base.md)), and its engine waited on a QUIC dial with no rule
of a browser's. A server that answers nothing, goes silent mid-request,
closes, speaks only QUIC v2, or sends a long download is visible to the
server: when the client gives up and what it sends then, whether a request
waits or goes on TCP, whether the client moves to another port, how it pings,
acknowledges, returns credit and changes keys. The interop report's bug 2
was the cost: a request waited 10 s on a blackholed QUIC dial where every
browser went on TCP within about 300 ms. The captures `h3-blackhole`,
`h3-broken`, `h3-vn` and `h3-download`
([report](../../internal/harness/reports/http3-edge-2026-10-25.md)) show what each
browser does; a model had to choose which of it to reproduce, and how.

## Options

- **One fallback rule for all profiles** (for example Chrome's: race TCP
  after 300 ms). Simple, and it fixes the stall, but Firefox never puts a
  request behind an unvalidated QUIC connection and Safari drops its attempt
  when TCP connects: a server sees a different client.
- **Per-browser fallback as profile data** (`http3.discovery.fallback`),
  derived by h3gen from the captures and checked against every sample:
  the mode, the delays and holds, when an attempt is abandoned, when an
  origin counts as broken, and where pending requests go. More engine
  code, one rule per browser behaviour seen.
- **How long an origin stays broken**: Chrome's source backs off for 5
  minutes (doubling); no capture shows a browser trying QUIC again for an
  origin within a page. Either model the 5 minutes from source, or keep it
  broken for the Transport's life.
- **Key updates**: keep upstream's (a key update after 100 packets, then
  every 2^22), or start none, as no browser did.
- **Give-up and abandon**: x/net/quic ends an unanswered handshake silently
  at its idle timeout; the browsers do it at their own time, some with a
  close.
- **A dead path**: keep the connection until its idle timeout (x/net/quic,
  Firefox), or each browser's rule: Chrome moves to a new UDP port, Safari
  closes after 12 s.
- **Keep-alive**: the profile's period whatever the server's idle timeout,
  or capped by it.
- **Chrome's main-job delay**: Chrome's source delays the TCP job behind a
  QUIC one it expects to work by 1.5 × the origin's smoothed RTT. Model it,
  or leave it out.

## Decision

Each browser's failure behaviour is profile data, derived by h3gen
(`quic.*`) and hellogen (`tls.quic.versionNegotiation`) from the edge
captures, as decision [0024](0024-clienthello-shape-in-profiles.md) made the
ClientHello's shape profile data.

- **Fallback per browser.** Chrome races (TCP 300 ms after a request starts
  waiting, at once beside a fresh attempt after a failed 0-RTT one) and holds
  every request behind a 0-RTT attempt until it gives up; Firefox validates
  in the background and holds only requests made while its resumed
  re-validation after a server's close is pending; Safari races with an
  88–129 ms head start (`tcpDelayMs`, by version) and drops the attempt
  when the request goes on TCP.
  Requests pending on a dead connection go where the browser sent them
  (`pending`: TCP for Chrome and Firefox, a new QUIC connection for Safari).
- **Broken for the Transport's life.** After `brokenAfter` failed attempts
  in a row (Chrome and Firefox 1, Safari 2 with resumed ones counting; since
  the 2026-10-28 amendment below, only attempts that sent a datagram count)
  the origin stays on TCP until the Transport is closed. The 5 minutes are
  source, not measured; a Transport is one browsing entity (decision
  [0019](0019-transport-is-one-browsing-entity.md)), and a page-length
  capture is all the evidence there is.
- **No client-initiated key updates.** A profile client follows the
  server's key updates and starts none (`quic.keyUpdate.initiate` false
  for every browser).
- **Give-up and abandon closes per profile** (`quic.loss.fresh`,
  `.resumed`, `.abandonClose`): Chrome at 4.0 s with an Initial
  CONNECTION_CLOSE (NO_ERROR, quiche's idle-timeout reason) padded to 1250
  bytes; Firefox silent at its idle timeout for a fresh attempt and at 5.0 s
  with an Initial application close (0x0c) in 255 bytes for a resumed one;
  Safari's dropped attempt sends a 0-RTT close only if it sent 0-RTT. A
  `Dial` whose context ends drops its attempt without a word.
- **Chrome's port migration.** Once the oldest unacknowledged packet is
  `quic.loss.pathDegrading.afterPtos` (4) of its PTOs old, the connection
  moves to a new UDP socket with the server's next connection ID (the old one
  retired, a PATH_CHALLENGE padded to 1250 bytes). The socket comes from
  `quic.DialConfig.NewPath`, which the engine fills from its dialer, and a
  profile that migrates cannot be dialled without it. Safari closes the
  connection 12 s after the last packet received ("keepalive limit
  reached"); Firefox keeps it until its idle timeout.
- **Keep-alive capped at half the idle timeout.** The PING period is the
  profile's (`quic.keepAlive`: Chrome and Firefox 15 s, Safari 2 s), or
  half the negotiated idle timeout if that is shorter.
- **Chrome's 1.5 × SRTT main-job delay is not modelled.** The race delay
  (`tcpDelayMs`) and the 0-RTT hold describe every capture.

## Evidence

- `internal/harness/reports/http3-edge-2026-10-25.md`, §1–3 and its corrections:
  Chrome's TCP after 305–306 ms and its 4.0 s hold behind a 0-RTT attempt;
  Firefox's requests never waiting but in the re-validation (held 3.0–7.2 s);
  Safari's TCP 89–129 ms after QUIC; no browser trying QUIC again for a broken
  origin in the rest of the page (25–45 s); the give-up closes; Chrome's
  migration 115–132 ms after the request, 2 probes first; Safari's close at
  +12.0 s; no key update started by any browser in 50 MiB (37,000 received
  packets, up to 8,600 sent).
- `profiles/EVIDENCE.md` rows for every field, and h3gen's checks of each
  against every connection (`sampleGaps` lists the outliers).
- `TestHTTP3EdgeMatchesBrowser`: the pages through mimicry against every
  sample, the migration in each connection's own PTOs; the four fallback
  mutations (no race delay, no 0-RTT hold, Firefox holding behind
  validation, no Version Negotiation retry) each fail it, and so do no
  migration, no Safari path close, no packet-number skip before probes, and
  Firefox's VN retry with its first hello.
  `TestHTTP3DownloadMatchesBrowser`: upstream's key update after 100
  packets and Chrome acknowledging every 2nd packet each fail it.
- `TestQUICInterop`: the blackhole and rebinding rows with Chrome's
  migration; the impaired-handshake rows hold Safari to its race (an
  attempt that needs a probe loses to TCP, and the origin is broken after
  two that sent a datagram; see the amendment below).
- Keep-alive cap: the browsers' periods were measured against the capture
  server's 60 s idle timeout, where the cap never applies; the interop rows
  with a 1 s and a 4 s server idle timeout need it, and Firefox's neqo keeps
  alive at half the negotiated idle timeout (or one PTO if longer:
  `keep_alive_timeout` in `neqo-transport/src/connection/idle.rs`; its 15 s
  is half of its 30 s), which no capture tested against a shorter one.
- Chrome's main-job delay: on loopback the smoothed RTT is about 1 ms, so
  1.5 × SRTT cannot be told from no delay in any capture.

## Consequences

- A server sees each browser's give-up, close, race and migration;
  requests no longer stall behind a dead QUIC dial (interop bug 2).
- A broken origin stays on TCP for as long as the Transport lives: a
  long-running client never tries HTTP/3 there again, where Chrome would
  after 5 minutes. Closing the Transport resets it.
- With no key update of its own, a connection's keys change only when the
  server's do. AES-GCM's confidentiality limit is 2^23 packets per key (RFC
  9001, section 6.6), about 10 GB of 1250-byte packets sent by the client:
  past that, on a server that never updates, the connection exceeds it.
  Whether a browser starts an update nearer the limit is not measured.
- `DialConfig.NewPath` is part of dialling a migrating profile: any caller
  of `internal/quic` (the engine, the interop prober) supplies a socket
  factory to the same peer.
- With a server idle timeout under 30 s, PINGs come at half of it, which
  differs from what a browser would send there, if it sends anything.
- Chrome's close of a migrated connection whose new path also dies
  ("85:Network blackhole detected") and Safari's QUIC resumption are not
  modelled; `h3EdgeGaps` pins both.

## Revisit when

- A capture shows a browser trying QUIC again for an origin it gave up on,
  or a Transport's life spans hours in use: then the back-off is data.
- A capture of a long upload, or of a server that never updates keys, shows
  a browser starting a key update.
- A capture over a real network, where 1.5 × SRTT is tens of milliseconds,
  shows Chrome's TCP job waiting that long.
- A browser is captured against a server with a short idle timeout.
- Chrome's blackhole close or Safari's resumption is captured often enough
  to model.

## Amendment 2026-10-28: fast fallback by default

By [0036](0036-fast-quic-fallback.md): the timing above
is now `Config.BrowserQUICFallback`. By default a request stops waiting on
an unanswered 0-RTT connection at the profile's race delay (Chrome
305–306 ms; Firefox, without one, its 300 ms first probe timeout) instead
of the give-up (`zeroRttHolds` 4 s, `hold` 5 s), and on a failed attempt at
once instead of at the race delay. The QUIC connections, the race delays
and everything a server that answers sees are unchanged;
`TestHTTP3EdgeMatchesBrowser` checks both modes.

## Amendment 2026-10-28: an attempt counts once it has sent

The maintainer's decision: an attempt counts toward
`brokenAfter` only once its first datagram has gone out
(`quic.DialConfig.OnFirstDatagram`). Under load (the harness's `-race`
suites, a busy host) a Safari attempt could lose its 88–129 ms race while
the process was still building its hello; dropped then, it had put nothing
on the network, yet counted as a failure, and two such marked the origin
broken for the Transport's life. A local CPU stall is not a network
failure, and no capture can show a browser's attempt that sent nothing, so
nothing measured changes. Such an attempt leaves no trace: the next request
starts another, and a request still goes on TCP at the race delay as
before, so an origin whose attempts never get a datagram out is never
marked broken but costs each request the race delay. An attempt that sent
and then failed counts exactly as before.
`TestQUICAttemptCountsOnlyOnceItSent` (internal/engine/native) checks both;
`TestQUICInterop`'s impaired-handshake rows hold the sessions that reach the
relay to `brokenAfter`.
