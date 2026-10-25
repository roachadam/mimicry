# 0025: WebSockets as each browser opens and frames them

- Status: Accepted
- Date: 2026-10-24
- Commits: (the WebSocket commit)

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
([report](../../harness/reports/websocket-2026-10-24.md)), all four
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
  `WebSocket` does (the user's choice).

## Decision

The API is:
- `DialWebSocket(ctx, t, url, WebSocketOptions{From, Protocols, Header})`
  returns a `*WebSocket` and the handshake response;
- the `*WebSocket` has `ReadMessage`, `WriteMessage`, `Close`, `Protocol`
  and `Extensions`;
- mimicry masks, compresses, answers pings and runs the close handshake.

Modelling:
- The handshake is the `websocket` request kind, derived by kindsgen.
  Priority is the kind's weight. Its only variant is non-secure `ws://`.
  Extensions is a slot whose value comes from the `websocket` section.
- `:protocol` follows `pseudo_header_order`, a fixed rule that kindsgen
  checks.
- The HTTP/1.1 socket's hello is `tls.websocket_h1` (`alpn`, `drop`),
  derived by hellogen.
- Profile `websocket` holds the rest:
  - `extensions` and `compression` (`zlib6`, or empty for not measured);
  - `uncompressed_max` / `compress_min`: sizes between them are not
    measured;
  - `h2_data` (`together`, `header_alone`, `split_control`), with DATA
    capped by `http2.max_data_frame`;
  - `close_h1` / `close_h2`.
- Cookies: the jar treats `ws`/`wss` as `http`/`https`. Safari 26 also
  sends unpartitioned Lax and None cookies cross-site
  (`cookies.websocket_unpartitioned`).
- Compression is a pure-Go port of zlib's deflate (`internal/zdeflate`):
  `compress/flate` is not byte-identical to zlib.
- These are `ErrNotMeasured`:
  - sending over a deflate socket with a Firefox profile;
  - a Safari message between 6 and 4095 bytes when compressed;
  - a server asking for a smaller window or no context takeover;
  - subprotocols on a non-secure socket;
  - WebSockets through a proxy.
- A `ws://` socket from an `https://` page is refused before anything is
  sent, as every browser refuses it.

## Consequences

- Sockets look like the browser's in every measured respect.
- Callers never write frames themselves.
- Chrome's occasional split of a 70 KB message is not reproduced; one
  frame is also a form Chrome sends.

## Revisit when

- Firefox's compressor is identified, or its transport end is captured.
- A capture with varied text tells zlib level 6 from 7 (the regular
  test text matches both).
- Safari's threshold between 6 and 4095 bytes is measured.
- Partitioning by top-level site lands (0011): cross-site sockets would
  move with it.
