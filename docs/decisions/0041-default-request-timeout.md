# 0041: NewClient times requests out after 30 s

- Status: Accepted
- Date: 2026-10-28
- Commits: `8b681d0`

**Summary.** `NewClient` sets a 30 s total timeout per request by default
through `Config.Timeout` (negative means none); `NewTransport` ignores
it. A server that stalls mid-response on a live connection otherwise hung
a request forever, and 30 s matches the other impersonating clients. A
library user doing large downloads or using slow proxies should raise it;
it is invisible on the wire.

## Problem

A request whose server stops sending mid-response, while keeping the
connection alive (it answers keep-alive PINGs), waited until the caller's
context ended, or forever without a deadline. The multi-client benchmark
hit this against nginx over HTTP/3 (3 in about 87,000 requests on one
CPU; [#5](https://github.com/roachadam/mimicry/issues/5)). `DialTimeout` covers only connecting.

## Options

- **No default** (net/http). Callers must remember a deadline; a missing one
  hangs a goroutine and its client.
- **30 s total per request from `NewClient`** (chosen). Matches
  tls-client (`DefaultTimeoutSeconds`), httpcloak (`WithTimeout`) and
  azuretls (`Session.TimeOut`), all 30 s.
- **A read-idle timeout inside the engine.** Finer, but new engine state for
  what `http.Client.Timeout` already does.

## Decision

`Config.Timeout`: 0 means 30 s, positive is used as is, negative means no
timeout. `NewClient` sets `http.Client.Timeout` from it; callers may still
change the returned client's `Timeout`. `NewTransport` ignores it, as
net/http's Transport has none.

## Evidence

- The stalls: benchmark layer 6 with the client on 1 CPU, 3 of 30 points
  ([multiclient-rotation report](../../internal/bench/reports/multiclient-rotation-2026-10-28.md);
  [#5](https://github.com/roachadam/mimicry/issues/5) has the analysis). Nothing about them is
  captured from a browser, and nothing here changes what is sent.
- `TestClientTimeout` (root, `idle_test.go`): `NewClient`'s
  `http.Client.Timeout` is 30 s by default and none when negative; with
  200 ms, reading the body of an HTTP/2 response whose server sent half
  of it and then nothing fails with a timeout instead of hanging.

## Consequences

- Covers connecting, the request and reading the whole body: large
  downloads and slow proxies need a larger value.
- Invisible on the wire: hanging up is what a closed tab does.

## Revisit when

- Callers routinely hit the 30 s on large downloads or slow proxies, and a
  read-idle timeout (time since the last byte, not since the start) would
  serve them better.
- The mid-stream stall ([#5](https://github.com/roachadam/mimicry/issues/5)) is explained and fixed in the
  engine, so the default no longer has a known stall to bound.
