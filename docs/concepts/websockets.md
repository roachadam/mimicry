# WebSockets

mimicry opens WebSockets as the profile's browser does, and frames every
message itself. See [decision 0025](../decisions/0025-websockets.md) and the
[capture report](../../harness/reports/websocket-2026-10-24.md).

```go
ws, resp, err := transport.DialWebSocket(ctx, "wss://chat.example.com/socket",
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
- **Refused before connecting:** `ws://` from an `https://` page.

## Through a proxy

With `Config.Proxy`, a socket goes as the profile's browser takes one
through a proxy (the [capture report](../../harness/reports/proxy-websocket-2026-10-26.md)):
- **wss://, extended CONNECT allowed:** a stream on the host's HTTP/2
  connection, the one inside the tunnel the page's requests used; nothing
  new reaches the proxy.
- **wss://, otherwise:** a tunnel of its own (the browser's CONNECT
  lines or SOCKS5 bytes, as for any request), then the socket hello.
  Firefox's socket connections through an HTTP proxy never resume a
  session, as none of its connections through one do; through SOCKS5
  they resume.
- **ws:// through an HTTP proxy:** Chrome and Firefox open a CONNECT
  tunnel to the host and port; Safari sends the handshake to the proxy
  whole, on a connection of its own, as `GET ws://host:port/path
  HTTP/1.1` with its usual header lines.
- **Credentials:** socket tunnels give them as the browser's other
  tunnels do: Chrome and Firefox unasked once the proxy has asked,
  Safari after a 407 on every tunnel. Safari's `ws://` handshake sent
  whole carries them unasked, after `Accept-Encoding`, as its other
  plain-HTTP requests do. Safari 26 copies Safari 27 (not captured).
- **Errors:** a proxy that fails the socket is a `*ProxyError`;
  `Config.Pins` and `VerifyConnection` check the server's TLS inside the
  tunnel.
- **Not measured (`ErrNotMeasured`):** `ws://` through SOCKS5.

## Messages

- Every message is one frame: masked with a fresh random key, with the
  shortest length form.
- **Compression** (permessage-deflate), when the server accepts it, is
  the browser's own deflate library at level 6, byte for byte
  (`internal/zdeflate`):
  - Chrome: Chromium's fork of zlib, whose hash finds other matches than
    zlib's;
  - Safari: zlib;
  - Firefox: zlib-rs, a Rust port of zlib-ng (its own match search, and
    the 4096-byte output buffer Firefox feeds it, both show in the
    bytes).
- **Which messages go uncompressed:**
  - Chrome compresses every one; an empty message is the single byte
    `00`.
  - Safari sends a message uncompressed when compressing does not make
    it shorter (an empty one, a few bytes, incompressible data), and then
    starts its compression context over.
  - Firefox sends an empty message uncompressed, and, when the server
    asks for `client_no_context_takeover`, one that compresses longer.
- **The server's parameters:** `client_no_context_takeover` compresses
  each message on its own; `client_max_window_bits` is accepted from 9
  to 15 whether or not it was offered (Firefox and Safari offer none), as
  every browser accepts it. At 8, which zlib cannot use, Chrome
  compresses with 9 bits and Safari fails the socket once open (no
  frame, the transport ended at once); `DialWebSocket` then returns an
  error. Firefox hangs on it (see
  [the report](../../harness/reports/websocket-deflate-2026-10-26.md));
  mimicry fails the dial as for Safari.
- Pings are answered at once.
- `ReadMessage` returns a `*CloseError` once the close handshake is done.

## Closing

`Close` echoes the browser's close handshake, then ends the transport as
the browser does:
- **Chrome:** closes the HTTP/1.1 connection 2 s later; over HTTP/2,
  END_STREAM on the close frame, and `Close` returns once the server has
  ended the stream too, as Chrome's close event waits for it: until then
  the stream is open, and Chrome would chain a new request's priority on
  it. mimicry resets a stream the server leaves open after 10 s.
- **Safari:** closes the connection, or resets the stream, at once.
- **Firefox:** leaves it to the server, as the captures show. mimicry
  gives up after 10 s.

## What checks it

`TestWebSocketsMatchBrowser` replays every socket of both capture pages,
and of the three proxy pages through the capture proxy, and compares
them with every browser sample:
- transport and TLS hello;
- what reached the proxy for it (CONNECT, SOCKS5, or the handshake sent
  whole);
- handshake lines or fields;
- every frame, including the compressed bytes;
- pongs and HTTP/2 DATA packing;
- how the transport ends.

`TestDeflateReproducesBrowserMessages` replays every captured message
through the profile's compressor and names the first byte that differs.
