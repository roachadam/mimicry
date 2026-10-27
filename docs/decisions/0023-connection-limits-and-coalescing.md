# 0023: Per-host HTTP/1.1 limit and HTTP/2 coalescing, per browser

- Status: Accepted
- Date: 2026-10-24
- Commits: (the wave-3 commit)

## Problem

mimicry opened as many HTTP/1.1 connections as requests needed, and never
shared an HTTP/2 connection between hosts. The replays already showed
Chrome sharing connections between hosts.

## Evidence

The `connections` capture ([report](../../harness/reports/connections-2026-10-24.md)),
three identical samples per browser:
- 10 parallel requests to an HTTP/1.1 host open 6 connections in every
  browser. The first six requests each open one; the rest queue behind
  them.
- An HTTP/2 host whose certificate covers another host, at the same
  address, carries that host's requests:
  - Chrome and both Safaris use it without connecting to the second host;
  - Firefox also connects to the second host, sends SETTINGS,
    WINDOW_UPDATE and GOAWAY on that connection, closes it, and uses the
    first connection.
- A certificate covering only its own host gets its own connection.

## Decision

A required profile section `connections`:
- `http1_per_host`: 6 everywhere. Requests above it wait first come,
  first served.
- `coalesce`:
  - `lookup` (Chrome, Safari): resolve the host, and use an existing
    connection whose certificate covers it and whose address matches;
  - `connect` (Firefox): dial and handshake, then decline the new
    connection the way Firefox does (`h2.Decline`) if an existing one
    reaches the same address. `h2.Decline` drains what the server sent
    (up to 1 s) before closing, so a TCP reset cannot overtake its frames:
    every Firefox sample shows them all arriving. How Firefox's own close
    looks on TCP is not captured.

Conditions the capture could not vary are kept conservative: same port,
same partition, no proxy. `Config.Resolver` lets callers (and the harness)
resolve hosts the way they dial them.

## Consequences

Chrome's speculative preconnect to the page host stays a pinned known
difference.

## Revisit when

A capture varies the address, the port, the partition or a proxy, or a
browser's limit changes.
