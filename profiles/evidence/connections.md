# Connections

The `connections` section comes from the `connections` capture ([report](../../internal/harness/reports/connections-2026-10-24.md)).

- Three samples per browser, Safari 26 included. Samples are identical.
- Checked by `TestConnectionsMatchBrowser` (every sample) and the unit tests named below.
- `Config.DisableCoalescing` turns `coalesce` off: each host on its own connection ([decision 0037](../../docs/decisions/0037-coalescing-opt-out.md)).
  - `TestConnectionsMatchBrowser` checks it against the same samples, with the coalesced hosts on connections of their own.
- `Config.MaxConnsPerHost` replaces `http1PerHost` when set.

| Claim (`connections.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 | Checked by |
|---|---|---|---|---|---|
| `http1PerHost` [1] | W [2] | W: same | W: same | W: same | [3] |
| `coalesce` | W: `lookup` [4] | W: `connect` [5] | W: `lookup` | W: `lookup` | `TestConnectionsMatchBrowser` |
| Certificate covers the host [6] | W [7] | W | W | W | `TestConnectionsMatchBrowser` |
| Same address, port, partition, no proxy | not shown [8] | same | same | same | no test [9] |
| Later requests take the adopted connection [10] | not captured [11] | same | same | same | no test [9] |

Notes:
1. 6. Requests over it wait and go out on connections as they free up. Later requests never go before earlier ones.
2. 10 parallel requests, 6 connections, the first six made one per connection.
3. `TestConnectionsMatchBrowser`, `TestWaitersAreServedInOrder`. First come first served among the waiting requests is the browsers' source, not captured: they leave together.
4. No connection to `static` or `y`.
5. One to each: SETTINGS, WINDOW_UPDATE, GOAWAY NO_ERROR, no request.
6. The connection taken has a certificate covering the host. Being same-site is not enough.
7. `solo`: its own connection.
8. The capture has one address, one port, credentialed requests and no proxy. Required, as Chrome's and Firefox's source do ([INFERENCE] for Safari).
9. A conservative choice; not shown by any capture.
10. Later requests to a coalesced host take the adopted connection: no lookup; Firefox: no new connection.
11. [INFERENCE], source.

## WebSockets

The `websocket` section comes from two captures, three samples per browser and scenario, Safari 26 included.

- `websocket`: HTTP/1.1 sockets.
- `websocket-h2`: extended CONNECT.
- Reports: [report](../../internal/harness/reports/websocket-2026-10-24.md); compression: [websocket-deflate report](../../internal/harness/reports/websocket-deflate-2026-10-26.md).
- Handshake headers are `kinds.websocket`. The socket connection's hello is `tls.websocketH1` (rows above).
- Hand-written. Every value holds in all 6 samples of every browser.
- Level 6 is measured: level 7 fails on the varied text of `so-deflate-sizes` for every library.

### Compression tests

`compression`, `uncompressed` and `windowBits8` are checked by:

- `TestDeflateReproducesBrowserMessages`: `ws.Compressor` replays every message of every deflate socket.
  - The server's `client_no_context_takeover` and `client_max_window_bits` 8–15 are included.
  - RSV1 and bytes must be the browser's.
- `TestWebSocketsMatchBrowser`: the same messages sent through mimicry.

### TestWebSocketsMatchBrowser

`TestWebSocketsMatchBrowser` (subtests `<profile>/<scenario>/<step>`) replays every socket step of both captures through mimicry and compares each sample:

- **Handshake** against `kinds.websocket`:
  - HTTP/1.1 lines verbatim: casing, cookies and `Sec-Fetch-*` included, the key elided.
  - HTTP/2 fields in order with `:protocol` last, and cookie crumbs' never-indexed flag.
  - HEADERS flags and priority from `kinds.websocket.weight` and `http2.headersPriority`.
  - The stream WINDOW_UPDATE from `http2.streamWindowUpdate`.
- **Insecure variant**: `insecure` (`plain-so`).
- **Cookie rules**: `cookies.Request.WebSocket`, `partitionedFirst`, `websocketUnpartitioned`, through the Cookie line.
- **Hello**: the socket connection's hello against `tls.websocketH1` (`diff.Compare`).
- **Every claim below**:
  - `extensions` on the handshake.
  - RSV1 and compressed bytes: every message, compressed or not.
  - DATA packing per WebSocket frame: frames within the cap exactly, Safari's 2 / 4 / payload replies included; larger ones within the cap, header alone where the browser's was.
  - The transport's end: who ends it, how, and the delay after the last client frame, within the browser's range plus 250 ms.
  - Where no sample shows the client ending it, mimicry must not within the capture.
  - Known differences are in `webSocketEndGaps`.

### Claims

| Claim (`websocket.…`) | Chrome 155 | Firefox 158 | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `extensions` | W: `permessage-deflate; client_max_window_bits` | W: `permessage-deflate` | W: `permessage-deflate` | W: same |
| `compression` | W: `chromium_zlib6` [1] | W: `zlib_rs6` [2] | W: `zlib6` (upstream zlib) | W: same as 27 |
| `uncompressed` | W: `never` [3] | W: `empty_or_longer` [4] | W: `empty_or_not_shorter` [5] | W: same as 27 |
| `windowBits8` | W: `as_9` [6] | not captured: `fail` [7] | W: `fail` [8] | W: same as 27 |
| `h2Data` [9] | W: `together` [10] | W: `header_alone` [11] | W: `split_control` [12] | W: same as 27 |
| `closeH1`, `closeWaitMs` | W: `after_wait`, 2000 [13] | W [14] | W: `at_once` [15] | W: same |
| `closeH2` | W: `end_stream` [16] | W: not measured (`""`) [17] | W: `rst_stream` [18] | W: same |

Notes:
1. Chromium's zlib fork, its 4-byte hash. Upstream zlib differs from the first varied message on.
2. zlib-rs 0.6.3, as vendored in Firefox 158, fed 4096-byte output buffers. Source: `WebSocketChannel.cpp` `PMCECompression`.
3. Empty → RSV1 `00`. Incompressible and tie-length messages are compressed, longer.
4. Firefox `empty_or_longer`:
   - Empty: raw.
   - With `client_no_context_takeover`: a message whose compressed form is longer goes raw; an equal one is compressed.
   - With takeover: longer ones are compressed.
5. Safari `empty_or_not_shorter`:
   - Empty: raw, context kept.
   - A message whose compressed form is not shorter (equal included) goes raw, and the context then restarts.
   - The last message's bytes need the reset.
6. Bytes equal to the 9-bit socket's.
7. Firefox `windowBits8` (source):
   - Firefox hangs: zlib-rs refuses −8, and `HandleExtensions` deadlocks on `mCompressorMutex`, seen as a page that never finishes.
   - The page skips the socket and mimicry fails the dial, a known difference.
8. onopen, then no frame and RST_STREAM NO_ERROR / TCP closed at once; 1006.
9. DATA cap: `http2.maxDataFrame`.
10. DATA up to 16375, header and payload in one.
11. Firefox `header_alone`:
    - A payload over 1000 bytes (`kCopyBreak` in `WebSocketChannel.h`): its 8- or 14-byte header alone, then the payload in DATA of at most 16000.
    - Compressed messages of 1030–8107 bytes show the 1000 rule.
12. DATA up to 16374. The pong and the close reply go as 2 / 4 / payload DATA frames; its own close whole.
13. TCP closed 2001–2016 ms after the handshake, the close handshake done by 5 ms. The server waits 3 s.
14. Firefox `closeH1`:
    - Firefox 158 on macOS: not measured (`""`). No transport end in any of 30 sockets.
    - Firefox 159 on macOS: `after_wait`, 1000 (14 of 14 HTTP/1.1 sockets, 1 sample).
    - Windows: `after_wait`, 1000. TCP closed 1002–1095 ms after, on every HTTP/1.1 socket of `websocket`, `websocket-h2` and the proxied pages, 3 samples each.
15. 0–5 ms; `plain-so` 14–21 ms, at once after its frames.
16. END_STREAM on the close frame's DATA, its own or its reply.
17. Firefox `closeH2`:
    - Neither END_STREAM nor RST_STREAM.
    - On Windows the server ended every HTTP/2 socket at 3 s, except `cs-protocols`'.
    - Firefox reset that one with RST_STREAM CANCEL 1.0 s after the close, on a connection of its own: pinned.
18. RST_STREAM NO_ERROR after the close echo.
