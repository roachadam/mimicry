# Connections

`connections` section, from the `connections` capture
([report](../../internal/harness/reports/connections-2026-10-24.md)), three samples
per browser, Safari 26 included, identical across samples. Checked by
`TestConnectionsMatchBrowser` (every sample) and the unit tests named.
`Config.DisableCoalescing` turns `coalesce` off, each host on its own
connection ([decision 0037](../../docs/decisions/0037-coalescing-opt-out.md)),
which `TestConnectionsMatchBrowser` checks against the same samples with
the coalesced hosts on connections of their own. `Config.MaxConnsPerHost`
replaces `http1PerHost` when set.

| Claim (`connections.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 | Checked by |
|---|---|---|---|---|---|
| `http1PerHost`: 6; requests over it wait and go out on connections as they free up, later requests never before earlier ones | W: 10 parallel requests, 6 connections, the first six made one per connection | W: same | W: same | W: same | `TestConnectionsMatchBrowser`, `TestWaitersAreServedInOrder`. First come first served among the waiting requests is the browsers' source, not captured: they leave together |
| `coalesce` | W: `lookup` (no connection to `static` or `y`) | W: `connect` (one to each, SETTINGS, WINDOW_UPDATE, GOAWAY NO_ERROR, no request) | W: `lookup` | W: `lookup` | `TestConnectionsMatchBrowser` |
| The connection taken has a certificate covering the host; being same-site is not enough | W (`solo`: its own connection) | W | W | W | `TestConnectionsMatchBrowser` |
| Same address, same port, same partition, no proxy | not shown (one address, one port, credentialed requests, no proxy); required, as Chrome's and Firefox's source do ([INFERENCE] for Safari) | same | same | same | no test (a conservative choice; not shown by any capture) |
| Later requests to a coalesced host take the adopted connection (no lookup; Firefox: no new connection) | not captured ([INFERENCE], source) | same | same | same | no test (a conservative choice; not shown by any capture) |

## WebSockets

`websocket` section, from the `websocket` (HTTP/1.1 sockets) and
`websocket-h2` (extended CONNECT) captures, three samples per browser and
scenario, Safari 26 included
([report](../../internal/harness/reports/websocket-2026-10-24.md); compression:
[websocket-deflate report](../../internal/harness/reports/websocket-deflate-2026-10-26.md)).
Handshake headers are `kinds.websocket`, the socket connection's hello
`tls.websocketH1` (rows above). Hand-written; every value holds in all 6
samples of every browser. `compression`, `uncompressed` and
`windowBits8` are checked by `TestDeflateReproducesBrowserMessages`
(`ws.Compressor` replays every message of every deflate socket, the
server's `client_no_context_takeover` and `client_max_window_bits` 8–15
included: RSV1 and bytes must be the browser's) and by
`TestWebSocketsMatchBrowser` (the same messages sent through mimicry).
Level 6 is measured: level 7 fails on the varied text of
`so-deflate-sizes` for every library.

`TestWebSocketsMatchBrowser` (subtests `<profile>/<scenario>/<step>`)
replays every socket step of both captures through mimicry and compares
each sample: the handshake against `kinds.websocket` (HTTP/1.1 lines
verbatim, casing, cookies and `Sec-Fetch-*` included, the key elided;
HTTP/2 fields in order with `:protocol` last, cookie crumbs'
never-indexed flag, HEADERS flags and priority from `kinds.websocket.weight`
and `http2.headersPriority`, the stream WINDOW_UPDATE from
`http2.streamWindowUpdate`), the `insecure` variant (`plain-so`), the
cookie rules (`cookies.Request.WebSocket`, `partitionedFirst`,
`websocketUnpartitioned`) through the Cookie line, the socket
connection's hello against `tls.websocketH1` (`diff.Compare`), and every
claim below: `extensions` on the handshake; RSV1 and compressed bytes
(every message, compressed or not); DATA packing per WebSocket frame
(frames within the cap exactly, Safari's 2 / 4 / payload replies
included; larger ones within the cap, header alone where the browser's
was); the transport's end (who ends it, how, and the delay after the
last client frame within the browser's range plus 250 ms; where no
sample shows the client ending it, mimicry must not within the capture;
known differences in `webSocketEndGaps`).

| Claim (`websocket.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `extensions` | W: `permessage-deflate; client_max_window_bits` | W: `permessage-deflate` | W: `permessage-deflate` | W: same |
| `compression` | W: `chromium_zlib6` (Chromium's zlib fork, its 4-byte hash; upstream zlib differs from the first varied message on) | W: `zlib_rs6` (zlib-rs 0.6.3, as vendored in Firefox 158, fed 4096-byte output buffers; source: `WebSocketChannel.cpp` `PMCECompression`) | W: `zlib6` (upstream zlib) | W: same as 27 |
| `uncompressed` | W: `never` (empty → RSV1 `00`; incompressible and tie-length messages compressed, longer) | W: `empty_or_longer` (empty raw; with `client_no_context_takeover`, a message whose compressed form is longer raw, an equal one compressed; with takeover, longer ones compressed) | W: `empty_or_not_shorter` (empty raw, context kept; a message whose compressed form is not shorter raw, equal included, the context then restarting: the last message's bytes need the reset) | W: same as 27 |
| `windowBits8` | W: `as_9` (bytes equal to the 9-bit socket's) | not captured (source): `fail`: Firefox hangs (zlib-rs refuses −8; `HandleExtensions` deadlocks on `mCompressorMutex`, seen as a page that never finishes); the page skips the socket and mimicry fails the dial, a known difference | W: `fail` (onopen, then no frame and RST_STREAM NO_ERROR / TCP closed at once; 1006) | W: same as 27 |
| `h2Data` (DATA cap: `http2.maxDataFrame`) | W: `together` (DATA up to 16375, header and payload in one) | W: `header_alone` (a payload over 1000 bytes, `kCopyBreak` in `WebSocketChannel.h`: its 8- or 14-byte header alone, then the payload in DATA of at most 16000; compressed messages of 1030–8107 bytes show the 1000 rule) | W: `split_control` (DATA up to 16374; the pong and the close reply as 2 / 4 / payload DATA frames, its own close whole) | W: same as 27 |
| `closeH1`, `closeWaitMs` | W: `after_wait`, 2000 (TCP closed 2001–2016 ms after the handshake, the close handshake done by 5 ms; the server waits 3 s) | W: Firefox 158 on macOS not measured (`""`): no transport end in any of 30 sockets; Firefox 159 on macOS `after_wait`, 1000 (14 of 14 HTTP/1.1 sockets, 1 sample); Windows `after_wait`, 1000 (TCP closed 1002–1095 ms after, every HTTP/1.1 socket of `websocket`, `websocket-h2` and the proxied pages, 3 samples each) | W: `at_once` (0–5 ms; `plain-so` 14–21 ms, at once after its frames) | W: same |
| `closeH2` | W: `end_stream` (END_STREAM on the close frame's DATA, its own or its reply) | W: not measured (`""`): neither END_STREAM nor RST_STREAM (on Windows the server ended every HTTP/2 socket at 3 s but `cs-protocols`', which Firefox reset with RST_STREAM CANCEL 1.0 s after the close, on a connection of its own: pinned) | W: `rst_stream` (RST_STREAM NO_ERROR after the close echo) | W: same |
