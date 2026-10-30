# 0025: WebSockets as each browser opens and frames them

- Status: Accepted; amended 2026-10-26 (`windowBits8`; WebSockets through proxies measured)
- Date: 2026-10-24
- Commits: `a3b014e`; amendments `0a4a442`, `b9a697d`

## Problem

A WebSocket is a handshake (HTTP/1.1 Upgrade, or an HTTP/2 extended
CONNECT, RFC 8441) followed by frames. Both are fingerprints:
- the handshake's headers, order, cookies and TLS hello;
- the frames' masking, lengths and compression;
- how the frames are packed into HTTP/2 DATA;
- how the socket closes.

A library that hands the caller a byte stream leaves framing to someone
else and gets every frame wrong.

## Evidence

The `websocket` and `websocket-h2` captures
([report](../../internal/harness/reports/websocket-2026-10-24.md)), all four
browsers, three samples each:
- **Transport:** every browser uses extended CONNECT on the page's
  existing HTTP/2 connection when the server allows it. Otherwise each
  socket gets its own new HTTP/1.1-only TLS connection, with ALPN
  `http/1.1` (Chrome also drops ALPS; Firefox drops `compress_certificate`).
- **Handshake:** each browser has a fixed list of its own, unlike its
  `fetch()` list. `:protocol` comes after the browser's usual
  pseudo-headers.
- **Frames:** always masked with a fresh key, the shortest length form,
  one frame per message.
- **Compression:** Chrome and Safari compress byte-identically to zlib
  level 6, sharing context between messages. Safari leaves a 5-byte
  message uncompressed and out of the context. Firefox's output matches
  no zlib setting.
- **Packing and close:** each browser packs DATA its own way and closes
  its own way. Firefox's end of the transport was not seen.

## Options

- **A byte stream** (`net/http`'s 101 body, a `net.Conn`). The caller
  would frame, so the frames would not be the browser's.
- **An adapter to an existing Go WebSocket library.** Its API, and our
  framing behind it: more surface, someone else's semantics.
- **A message-level connection** that owns framing, as the JavaScript
  `WebSocket` does (chosen; the maintainer's decision).

## Decision

The API is:
- `(*Transport).DialWebSocket(ctx, url, WebSocketOptions{From, Protocols, Header})`
  returns a `*WebSocket` and the handshake response (a package function
  taking the Transport until 2026-10-26; a method reads as the request
  path it is);
- the `*WebSocket` has `ReadMessage`, `WriteMessage`, `Close`, `Protocol`
  and `Extensions`;
- mimicry masks, compresses, answers pings and runs the close handshake.

Modelling:
- The handshake is the `websocket` request kind, derived by kindsgen.
  Priority is the kind's weight. Its only variant is non-secure `ws://`.
  Extensions is a slot whose value comes from the `websocket` section.
- `:protocol` follows `pseudoHeaderOrder`, a fixed rule that kindsgen
  checks.
- The HTTP/1.1 socket's hello is `tls.websocketH1` (`alpn`, `drop`),
  derived by hellogen.
- Profile `websocket` holds the rest:
  - `extensions`, and `compression` (the browser's deflate library at
    level 6: `chromium_zlib6`, `zlib6`, `zlib_rs6`), `uncompressed`
    (which messages go raw) and `windowBits8` (amended 2026-10-26,
    [websocket-deflate report](../../internal/harness/reports/websocket-deflate-2026-10-26.md));
  - `h2Data` (`together`, `header_alone`, `split_control`), with DATA
    capped by `http2.maxDataFrame`;
  - `closeH1` / `closeH2`.
- Cookies: the jar treats `ws`/`wss` as `http`/`https`. Safari 26 also
  sends unpartitioned Lax and None cookies cross-site
  (`cookies.websocketUnpartitioned`).
- Compression is a pure-Go port of each browser's library
  (`internal/zdeflate`: zlib, Chromium's zlib, zlib-rs): `compress/flate`
  is not byte-identical to any of them.
- These are `ErrNotMeasured`:
  - subprotocols on a non-secure socket;
  - WebSockets through a proxy where not captured (amended 2026-10-26:
    the [proxy-websocket captures](../../internal/harness/reports/proxy-websocket-2026-10-26.md)
    measured the rest, modelled as profile `proxy.websocket`, see
    [0014](0014-proxies.md); Safari 27's with proxy credentials the same
    day, Safari 26's copied from it): `ws://` through SOCKS5.
- A `ws://` socket from an `https://` page is refused before anything is
  sent, as every browser refuses it.

## Consequences

- Sockets look like the browser's in every measured respect.
- Callers never write frames themselves.
- Chrome's occasional split of a 70 KB message is not reproduced; one
  frame is also a form Chrome sends.

## Revisit when

- Firefox's transport end is captured.
- Partitioning by top-level site lands (0011): cross-site sockets would
  move with it.
