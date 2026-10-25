# WebSockets

mimicry opens WebSockets as the profile's browser does, and frames every
message itself. See [decision 0025](../decisions/0025-websockets.md) and the
[capture report](../../harness/reports/websocket-2026-10-24.md).

```go
ws, resp, err := mimicry.DialWebSocket(ctx, transport, "wss://chat.example.com/socket",
	mimicry.WebSocketOptions{
		From:      page, // the page that opens it: Origin, cookies
		Protocols: []string{"chat"},
	})
if err != nil {
	return err // resp holds the server's answer if it refused
}
defer ws.Close(1000, "")
if err := ws.WriteMessage(ctx, mimicry.TextMessage, []byte("hello")); err != nil {
	return err
}
typ, msg, err := ws.ReadMessage(ctx)
```

## The handshake

- **Over HTTP/2 or HTTP/1.1.** If the pooled HTTP/2 connection to the
  host allows extended CONNECT (the server sent
  `SETTINGS_ENABLE_CONNECT_PROTOCOL=1`), the socket is a stream on it.
  Otherwise it gets a new HTTP/1.1 connection of its own, never pooled,
  whose ClientHello is the profile's `tls.websocket_h1` variant.
- **Headers.** The `websocket` request kind gives the list: order,
  defaults and casing, with `Sec-WebSocket-Key`, `-Protocol` and
  `-Extensions` filled per socket. `Origin` is always sent.
- **Cookies.** The jar's rules for a credentialed request from `From`.
  Safari 26 also sends a cross-site host's unpartitioned Lax and None
  cookies.
- **Refused before connecting:** `ws://` from an `https://` page, and
  WebSockets through a proxy (not measured).

## Messages

- Every message is one frame: masked with a fresh random key, with the
  shortest length form.
- **Compression** (permessage-deflate), when the server accepts it:
  - Chrome compresses every message.
  - Safari leaves messages of 5 bytes or less uncompressed, and out of
    the compression context.
  - Both match zlib level 6 byte for byte (`internal/zdeflate`).
  - With a Firefox profile, sending on a compressed socket is
    `ErrNotMeasured`: Firefox's compressor is not reproduced yet. So is a
    Safari message between 6 and 4095 bytes.
- Pings are answered at once.
- `ReadMessage` returns a `*CloseError` once the close handshake is done.

## Closing

`Close` echoes the browser's close handshake, then ends the transport as
the browser does:
- **Chrome:** closes the HTTP/1.1 connection 2 s later; over HTTP/2,
  END_STREAM on the close frame.
- **Safari:** closes the connection, or resets the stream, at once.
- **Firefox:** leaves it to the server, as the captures show. mimicry
  gives up after 10 s.

## What checks it

`TestWebSocketsMatchBrowser` replays every socket of both capture pages
and compares them with every browser sample:
- transport and TLS hello;
- handshake lines or fields;
- every frame, including the compressed bytes;
- pongs and HTTP/2 DATA packing;
- how the transport ends.

`TestDeflateReproducesBrowserMessages` checks the compressor against
every captured compressed message.
