# 0041: NewClient times requests out after 30 s

- Status: Accepted
- Date: 2026-10-28
- Commits: (this commit)

## Problem

A request whose server stops sending mid-response, while keeping the
connection alive (it answers keep-alive PINGs), waited until the caller's
context ended, or forever without a deadline. The multi-client benchmark
hit this against nginx over HTTP/3 (3 in about 87,000 requests on one
CPU; backlog). `DialTimeout` covers only connecting.

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

## Consequences

- Covers connecting, the request and reading the whole body: large
  downloads and slow proxies need a larger value.
- Invisible on the wire: hanging up is what a closed tab does.
