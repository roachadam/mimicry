# WebSockets

mimicry opens WebSockets the way the browser does, and frames every
message itself.

```go
ws, resp, err := transport.DialWebSocket(ctx, "wss://www.shop.example/socket",
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

- **HTTP/2 or HTTP/1.1.** If the server allows WebSockets over its HTTP/2
  connection (extended CONNECT), the socket is a stream on it. Otherwise
  the socket gets its own HTTP/1.1 connection, never pooled. That
  connection uses a separate ClientHello the browser uses for sockets.
- **Headers.** Order, defaults and casing follow the browser. The
  `Sec-WebSocket-*` headers are filled per socket. `Origin` is always
  sent.
- **Cookies.** The jar's rules for a credentialed request from the page
  in `From`. Safari 26 also sends a cross-site host's unpartitioned Lax
  and None cookies.
- **Refused before connecting:** `ws://` from an `https://` page.

## Through a proxy

With `Config.Proxy`, a socket goes through the proxy as the browser's
does:

- **`wss://`, server allows HTTP/2 sockets:** a stream on the host's
  HTTP/2 connection, inside the tunnel the page already uses. Nothing new
  reaches the proxy.
- **`wss://`, otherwise:** a tunnel of its own, opened like any request's,
  then the socket's handshake.
- **`ws://` through an HTTP proxy:** Chrome and Firefox open a CONNECT
  tunnel. Safari sends the handshake to the proxy whole, as
  `GET ws://host:port/path HTTP/1.1`.
- **`ws://` through SOCKS5:** not supported; the dial returns
  `ErrNotMeasured`.
- **Credentials:** sent as for other requests (see
  [Proxies](proxies.md#credentials)). Safari's whole `ws://` handshake
  always carries them unasked.
- **Errors:** a proxy that fails the socket returns a `*ProxyError`.
  `Config.Pins` and `VerifyConnection` check the server's TLS inside the
  tunnel.

## Messages

- Every message is one frame, masked with a fresh random key.
- **Compression** (permessage-deflate), when the server accepts it, uses
  each browser's own deflate library, so the bytes match.
- **Uncompressed messages:**
  - Chrome compresses every message. An empty one is the single byte `00`.
  - Safari sends a message uncompressed when compressing would not shrink
    it, then restarts compression.
  - Firefox sends an empty message uncompressed. If the server asks for
    `client_no_context_takeover`, it also skips messages that would grow.
- **Server parameters:** `client_no_context_takeover` compresses each
  message on its own. `client_max_window_bits` from 9 to 15 is accepted,
  even if not offered, as browsers do. At 8, Chrome compresses with 9
  bits; Safari and mimicry fail the socket, and `DialWebSocket` returns an
  error.
- Pings are answered at once.
- `ReadMessage` returns a `*CloseError` once the close handshake is done.

## Closing

`Close` sends the browser's close handshake, then ends the connection as
the browser does:

- **Chrome:** over HTTP/1.1, closes the connection 2 s later. Over HTTP/2,
  `Close` waits for the server to end the stream, as Chrome does. mimicry
  resets the stream if the server leaves it open for 10 s.
- **Safari:** closes the connection, or resets the stream, at once.
- **Firefox:** waits for the server to close. mimicry gives up after 10 s.

## Limits

- `ws://` through a SOCKS5 proxy is refused (`ErrNotMeasured`).
- `WebSocketOptions.Protocols` is refused (`ErrNotMeasured`) where the
  profile's handshake list has no place for `Sec-WebSocket-Protocol`, such
  as `ws://` from an `http://` page.
- Reads are bounded at 64 MiB per message and 64 queued messages: safety
  bounds, not browser values.

Background: decisions [0025](../decisions/0025-websockets.md) and
[0039](../decisions/0039-proxy-credentials-up-front.md).
