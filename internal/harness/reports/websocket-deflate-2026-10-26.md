# WebSocket compression: what Chrome 155, Firefox 158 and Safari 27/26 send, 2026-10-26

The `websocket` and `websocket-h2` pages, recaptured with new sockets
(`server.WebSocketSteps`), three samples per browser and scenario, Safari
26 on the second Mac. Messages are cut from `wsProse` (48 KiB of chat,
search, cart and presence events, one JSON object per line, from a fixed
seed) or are pseudo-random bytes (`wsNoise`):

- `so-deflate-sizes`: a 4096-byte message, two empty ones (text, binary),
  200 bytes repeating the first's text, every size from 6 to 40 bytes,
  then 48 … 4095 in growing steps, small repeats, incompressible 6, 64,
  1024 and 4096 bytes, the first message again;
- `so-deflate-nct`: the server answers `client_no_context_takeover`; two
  equal 4 KiB messages, small and incompressible ones, empty, and two
  messages that compress to exactly their own length (10 and 24 bytes);
- `so-deflate-w8` … `w15`: the server answers `client_max_window_bits=N`
  (Firefox and Safari did not offer it); repeats at distances that tell the
  windows apart.

Every finding holds in all 6 samples per browser (Safari 26 = Safari 27).

## Compressors

Byte for byte, every message of every sample:

| | Chrome | Firefox | Safari |
|---|---|---|---|
| Library | Chromium's zlib (`third_party/zlib` at 155.0.8059.85: zlib 1.3.2 with a 4-byte multiplicative hash, `ins_h = (v*66521+66521)>>16`) | zlib-rs 0.6.3 (vendored unmodified in Gecko, through libz-rs-sys's C API: `Cargo.lock`, `.cargo-checksum.json`) | zlib (1.3.1's deflate) |
| Level | 6 (7 fails from the first varied message) | 6 (`Z_DEFAULT_COMPRESSION`: zlib-rs's `deflate_medium`) | 6 (7 fails) |
| Detail that shows | — | Firefox feeds `deflate(Z_SYNC_FLUSH)` 4096-byte output buffers in a loop (`PMCECompression::Deflate`); a full buffer ends `deflate_medium`'s look-ahead, and a flush that fills one exactly gets a second empty block | — |

Found how: `strings` on Firefox's `XUL` shows `third_party/rust/zlib-rs/...`
paths and no zlib version string; a Rust program on `libz-rs-sys = "=0.6.3"`
with Firefox's parameters (`deflateInit2(-1, 8, -15, 8, 0)`) reproduced the
24 Firefox messages of the 2026-10-24 captures. Chrome's varied-text
messages matched no upstream zlib level; Chromium's own `third_party/zlib`
built with Chrome's arm64 defines reproduced them all. `internal/zdeflate`
ports all three; differential runs against the three reference builds
(about 200 000 messages each, every window 9–15, with and without resets)
were byte-identical.

## Which messages go uncompressed

| | Chrome | Firefox | Safari |
|---|---|---|---|
| Empty | compressed: RSV1, payload `00` | raw | raw (the context kept: the next message still matches the earlier text) |
| Compressed form longer than the message | compressed (6 → 8, 300 noise → 306, 4096 noise → 4102) | with context takeover compressed (as Chrome); with `client_no_context_takeover` raw (`WebSocketChannel.cpp`: `temp.Length() > mMsg.Length()`) | raw, in both modes |
| Compressed form equal | compressed | compressed (10 → 10) | raw (10, 24) |
| After a raw message | — | — | the compression context starts over: the last 4096-byte message compresses to 1173 bytes, as from an empty window (822 with the earlier text kept, 1173 with a reset; a restored snapshot fails) |

So Safari's old "5 bytes or less" was this rule: the 5-byte message of
`so-deflate` compresses to 7.

## Server parameters

| | Chrome | Firefox | Safari |
|---|---|---|---|
| `client_no_context_takeover` | each message on its own | same | same |
| `client_max_window_bits` 9–15 (unoffered for Firefox, Safari) | accepted, window used | accepted, window used | accepted, window used |
| `client_max_window_bits=8` | compresses with 9 bits (Chromium raises 8 to 9 before zlib) | **hangs**: zlib-rs refuses raw −8, and `HandleExtensions` calls `AbortSession` holding `mCompressorMutex`, which `DoStopSession` locks again; no request of the page went out afterwards, its 10 s step timer never reported (seen twice in exploratory runs; the final page skips this socket in Firefox) | `onopen`, then fails: no frame, RST_STREAM NO_ERROR at once (h2) or the TCP connection closed (HTTP/1.1); `onclose` 1006 |
| Page's `extensions` attribute | the server's answer | `permessage-deflate` alone | the server's answer |

## Framing

Firefox over HTTP/2 writes a frame's header in a DATA frame of its own when
the payload is longer than 1000 bytes (`kCopyBreak`), not only when it
exceeds a DATA frame: compressed messages of 1030 to 8107 bytes show it
(the 2026-10-24 captures had no payload between 1001 and 16000 bytes).

## What mimicry models

`websocket.compression`, `uncompressed`, `window_bits_8` (profiles),
`ws.Compressor`, Firefox's 1000-byte header rule, and
`client_max_window_bits` accepted unoffered. Firefox's hang is not
reproduced: mimicry fails the dial and ends the transport
([EVIDENCE.md, Not covered](../../../profiles/EVIDENCE.md#not-covered-by-any-claim)).
