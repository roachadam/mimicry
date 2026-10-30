# 0020: The connection pool is its own package

- Status: Accepted; amended 2026-10-28 by [0040](0040-idle-connection-cleanup.md) (idle timeout for every protocol, `Config.IdleConnTimeout`, a separate `KeyTimeout`, `Shared.OnClose`)
- Date: 2026-10-24
- Commits: `ea769bc`

## Problem

The pool lived inside `internal/engine/native/engine.go`, mixed with
routing, dialing and the proxy-auth retry. `internal/pool` held only a
context key. The review found most of the engine's bugs in that mix:
- idle HTTP/1.1 connections were never checked or reaped;
- a failed shared dial was retried by every waiter in turn;
- waiters needed a rewindable body although theirs was never sent;
- a graceful GOAWAY closed streams the server had promised to finish;
- per-key state was never forgotten.

None of it had unit tests. WebSockets (dedicated connections) and HTTP/3
(the same keys over another transport) would add to it.

## Options

- **Fix the bugs in place.** The bugs go, the mix stays, and it is still
  testable only end to end.
- **Move the pool to `internal/pool`**, generic over the HTTP/2 and
  HTTP/1.1 connection types, with the dial injected. Unit-tested with
  fake connections and a fake clock.

## Decision

`internal/pool` holds the pool. Per key (scheme, host, port, partition,
proxy) it keeps:
- one shared multiplexed connection;
- an idle HTTP/1.1 list, liveness-checked (`h1.Conn.Usable`) outside the
  lock before reuse, and reaped after the idle timeout;
- one dial shared by concurrent requests, whose error goes to every waiter
  unless only the dialer's own context ended.

A connection that answers "unusable" (GOAWAY, REFUSED_STREAM) leaves the
pool unclosed; it finishes its streams and closes itself. A request that
was never sent retries as is (`errNotSent`); one whose body was read needs
a fresh body (`errRetry`). A key is forgotten (its "HTTP/1.1 only" flag
included) an idle timeout after its last connection.

TLS tickets and proxy credentials are **not** forgotten with the key:
- browsers resume a later connection with an earlier one's ticket (the
  `resume` capture);
- they send proxy credentials unasked on later connections (the `proxy`
  captures).

Expired TLS 1.3 tickets are dropped, so an expired ticket never yields the
resumed ClientHello shape. TLS 1.2 ticket lifetime is unmeasured.

## Consequences

The engine routes and dials; the pool is tested without a network. The
90 s idle timeout and the reaper are unmeasured values, listed in the
ledger. Proxy-credential state grows with every proxy used.

## Revisit when

A capture measures a browser's idle timeout, ticket lifetime or per-host
connection cap.

## Amendment 2026-10-28: every protocol, and its own settings

- HTTP/3 has a pool of its own, the same type with QUIC connections as
  the shared kind (`pool.Pool[*quicConn, noExclusive]`).
- The idle timeout covers the shared connections too, HTTP/2 and HTTP/3,
  counted from their last request's end (`Shared.IdleSince`, closed with
  `Shared.Expire`). `ExpireKey` closes a key's idle connections early, for
  a proxy the Transport no longer uses
  ([0040](0040-idle-connection-cleanup.md)).
- The 90 s is now the default of `Config.IdleConnTimeout` (negative: idle
  connections never close for their idleness). Forgetting a key is a
  separate `KeyTimeout`, fixed at 90 s, so a shorter idle timeout does not
  forget an HTTP/1.1-only server sooner.
- A shared connection tells the pool it has closed through
  `Shared.OnClose`; no goroutine watches it.
- With proxy credentials sent up front by default
  ([0039](0039-proxy-credentials-up-front.md)), the credential state grows
  only with `Config.BrowserProxyAuth`.
