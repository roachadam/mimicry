# 0023: Per-host HTTP/1.1 limit and HTTP/2 coalescing, per browser

- Status: Accepted; amended 2026-10-28 by [0037](0037-coalescing-opt-out.md) (options to disable coalescing and change the HTTP/1.1 limit); amended 2026-10-30 (no preconnects); amended 2026-11-01 (Firefox on Windows's unused connection); amended 2026-11-09 by [0064](0064-client-certificates.md) (no coalescing onto a connection with a client certificate)
- Date: 2026-10-24
- Commits: `62d9b2a`

**Summary.** Each profile's required `connections` section sets the per-host
HTTP/1.1 limit (6 in every browser) and how HTTP/2 connections are shared
between hosts: Chrome and Safari reuse one by lookup, Firefox connects and
then declines the new connection. The `connections` capture measured this,
while mimicry had opened unlimited HTTP/1.1 connections and never coalesced.
Browser behaviour stays the default; since 0037 `Config.DisableCoalescing` and
`Config.MaxConnsPerHost` can change it.

## Problem

mimicry opened as many HTTP/1.1 connections as requests needed, and never
shared an HTTP/2 connection between hosts. The replays already showed
Chrome sharing connections between hosts.

## Evidence

The `connections` capture ([report](../../internal/harness/reports/connections-2026-10-24.md)),
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
- `http1PerHost`: 6 everywhere. Requests above it wait first come,
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

## Amendment 2026-10-28: coalescing and the limit can be changed

By [0037](0037-coalescing-opt-out.md): coalescing as
above stays the default; `Config.DisableCoalescing` gives every host its
own HTTP/2 connection (no lookup, and for Firefox no connection opened and
declined), faster on lossy networks. `Config.MaxConnsPerHost` changes
`http1PerHost`'s limit (zero keeps the profile's 6).
`TestConnectionsMatchBrowser` checks both modes.

## Amendment 2026-10-30: no preconnects

Chrome opens one more TLS connection to the page's host on every page
load (a handshake, no request), on macOS and Windows, and sometimes uses
it later; through a proxy these show as extra CONNECTs that get only a
407. mimicry makes no speculative connections, and modelling Chrome's is
not planned (the maintainer's decision, 2026-10-30): the difference is a
connection count per page, noisy behind shared or rotating addresses, and
it would cost an extra, mostly unused handshake per page load (through a
proxy, the 407s too). The difference stays pinned in
`connections_test.go`. Reopen if a vendor is seen counting connections per
page.

## Amendment 2026-11-01: Firefox on Windows's unused connection

Firefox on Windows opens a second TCP connection to a page's host 0–27
ms after the first, sends its HTTP/2 preface on it and closes it with
GOAWAY, unused: in 131 of the 135 samples of pages it opened directly
over HTTPS, in none through a proxy, and never on macOS
([decision 0047, amendment](0047-windows-profiles.md#amendment-2026-11-01-firefox-on-windows)).
Its full handshake also leaves Firefox one more TLS 1.2 ticket for the
host, so one more later connection resumes. Not modelled, for the reasons
above: a connection count per page, and an extra handshake per page load
for every caller. The e2e tests leave it out of the browser's pages
(`unusedOpenings`), and the resumption count it changes is pinned
(`resumeCountGaps`). Reopen with Chrome's preconnect.

## Amendment 2026-11-09: client certificates

A connection that sent a client certificate, or resumed a session one was
sent in, takes no other host's requests: the certificate was chosen for
its host (`Config.GetClientCertificate`). Chrome's and Firefox's source
refuse the same; no capture had two hosts on one certificate
([decision 0064](0064-client-certificates.md)).
