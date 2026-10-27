# WebSockets: what Chrome 155, Firefox 158 and Safari 27/26 send, 2026-10-24

The `websocket` page (the HTTP/2 server does not advertise
`SETTINGS_ENABLE_CONNECT_PROTOCOL`) and `websocket-h2` (it does), three
samples per browser and scenario; Safari 26 on the second Mac. Sockets and
cookies as in `server.WebSocketSteps` / `WebSocketPageSteps`: `so-basic`,
`cs-protocols` (cross-site, `chat, superchat`), `so-deflate`,
`so-server-close` (server sends 1001 first), `mixed` (`ws://` from the
`https://` page) and `plain-so` (`ws://` from the `http://` page). Unless a
count is given, a finding holds in every sample (3 of 3) of the scenario
named, or in all 6 when no scenario is named.

## Transport

| | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| `websocket` (no extended CONNECT) | HTTP/1.1 Upgrade on a new TLS connection per socket (4 per sample), one request each, never reused | the same | the same |
| `websocket-h2` | RFC 8441 extended CONNECT on the page's existing h2 connection (stream 7, 13, 17 on connection 1); the cross-site socket on the ws-b connection that carried the credentialed `b-set-3p` fetch (the page's partition), not on the first-party ws-b connection | the same | the same |
| ALPN on the HTTP/1.1 socket connections | `http/1.1` only | `http/1.1` only | `http/1.1` only |
| Rest of the hello vs the page's | no ALPS (`0x44cd`), with a PSK (`pre_shared_key`, resumed); otherwise the page's (Chrome permutes extensions anyway) | with a PSK, without `session_ticket` (as Firefox's other resumed hellos) and **without `compress_certificate` (`0x001b`)**, which the page's resumed h2 hellos keep; ECH payload 400 (resumed) | identical apart from ALPN |
| `ws://` sockets (`plain-so`) | HTTP/1.1 on a new TCP connection, not the page's keep-alive one | the same | the same |

In `websocket-h2` no browser opened an HTTP/1.1 connection for a `wss://`
socket.

## Handshake request

### HTTP/1.1 (`websocket`), verbatim order (key and port elided)

Identical across the 3 samples for every socket; `plain-so` identical in
both scenarios.

Chrome, same-origin (`so-basic`, `so-deflate`, `so-server-close`):

```
GET /ws/so-basic HTTP/1.1
Host: www.ws-a.test:PORT
Connection: Upgrade
Pragma: no-cache
Cache-Control: no-cache
User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36
Upgrade: websocket
Origin: https://www.ws-a.test:PORT
Sec-WebSocket-Version: 13
Accept-Encoding: gzip, deflate, br, zstd
Accept-Language: en-US,en;q=0.9
Cookie: ws_a_lax=1; ws_a_none=1; ws_a_part=1
Sec-WebSocket-Key: <key>
Sec-WebSocket-Extensions: permessage-deflate; client_max_window_bits
```

Cross-site (`cs-protocols`): `Cookie: ws_b_none=1; ws_b_part3p=1`, and
`Sec-WebSocket-Protocol: chat, superchat` appended last. `plain-so`:
`Origin: http://…`, `Accept-Encoding: gzip, deflate`,
`Cookie: ws_a_lax=1`, otherwise unchanged.

Firefox, same-origin:

```
GET /ws/so-basic HTTP/1.1
Host: www.ws-a.test:PORT
User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:158.0) Gecko/20100101 Firefox/158.0
Accept: */*
Accept-Language: en-US,en;q=0.9
Accept-Encoding: gzip, deflate, br, zstd
Sec-WebSocket-Version: 13
Origin: https://www.ws-a.test:PORT
Sec-WebSocket-Extensions: permessage-deflate
Sec-WebSocket-Key: <key>
Connection: Upgrade
Cookie: ws_a_lax=1; ws_a_none=1; ws_a_part=1
Sec-Fetch-Dest: empty
Sec-Fetch-Mode: websocket
Sec-Fetch-Site: same-origin
Pragma: no-cache
Cache-Control: no-cache
Upgrade: websocket
```

Cross-site: `Sec-WebSocket-Protocol: chat, superchat` after `Origin`,
`Sec-Fetch-Storage-Access: none` after `Sec-WebSocket-Key`,
`Cookie: ws_b_part3p=1`, `Sec-Fetch-Site: cross-site`. `plain-so`: the
`Sec-Fetch-*` lines dropped, `Origin: http://…`,
`Accept-Encoding: gzip, deflate`, `Cookie: ws_a_lax=1`.

Safari 27 (Safari 26 the same with `Version/26.2` and
`Accept-Encoding: gzip, deflate, br`), same-origin:

```
GET /ws/so-basic HTTP/1.1
Host: www.ws-a.test:PORT
Origin: https://www.ws-a.test:PORT
Pragma: no-cache
Sec-Fetch-Site: same-origin
Sec-WebSocket-Version: 13
Sec-Fetch-Mode: websocket
User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.0.1 Safari/605.1.15
Sec-WebSocket-Extensions: permessage-deflate
Cache-Control: no-cache
Sec-Fetch-Dest: websocket
Accept: */*
Accept-Language: en-US,en;q=0.9
Priority: u=3, i
Accept-Encoding: gzip, deflate, br, zstd
Cookie: ws_a_part=1; ws_a_lax=1; ws_a_none=1
Sec-WebSocket-Key: <key>
Connection: Upgrade
Upgrade: websocket
```

Cross-site: `Sec-WebSocket-Protocol: chat, superchat` after
`Sec-Fetch-Site: cross-site`. `plain-so` is a different order, not this one
minus lines:

```
GET /ws/plain-so HTTP/1.1
Host: www.ws-a.test:PORT
User-Agent: …
Cache-Control: no-cache
Sec-WebSocket-Version: 13
Sec-WebSocket-Extensions: permessage-deflate
Origin: http://www.ws-a.test:PORT
Pragma: no-cache
Accept: */*
Accept-Language: en-US,en;q=0.9
Priority: u=3, i
Accept-Encoding: gzip, deflate
Cookie: ws_a_lax=1
Sec-WebSocket-Key: <key>
Connection: Upgrade
Upgrade: websocket
```

### HTTP/2 extended CONNECT (`websocket-h2`)

Instructions identical across samples per socket (HPACK indices differ only
with table state). No browser sends `Sec-WebSocket-Key`, `Connection`,
`Upgrade` or `Host`; the remaining fields keep the HTTP/1.1 order.

| | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| Pseudo-headers | `:method CONNECT, :authority, :scheme, :path, :protocol` | `:method, :path, :authority, :scheme, :protocol` | `:method, :scheme, :authority, :path, :protocol` |
| = normal GET order + `:protocol` last | yes | yes | yes |
| HEADERS flags | `0x24` END_HEADERS+PRIORITY, no END_STREAM | `0x24`, no END_STREAM | `0x04` END_HEADERS only |
| Priority | depends 0, exclusive, weight 146 | depends 0, non-exclusive, weight 21 | none |
| Regular fields | `pragma, cache-control, user-agent, origin, sec-websocket-version, accept-encoding, accept-language, cookie…, sec-websocket-extensions[, sec-websocket-protocol]` | `user-agent, accept, accept-language, accept-encoding, sec-websocket-version, origin[, sec-websocket-protocol], sec-websocket-extensions[, sec-fetch-storage-access], cookie…, sec-fetch-dest, sec-fetch-mode, sec-fetch-site, pragma, cache-control` | `origin, pragma, sec-fetch-site[, sec-websocket-protocol], sec-websocket-version, sec-fetch-mode, user-agent, sec-websocket-extensions, cache-control, sec-fetch-dest, accept, accept-language, priority, accept-encoding, cookie…` |
| Cookie | one field per cookie (crumbs), indexed/incremental like other fields | crumbs, never-indexed (`0x10`, name index 32) | crumbs, never-indexed, last |
| After HEADERS | — | a WINDOW_UPDATE on the stream | — |

### Values

| | Chrome | Firefox | Safari 27 | Safari 26 |
|---|---|---|---|---|
| `Sec-WebSocket-Extensions` | `permessage-deflate; client_max_window_bits` | `permessage-deflate` | `permessage-deflate` | `permessage-deflate` |
| `Sec-WebSocket-Protocol` | `chat, superchat` | same | same | same |
| `Sec-WebSocket-Version` | 13 | 13 | 13 | 13 |
| `Pragma` / `Cache-Control` | `no-cache` / `no-cache` | same | same | same |
| `Accept` | none | `*/*` | `*/*` | `*/*` |
| `Sec-Fetch-*` | none, on any socket | `Dest: empty`, `Mode: websocket`, `Site`; `Storage-Access: none` cross-site only | `Site`, `Mode: websocket`, `Dest: websocket` | same as 27 |
| `Priority` | none | none | `u=3, i` | `u=3, i` |
| `Referer` | none | none | none | none |
| Same-origin cookies | `ws_a_lax; ws_a_none; ws_a_part` | same | `ws_a_part; ws_a_lax; ws_a_none` | same as 27 |
| Cross-site cookies (`www.ws-b.test` from ws-a) | `ws_b_none; ws_b_part3p` | `ws_b_part3p` | `ws_b_part3p` | **`ws_b_part3p; ws_b_lax; ws_b_none`** |
| `plain-so` cookies | `ws_a_lax` | same | same | same |
| `plain-so` `Accept-Encoding` | `gzip, deflate` | same | same | same |

No browser sent `ws_b_part` (ws-b's own top-level partition) on the
cross-site socket. Safari 26 sending the Lax and unpartitioned None cookies
cross-site holds in all 6 of its samples (HTTP/1.1 and h2); Safari 27 never
does.

Compared with each browser's credentialed cross-site `fetch()`
(`b-set-3p`, same page): Chrome's socket drops `sec-ch-ua*`, every
`sec-fetch-*` (the fetch has `sec-fetch-storage-access: active`), `accept`,
`referer` and `priority`, and adds `pragma`/`cache-control`; Firefox's
keeps `Sec-Fetch-Storage-Access: none` but uses `Sec-Fetch-Mode:
websocket`, drops `Referer`, `Priority` and `TE`; Safari's uses
`Sec-Fetch-Dest: websocket` (Firefox: `empty`), drops `Referer` and keeps
`Priority: u=3, i`. The orders are the browsers' WebSocket orders above,
not the fetch orders.

## Frames

| | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| Masking | every client frame (141 frames, 141 distinct keys, none zero) | same (138/138) | same (138/138 each) |
| Lengths | minimal form: 5, 125 → 7-bit; 126, 200, 65535, 256 → 16-bit; 65536 (text and binary) → 64-bit | same | same |
| 71680-byte text | 64-bit single frame, **or** fragmented `text(FIN=0) 65464` + `continuation 6216` (both 16-bit): fragmented in 1 of 3 `websocket` and 2 of 3 `websocket-h2` samples | single 64-bit frame | single 64-bit frame |
| Opcodes | text for strings, binary for ArrayBuffers | same | same |
| permessage-deflate | RSV1 on every message, the 5-byte one included (7 bytes); context takeover (second 4096 → 31 bytes after 45); output byte-identical to zlib raw deflate level 6, `Z_SYNC_FLUSH`, trailer stripped | RSV1 on every message; takeover (61 → 46); 71680 → 376; **not reproduced by zlib** at any level / window / memLevel / strategy, nor with per-chunk flushes | **5-byte message sent uncompressed** (RSV1 clear), the rest compressed; takeover (46 → 31); byte-identical to zlib level 6 when the 5-byte message is left out of the context |
| Compressed bytes | identical across all 6 samples | identical | identical (27 = 26) |
| Server ping (`mimicry`) | pong with the same 7 bytes, before the first data frame, within 1 ms | same (0–1 ms) | same |
| Close | `close 1000 "bye"` (5 bytes); server echoes the code | same | same |

On h2 (DATA frames per WebSocket frame): Chrome writes header and payload
together, DATA up to 16375 bytes; Firefox writes a large frame's header
alone (8 or 14 bytes) then the payload in 16000-byte DATA; Safari packs
data frames whole (DATA up to 16374) but writes its pong and its reply to
the server's close as three DATA frames (2-byte header, 4-byte mask,
payload).

### Transport ending after a client close

| | Chrome | Firefox | Safari 27, 26 |
|---|---|---|---|
| HTTP/1.1 | client closes TCP ≈ 2.0 s after the handshake (2001–2016 ms; its frames end by 5 ms), i.e. waits ~2 s for the server, then closes; the page's `onclose` waits too (sockets 2 s apart) | **never observed**: `closed_by` empty on all 30 sockets | client closes TCP at once (0–5 ms; `plain-so` 1–21 ms) |
| h2 | `END_STREAM` on the DATA carrying its close frame (0–3 ms) | **no END_STREAM, no RST_STREAM** in any sample | `RST_STREAM NO_ERROR` right after the close echo (0–4 ms) |

## Server-initiated close (1001 "going away")

All browsers, all samples, both scenarios: one text echo, then the reply
`close 1001 "going away"` (12 bytes: the server's code **and reason**
echoed) within 1 ms; script saw `onclose` 1001 `going away`, clean. Then:
Chrome HTTP/1.1 closes TCP at 2001–2008 ms, Chrome h2 sets END_STREAM on the
close reply's DATA; Safari HTTP/1.1 closes TCP immediately, Safari h2 sends
`RST_STREAM NO_ERROR`; Firefox ends neither (as above).

## Mixed content and plain sockets

| `mixed` (`ws://` from `https://`) | Result in all 6 samples |
|---|---|
| Chrome | constructor throws `SecurityError: Failed to construct 'WebSocket': An insecure WebSocket connection may not be initiated from a page loaded over HTTPS.`; nothing reached the server |
| Firefox | `onerror`, `onclose` 1006 (not clean), never open; no plain handshake reached the server |
| Safari 27, 26 | `onerror`, no `onclose`, the step timed out (10 s); no plain handshake reached the server |

Whether Firefox or Safari upgraded `mixed` to `wss://` against the plain
port (which refuses TLS) is not in the page trace: refused TLS hellos are
only in the server's log, which is not kept with the captures. The same
holds for Safari's TLS attempts on the plain port before `plain-so` and the
`http://` page: the trace cannot show them. What it shows: every browser's
`plain-so` handshake arrived as plain HTTP/1.1 (Safari 27's first frame
then came 13–20 ms after the handshake, Safari 26's 0–1 ms), with the
non-secure rules of the plain-HTTP report (no
`Sec-Fetch-*` in Firefox and Safari, `gzip, deflate`, only the non-Secure
`ws_a_lax`).

## WSResults

Every `wss://` and `plain-so` step opened, echoed all messages and closed
clean (1000, or 1001 `going away`), with `protocol` `chat` on
`cs-protocols` and `extensions` `permessage-deflate` on `so-deflate`. The
only failures are `mixed`, as above. No server-side frame error in any
sample.

## What mimicry must model

| | Chrome | Firefox | Safari 27 | Safari 26 |
|---|---|---|---|---|
| Transport choice (code) | h2 extended CONNECT on the pooled connection when the server sets ENABLE_CONNECT_PROTOCOL, else a fresh HTTP/1.1-only TLS connection per socket | same | same | same |
| HTTP/1.1 socket hello (profile) | ALPN `http/1.1`, no ALPS | ALPN `http/1.1`, no `compress_certificate` | ALPN `http/1.1` | ALPN `http/1.1` |
| Header lists (profile) | own HTTP/1.1 order; h2 = same minus Host/Connection/Upgrade/Key | same | same; separate `insecure` order | same |
| Pseudo-header order (profile) | m,a,s,p,`:protocol` | m,p,a,s,`:protocol` | m,s,a,p,`:protocol` | same |
| HEADERS priority (profile) | excl, weight 146 | non-excl, weight 21 (+ stream WINDOW_UPDATE) | none | none |
| Extension offer (profile) | `permessage-deflate; client_max_window_bits` | `permessage-deflate` | `permessage-deflate` | same |
| Cookies (code + profile) | 3p: None + partitioned | 3p: partitioned only | 3p: partitioned only; partitioned first | 3p: partitioned + Lax + None; partitioned first |
| Framing (code) | minimal lengths; ~64 KiB fragmentation sometimes | single frames | single frames | single frames |
| Compression (code) | zlib level 6, every message | own encoder (not zlib-reproducible) | zlib level 6, small messages uncompressed | same |
| h2 DATA packing (code) | ≤16375, header+payload | header alone, 16000 chunks | ≤16374; control frames split 2/4/n | same |
| Close (code) | echo code+reason; h1 TCP close after ~2 s; h2 END_STREAM on the close DATA | echo; transport left open | echo; h1 TCP close at once; h2 RST_STREAM NO_ERROR | same |
| `ws://` from `https://` | refuse (SecurityError) | fail (1006) | fail (error, no close) | same |

## Unexplained or inconsistent

- Chrome's 71680-byte text: fragmented at 65464 bytes in 3 of 6 samples,
  whole in the other 3; the 65535- and 65536-byte messages never were.
- Firefox's ending: no transport end in 30 sockets, not even the server's
  own after `WSLinger` (3 s), which suggests its pages finished (and the
  trace was written) before the linger fired; whether Firefox ever closes
  the TCP connection or stream itself is not measured.
- Firefox's deflate output matches no zlib configuration tried
  (explained 2026-10-26: zlib-rs; see the
  [websocket-deflate report](websocket-deflate-2026-10-26.md)).
- Firefox opens extra h2 connections that send only SETTINGS,
  WINDOW_UPDATE and GOAWAY (2 per sample in `websocket`, 2–3 in
  `websocket-h2`; resumed hellos with ALPN `h2, http/1.1`); Chrome has 2
  unused connections per sample in both scenarios. They appear whether or
  not sockets go over h2, so they are not the socket's transport.
- Firefox's HTTP/1.1 socket hello drops `compress_certificate`, which its
  other resumed hellos keep.
- Safari 26 vs 27 on cross-site socket cookies (above): consistent within
  each version, different between them.
- Safari's refused TLS attempts on the plain port (`mixed`, `plain-so`) are
  not recorded in the pagetrace and cannot be reported from these captures.
