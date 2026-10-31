# 0040: Idle HTTP/2 and HTTP/3 connections close; unused proxies and dropped Transports let theirs go

- Status: Accepted; amended 2026-10-28 (`Config.IdleConnTimeout`, `pool.Shared.OnClose` instead of a watcher goroutine); amended 2026-10-29 (`Config.MaxIdleProxies`)
- Date: 2026-10-28
- Commits: `145b90b`; amendments `8b681d0` (IdleConnTimeout), `e28df4c` (MaxIdleProxies)

**Summary.** Idle HTTP/2 and HTTP/3 connections now close after 90 s like
HTTP/1.1, proxies outside the 16 most recently used (and unused for 10 s)
have their idle connections closed, and a Transport dropped without
`CloseIdleConnections` closes its idle connections once garbage
collected. The maintainer's decision was that idle connections get
cleaned up, since rotating proxies or clients otherwise kept connections
and goroutines alive. Amendments added `Config.IdleConnTimeout` (negative
turns the idle timeout off) and `Config.MaxIdleProxies` (keep only the N
most recent proxies' connections, no time grace), so a library user can
tune both.

## Problem

The pool ([0020](0020-connection-pool-package.md)) closed idle HTTP/1.1
connections after 90 s and nothing else: an HTTP/2 connection stayed open
until the server closed it, a Chrome idle PING went unanswered, or the
caller called `CloseIdleConnections`; an HTTP/3 connection until QUIC's
idle timeout. Each proxy URL has connections of its own
([0014](0014-proxies.md)), so:

- **Rotating proxies within a Transport** (changing what `Config.Proxy`
  returns, the documented way) left every old identity's connections
  open: HTTP/1.1 ones for 90 s, HTTP/2 ones for as long as the server
  allowed. In the benchmark's many-clients layer
  ([multiclient-2026-10-28](../../internal/bench/reports/multiclient-2026-10-28.md),
  a new identity every 10 requests) mimicry held 313 KB of live heap and
  46 goroutines per client after 4 s over HTTP/2 through CONNECT, against
  35 KB and 2 without rotation, and growing.
- **Rotating clients** (dropping a client and building another with the
  next identity) without `CloseIdleConnections` left the dropped
  Transport's connections and their goroutines alive for the same time:
  for HTTP/2, without bound.

The maintainer's decision: idle connections are cleaned up, and the
realistic pattern (a new client per identity) is benchmarked.

## Options

**An idle timeout for HTTP/2 and HTTP/3.**

- Each browser's value from its source code. Source is a lead, not
  evidence ([0003](0003-evidence-rule.md)), and a value per browser taken
  from it is still unmeasured; no capture distinguishes it.
- **The HTTP/1.1 value, 90 s, for every protocol** (chosen).

**Proxies a Transport no longer uses.**

- Close every other proxy's connections when the proxy changes
  (httpcloak's `SetProxy` closes the old transports, azuretls's closes
  idle connections): round-robin over a proxy list then dials on every
  request.
- A cap on idle connections per Transport, least recently used out
  (net/http's `MaxIdleConns`): round-robin over more proxies than the cap
  dials on every request, however fast it comes round.
- Close a proxy's connections once it has been unused for a while:
  round-robin over few proxies at a slow pace (a proxy every 15 s over
  4) closes connections it would use again.
- **Both, so that either protects a proxy** (chosen): a proxy keeps its
  idle connections while it is one of the 16 most recently used, or was
  used in the last 10 s.

**Dropped Transports.**

- Rely on the idle timeout alone: bounded, 90 s.
- **Close the idle connections when the garbage collector finds the
  Transport unreachable** (`runtime.AddCleanup`), and keep the timeout as
  the bound for whatever is in flight then (chosen). A finalizer on an
  object its connections' goroutines reach never runs (azuretls sets one
  on its `Session`, which its transport's dial function refers back to:
  [INFERENCE] it never runs), so this needs the Transport to be
  unreachable while the connections are not.

## Decision

- **Idle timeout, 90 s, all protocols.** An HTTP/2 or HTTP/3 connection
  closes 90 s after its last request ended (`h2.Conn.IdleSince`,
  `h3.Conn.IdleSince`), never while one is in flight; the pool looks at
  each shared connection once per idle timeout
  (`pool.Config.IdleTimeout`, one timer per key, re-armed for the time
  left). HTTP/1.1 keeps its 90 s since release.
  - HTTP/2 closes as the profile's browser lets one go: GOAWAY
    (NO_ERROR), then the close, for a profile whose browser was captured
    sending one when it drops a connection (`http3.discovery.goawayTcp`:
    Firefox); otherwise the close alone (Chrome, Safari), as
    `CloseIdleConnections` does.
  - HTTP/3 ends silently (no CONNECTION_CLOSE), as every browser's idle
    connection ends at its QUIC idle timeout. Nothing keeps an idle
    HTTP/3 connection alive: keep-alive PINGs go only while a request
    waits (`quic.Conn.KeepAlive`). So Chrome's and Firefox's connections
    end at their 30 s `max_idle_timeout` (or the server's, if shorter)
    before the pool's 90 s, as before; the 90 s applies to Safari (300 s
    in Safari 27, none in Safari 26) against a server whose idle timeout
    is longer than 90 s or absent.
  - `CloseIdleConnections` is unchanged (HTTP/2: the close;
    HTTP/3: CONNECTION_CLOSE NO_ERROR).
- **Unused proxies.** Each request through a proxy records its use
  (`internal/engine/native/proxies.go`). Once more than 16 proxies are
  known, a sweep closes the idle connections (HTTP/1.1, HTTP/2, HTTP/3, as
  the idle timeout would) of every proxy that is neither among the 16 most
  recently used nor used in the last 10 s, and forgets it once none of its
  connections is in use; it runs again when the next proxy can go stale,
  so a proxy's connections close 10–20 s after its last use at most.

  | Use | What happens |
  |---|---|
  | Rotation, a new identity per URL | old identities beyond the 16 most recent close 10–20 s after their last request |
  | Round-robin over up to 16 proxies, any pace | nothing closes early (the 90 s timeout still applies) |
  | Round-robin over more, each again within 10 s | nothing closes early |
  | Round-robin over more, slower than that | a connection per request beyond the 16 most recent, as with a new proxy |

  16 covers a list of proxies used in turn ("several" in practice) with a
  bounded cost when rotating (16 identities' connections); 10 s keeps a
  large list served quickly (each proxy again within 10 s) warm and lets
  a rotating client's old identities go within 20 s rather than 90.
- **Dropped Transports.** `NewTransport` registers a cleanup that closes
  the engine's idle connections, on a goroutine of its own, once the
  Transport is unreachable. Nothing the engine holds refers to the
  Transport (the pin check now holds the pins, not the Transport), while
  the connections' goroutines refer to the engine: so a client dropped
  without `CloseIdleConnections` closes its idle connections at the next
  collection, and those still in flight at the idle timeout after they
  finish. The Transport's documentation says to call
  `CloseIdleConnections` when discarding a client.

## Evidence

- **No capture shows a browser closing an idle HTTP/2 connection.** The
  longest idle gap captured is 35 s: Safari reused its `h3-b` HTTP/2
  connection 35 s later
  ([http3-2026-10-25](../../internal/harness/reports/http3-2026-10-25.md)),
  and the `slow-idle` scenario's 11 s gap shows only Chrome's PING before
  the next request (EVIDENCE.md `idlePingMs`). 90 s is above every gap
  captured, so no captured sequence changes.
- **HTTP/3 idle:** Chrome and Firefox go silent, the server's idle timer
  firing 30.0 s after their last datagram, with no PING and no
  CONNECTION_CLOSE; Safari's connection outlived the 35 s gap
  (http3-2026-10-25, "How connections end"; EVIDENCE.md
  `quic.transportParameters`).
- **GOAWAY:** Firefox's on its HTTP/2 connection once HTTP/3 is up
  (`goawayTcp`, `h3-discovery`/`h3-broken`) and on a connection it
  declines for coalescing ([0023](0023-connection-limits-and-coalescing.md)).
  [INFERENCE] It lets an idle connection go the same way; Chrome's and
  Safari's way is not captured.
- **Other clients**, at the benchmarked versions (`internal/bench/go.mod`;
  read in the module cache):

  | Client | Idle HTTP/1.1 / HTTP/2 / HTTP/3 | On a proxy change | Dropped client |
  |---|---|---|---|
  | net/http (go1.27.3) | 90 s / 90 s (`DefaultTransport`; `internal/http2/transport.go:3185`) / no HTTP/3 (quic-go's `http3.Transport`, the benchmark's: 30 s QUIC idle timeout, 10 s keep-alive PINGs with or without streams, no pool eviction; [INFERENCE] a pooled connection never idles out) | no setter; old proxies' connections stay until the idle timeout or `MaxIdleConns` (100) evicts them | nothing |
  | tls-client v1.17.1 | 90 s / 90 s / QUIC 30 s with 10 s keep-alive, no pool eviction ([INFERENCE] as quic-go's) | `SetProxy` builds a new round tripper and drops the old one unclosed (`client.go:344-418`) | nothing |
  | httpcloak v1.7.3 | 90 s (30 s sweep) / 90 s, 5 min age / QUIC 30 s, keep-alive only with streams | `SetProxy` closes the old transports, in-flight HTTP/2 included (`transport/transport.go:953-1048`) | nothing; `Close` required |
  | azuretls v1.13.2 | 90 s / never (`IdleConnTimeout` not copied to its HTTP/2 transport) / QUIC 90 s, 30 s keep-alive | `SetProxy` closes idle HTTP/1.1 and HTTP/2, not HTTP/3 (`proxy.go:52-138`) | a `SetFinalizer` on `Session` that its own transport keeps reachable ([INFERENCE] never runs) |

  None closes another proxy's connections selectively; mimicry's
  Transport takes the proxy per request (`Config.Proxy`), so it has no
  `SetProxy` moment to act on and decides by use instead.
- **Tests:**
  - `TestSharedConnectionExpiresWhenIdle`, `TestExpireKey`
    (`internal/pool`, fake clock): a shared connection expires at the
    timeout since its last request, never in flight; `ExpireKey` closes a
    key's idle connections and reports one in use.
  - `TestIdleConnectionsExpire` (`internal/engine/native`, fake clock):
    Chrome's HTTP/2 connection closes at 90 s with no GOAWAY, Firefox's
    with GOAWAY first, an HTTP/3 connection ends with nothing reaching
    the server (closing it with CONNECTION_CLOSE instead fails it), and
    the next request dials anew.
  - `TestUnusedProxiesCloseTheirConnections` (fake clock): rotation over
    20 proxies closes the 4 least recent; round-robin over 4 every 15 s
    and over 24 within 10 s dials each proxy once and closes nothing
    while in use.
  - `TestDroppedTransportClosesItsConnections` (root): a dropped client's
    HTTP/2 and HTTP/1.1 connections close and the goroutine count returns
    to where it was, after garbage collection, with no
    `CloseIdleConnections`; it fails without the cleanup, and with the
    pin check holding the Transport.

## Consequences

- By default (no `Config.IdleConnTimeout` below zero, no
  `Config.MaxIdleProxies`; see the amendments), no connection or goroutine
  outlives 90 s of idleness, and a rotating client keeps at most its 16
  most recent proxies' idle connections beyond 10–20 s.
- A server whose own idle timeout is longer than 90 s sees mimicry close
  an idle HTTP/2 connection at 90 s, where the browser's time is not
  measured ([EVIDENCE.md, Not covered](../../profiles/EVIDENCE.md#not-covered-by-any-claim)).
- Round-robin over more than 16 proxies, each used less often than every
  10 s, opens a connection per request beyond the most recent 16.
- A Transport collected while requests are in flight closes their
  connections only at the idle timeout after they end.
- `Transport` documents `CloseIdleConnections` for discarded clients.

## Amendment 2026-10-28: Config.IdleConnTimeout, and no watcher goroutine

- **`Config.IdleConnTimeout`.** The 90 s stays the default (zero); a
  positive value replaces it for HTTP/1.1, HTTP/2 and HTTP/3 alike, and a
  negative one means mimicry never closes an idle connection for its
  idleness: the server, the proxy, QUIC's own idle timeout (the
  profile's `max_idle_timeout`, 30 s in Chrome and Firefox) or
  `CloseIdleConnections` ends it. The trade-off is reuse across longer
  pauses against memory, a goroutine per HTTP/2 or HTTP/3 connection and,
  through a proxy, its connection slots; servers and proxies often close
  idle connections at 60–120 s anyway. How long the pool remembers a host
  with no connections (HTTP/1.1-only) stays 90 s
  (`pool.Config.KeyTimeout`), so a negative value does not grow the pool
  without bound.
- **It does not override the proxy rule.** The unused-proxy sweep keeps
  its own rule (16 most recent, 10 s) and closes a stale proxy's idle
  connections whatever the timeout, negative included; a timeout shorter
  than 10–20 s closes them sooner by itself. The dropped-Transport
  cleanup is unchanged too.
- **One goroutine per pooled HTTP/2 connection.** The pool learnt that a
  shared connection ended from a goroutine of its own per connection,
  waiting on `Done`. A shared connection now runs a callback once it ends
  (`pool.Shared.OnClose`: the HTTP/2 read loop as it exits, the HTTP/3
  connection's goroutine that sees QUIC end), with none of the
  connection's locks held; the pool registers it after releasing its own
  lock, and on an already-closed connection it runs at once. A pooled
  idle HTTP/2 client measured (`httptest` server, 200 clients, Chrome
  profile) 3 goroutines instead of 4 (client side 1 instead of 2), stacks
  25.5 KB instead of 28.2 KB, heap about the same (45 KB, server side
  included); after `CloseIdleConnections`, none either way.
  `TestPooledHTTP2ConnectionGoroutines` (one goroutine, and removal when
  the server closes; 2 without the change) and
  `TestSharedConnectionLeavesThePoolWhenItCloses` (a close before, during
  and after the install).
- **An HTTP/2 PING keep-alive** (net/http2's `ReadIdleTimeout`, which
  tls-client sets) was considered and not added: periodic PINGs on an
  idle connection are wire traffic no browser sends in that pattern
  (only Chrome's single idle PING is captured). Without it, a connection
  that died silently is found by the next request on it, as in Firefox and
  Safari; `Config.Timeout` (30 s) bounds that request.
- Tests: `TestIdleConnectionsExpire` covers the default, 30 s and
  negative for HTTP/2 and HTTP/3; `TestNegativeIdleTimeoutKeepsIdleConnections`
  (`internal/pool`) HTTP/1.1 and shared connections kept, and
  `CloseIdle` still closing them.

## Amendment 2026-10-29: Config.MaxIdleProxies

- **Problem.** In the benchmark at `df5a13f`
  ([linux-2026-10-29](../../internal/bench/reports/linux-2026-10-29/bench-2026-10-29.md))
  a client rotating to a new identity
  every 10 requests (about every 120 ms) kept 1.8 MB of live heap and 76
  tunnels after 30 s, against azuretls's 64 KB (its `SetProxy` closes
  the old proxy's connections). The count (16) hardly mattered: the 10 s
  grace kept every identity of the last 10 s.
- **Options.** A count alone, keeping the 10 s grace: does nothing at
  that rotation rate. Closing on every proxy change (azuretls,
  httpcloak): round-robin over a list dials on every request. Changing
  the default: the round-robin warmth it buys is the reason for the rule,
  and the data show no cost to the rotating client beyond memory
  (below). **A `Config` count that replaces the rule** (chosen); a
  negative value meaning "the current proxy only" was considered and
  rejected as a second spelling of 1.
- **Decision.** `Config.MaxIdleProxies`: zero keeps the rule above (16
  most recent, or used in the last 10 s); N > 0 keeps the N most recently
  used proxies' idle connections and no time grace: a proxy pushed out of
  them is swept at once (the sweep is armed with no delay when a new
  proxy appears; an earlier due time wins over a later one), so 1 keeps
  only the latest request's proxy. Negative is an error. A stale proxy
  whose connections are in use is looked at again within 10 s, as before.
- **Evidence** (the Linux test host, i5-13500, 100 clients, HTTP/2 through CONNECT,
  1 KB, a new identity every 10 requests, 30 s, 3 passes;
  [proxyidle-2026-10-29](../../internal/bench/reports/focus-2026-10-29/ctor/after/proxyidle-2026-10-29.md)):

  | Client | Live heap per client (last third) | Tunnels open per client | CPU per request | New connection p99 |
  |---|---:|---:|---:|---:|
  | mimicry (default) | 1672–1699 KB | 69.7–70.0 | 120–124 µs | 50.6–61.1 ms |
  | `MaxIdleProxies` 4 | 360–365 KB | 3.9 | 120 µs | 44.8–45.4 ms |
  | `MaxIdleProxies` 1 | 304–306 KB | 0.9 | 120 µs | 44.8–45.4 ms |
  | tls-client | 9127–9303 KB | 187–189 | 133–135 µs | 51.6–61.6 ms |
  | azuretls | 67–68 KB | 0.9 | 161–172 µs | 45.9–48.5 ms |

  Requests/s were the same for all (6768–6862). Every mimicry variant
  still grew about 9 KB per client per second: each identity's TLS
  session ticket stays until it expires (the server's lifetime) and the
  pool remembers each connection key for 90 s (`KeyTimeout`), about 1.4
  KB per identity (a heap profile diff over 400 identities on the M3 Pro laptop).
  So 1 does not reach azuretls's 67 KB; forgetting a swept proxy's
  tickets and keys is tracked in
  [#6](https://github.com/roachadam/mimicry/issues/6).
- **Default kept**: the rotating client pays memory only (no CPU, rate or
  latency cost), and a list used in turn keeps its connections.
- Tests: `TestUnusedProxiesCloseTheirConnections` gains rows for 4 and 1
  (rotation: the pushed-out proxies close with the clock barely moving)
  and for round-robin over 4 slowly with 4 (nothing closes);
  mutation-checked (a 10 s delay or a grace kept with a count fails
  both rotation rows).

## Revisit when

- A capture measures a browser closing an idle HTTP/2 connection (time
  or frames), or an HTTP/1.1 idle timeout.
- Callers rotate over large proxy lists slowly enough to thrash, or need
  the proxy rule's values as options.
- Go's runtime starts running cleanups for objects reachable from
  goroutines, or the engine starts holding the Transport.
