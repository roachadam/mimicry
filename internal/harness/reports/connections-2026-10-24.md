# Connection sharing in Chrome 155, Firefox 158, Safari 27 and Safari 26, 2026-10-24

The `connections` scenario (`server.ConnectionsSteps`): from a page on
`www.conns-a.test` (HTTP/2), no-cors fetches with credentials, one step at
a time, 300 ms apart:

| Step | Host | Certificate names | Question |
|---|---|---|---|
| `coalesce.ss` | `static.conns-a.test` | itself + `www` (the page's certificate names `static` too) | on the page's connection? |
| `pair.x`, then `pair.y` | `x.conns-b.test`, `y.conns-c.test` | each names both | `y` on `x`'s connection? |
| `solo` | `solo.conns-a.test` | only itself | the control: its own connection |
| `par01`–`par10`, all at once | `h1.conns-a.test` (ALPN `http/1.1` only) | only itself | how many connections; which request on which |

Every host resolved to 127.0.0.1 and was served on one port. Each HTTP/1.1
answer was held back 1 s, so all ten requests were in flight together.
Three samples per browser; Safari 26 from a second Mac.

## HTTP/2 coalescing

Identical in all three samples of each browser:

| Connection | Chrome | Firefox | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `www` | `/`, `static…/c/coalesce.ss`, `/done` | the same | the same | the same |
| `static` | none | **its own, no request**: SETTINGS, WINDOW_UPDATE, GOAWAY (NO_ERROR) | none | none |
| `x` | `/c/pair.x`, `y…/c/pair.y` | the same | the same | the same |
| `y` | none | **its own, no request**: SETTINGS, WINDOW_UPDATE, GOAWAY (NO_ERROR) | none | none |
| `solo` | `/c/solo` | the same | the same | the same |
| Other | one more handshake to `www`, no HTTP/2 preface (a preconnect) | — | — | — |

So every browser sends a request to a host the certificate of an open
HTTP/2 connection covers on that connection (`:authority` the request's
own host), and never one to a host it does not cover (`solo` is same-site
with the page, which is not enough). They differ in when:

- **Chrome, Safari**: no connection to `static` or `y` at all. They decide
  before connecting (by source, Chrome after resolving the host, matching
  the address; [INFERENCE] for Safari).
- **Firefox**: connects and completes the TLS handshake to `static` and to
  `y` (handshake order: `www`, `static`, `x`, `y`, `solo`), sends the
  HTTP/2 preface, then at once GOAWAY with NO_ERROR and no streams, no
  SETTINGS acknowledgement; the request goes on the covering connection.

Chrome coalesces only onto connections whose certificate had no errors:
it coalesced here, so it trusted the capture CA.

## HTTP/1.1 connections per host

Ten parallel requests, six connections in every sample of every browser.
The first six requests the page made each opened a connection; the last
four (`par07`–`par10`) each went on one of them once its response ended:
four connections carried two requests, two carried one.

Arrival order (the server's log; connections numbered by their first
request's arrival), first six | last four, with the connection each of
the last four reused:

| Sample | Chrome | Firefox | Safari 27 | Safari 26 |
|---|---|---|---|---|
| 1 | 01 03 04 02 05 06 \| 07→2 08→4 09→1 10→6 | 01 04 06 03 02 05 \| 08→4 09→6 07→1 10→3 | 01–06 \| 07→1 08→3 09→2 10→4 | 01 02 03 05 04 06 \| 07→1 08→2 09→3 10→4 |
| 2 | 01–06 \| 10→1 08→2 09→5 07→3 | 03 06 01 02 05 04 \| 07→4 09→6 08→2 10→1 | 01–06 \| 07→1 08→2 09→3 10→4 | 01 02 05 03 04 06 \| 07→1 08→2 09→3 10→4 |
| 3 | 01–06 \| 10→5 09→1 08→6 07→4 | 02 06 01 03 05 04 \| 07→2 08→6 10→1 09→3 | 01 02 04 05 06 03 \| 07→1 08→2 09→3 10→4 | 01 02 04 03 05 06 \| 07→1 08→2 09→3 10→4 |

The four waiting requests leave within moments of each other on different
connections, so their arrival order shows the race between connections,
not the browser's queue: Safari's is always 07–10, Chrome's and Firefox's
vary. What every sample shows is that requests made later never took a
connection from those made earlier.

## What mimicry does

- Profile section `connections` (`profile/connections.go`), required:
  `http1_per_host` 6 for every profile; `coalesce` `lookup` for Chrome and
  both Safaris, `connect` for Firefox.
- `internal/pool`: `Config.MaxExclusive` caps a key's HTTP/1.1
  connections (in use, idle, being dialed) once its server is known to
  speak HTTP/1.1 only; more requests wait in one first-come-first-served
  queue, shared with the requests waiting for a key's first dial. A
  released connection goes straight to the oldest waiter (a dead one is
  closed and the waiter dials in its place, keeping its turn); a closed
  connection or failed dial lets the oldest waiter dial. `Dial.Adopt`
  makes another key's HTTP/2 connection this key's too; `SharedConns`
  lists the candidates.
- `internal/engine/native` (`coalesce.go`): an HTTP/2 connection keeps its
  server's address and certificate and its route group (scheme, port,
  partition; none through a proxy). With `lookup`, a dial for a key not
  known to be HTTP/1.1-only first looks for connections in the group whose
  certificate covers the host; only if one exists is the host looked up
  (`Config.Resolver`), and the request takes a connection at one of its
  addresses instead of dialing. With `connect`, after the handshake, if a
  covering connection reaches the address the new one reached, the new one
  is declined (`h2.Decline`: preface, GOAWAY NO_ERROR, close, nothing read)
  and the request goes on the other. Either way later requests to the host
  find the adopted connection in the pool directly.
- `Config.Resolver` (root and engine; also used by `net.Dialer` when there
  is no `DialContext`).

Checked by `TestConnectionsMatchBrowser` (e2e, every sample: connections
by host, protocol and request count with Chrome's preconnect pinned as the
one known difference; requests per HTTP/2 connection in order, declined
connections' frames; the early/late pattern on HTTP/1.1 connections), and
unit tests: `internal/pool/limit_test.go`, `internal/engine/native/
coalesce_test.go`, `internal/h2/decline_test.go`,
`profile/connections_test.go`.

## Not shown by this capture (conservative choices)

- **Address**: every host resolved to 127.0.0.1, so the capture cannot
  show whether the address must match. mimicry requires it (Chrome's and
  Firefox's source do; [INFERENCE] for Safari), so a host on another
  address gets its own connection.
- **Port and partition**: one port, credentialed requests only. mimicry
  coalesces only to the same port and within one partition (Chrome keeps
  credential-less requests apart anyway).
- **Proxies**: not captured. mimicry never coalesces through a proxy (the
  proxy resolves names, so the address cannot be checked).
- **Which certificate**: the pair's certificates name each other, so the
  capture cannot tell whether the existing connection's or the new host's
  certificate must cover; mimicry checks the existing connection's (the one
  the request goes on, RFC 9113 9.1.1).
- **Later requests to a coalesced host**: not captured; they take the
  adopted connection with no lookup and, for Firefox, no new connection
  ([INFERENCE], from Chrome's and Firefox's source).
- **Queue order among the waiting requests**: first come first served
  (the browsers' source: one queue per host, by priority then order); the
  capture only shows that later requests never overtook earlier ones.
- **Plain HTTP, proxies, other limits**: the cap was measured on HTTPS;
  mimicry applies it to plain HTTP too and per proxy route ([INFERENCE]).
  Chrome's 256 total and 32 per proxy, Firefox's 900 total, are not
  modelled.
- **Chrome's preconnect** to the page's host (a handshake, no preface, no
  request): mimicry makes no speculative connections (as in the kinds
  report).
