# mimicry-core's messages

The schema of the messages between `mimicry-core` and the language SDKs
([decision 0069](../docs/decisions/0069-language-bindings.md)):
[`mimicry/v1/mimicry.proto`](mimicry/v1/mimicry.proto), the payloads of the
wire protocol's CALL, REPLY and EVENT frames. The frames, streams, credit
and RESET are in [`testdata/wire/`](testdata/wire/README.md); this is what
the layer above them does.

- Go: `internal/rpc/pb`, generated and checked in. After editing the
  schema, `moon run mimicry:proto` lints it and regenerates (buf and
  protoc-gen-go pinned); `TestGenerated` fails while the two differ.
- Python and TypeScript generate their own code from the same file in
  their SDKs (buf).
- Names mirror Go's public API: `Config`'s fields, `Kind` values, `Options`
  (Go's `mimicry.Request`; Go's `From` is `page`), the error types. An
  enum's zero value is Go's zero value of that type (`KIND_NAVIGATE`), so
  buf's `ENUM_ZERO_VALUE_SUFFIX` rule is off.

## A connection

1. The SDK's first call is `Hello`: protocol major and minor, its version,
   feature bits. The core answers `HelloReply` with its own; another major
   version is refused (`CODE_INVALID`, naming both) and the core closes the
   connection. Protocol 1.0 defines no feature bits.
2. Every call is a stream: CALL with a `Call` (one method), REPLY with a
   `Reply` (`error`, a `result`, both, or neither for success with nothing
   to return). A call with no body sets END on its CALL; a reply with no
   body sets END on its REPLY.
3. The objects a call creates (client, transport, jar, ticket cache, Alt-Svc
   cache, client hint store, parsed profile, WebSocket) have ids, owned by
   the connection; `Close{id}` frees one, and disconnecting frees them all.
   Ids are never reused within a connection.
4. Closing the connection (EOF) makes `mimicry-core` exit.

### Messages larger than a frame

A payload is at most 64 KiB. A `Call`, `Reply` or `Event` that is larger is
sent as one with only `continued` set to its length; the message itself is
then the first `continued` bytes of the stream's DATA, and any body follows
it in the same DATA. (Large root certificate bundles, many cookies, long
certificate chains.)

### Bodies

| Call | DATA from the SDK | DATA from the core |
|---|---|---|
| `Do` | the body when `request.stream_body` | the response body, after `Response` |
| `DialWebSocket` | messages (below) | messages; on a failed handshake, the response's body |
| `ParseProfile` | the profile's JSON | |
| `ProfileSpec` | | the profile's JSON (`profile.Encode`) |
| `ReadJSON`, `ReadNetscape` | the saved state | |
| `WriteJSON`, `WriteNetscape` | | the state |

A request's body can also go whole in `request.body`, which the core can
send again for a redirect that keeps it (`GetBody`); a streamed body
cannot: such a redirect goes as it goes in Go for a request without
`GetBody` (mimicry refuses a 301 or 302 that keeps the body; `http.Client`
returns a 307 or 308 instead of following it).

A RESET of a call's stream cancels it: a request's context ends. When a
response body fails after its REPLY was sent, the core first sends a
`StreamError` notice naming the stream and the error, then resets the
stream; the SDK raises that error from the body's read.

### WebSockets

`DialWebSocket`'s stream stays open after its REPLY, and its DATA carries
the socket's messages both ways as records, independent of DATA frames:

```
record = type u8 | length u32 (big-endian) | payload
type   = 1 text | 2 binary | 0 end (core only)
```

- The core sends each message the server sent; when the socket has closed,
  one `end` record whose payload is the `Error` reading would return from
  then on (`CODE_CLOSE` with the close code and reason after a close
  handshake), then END.
- The SDK sends each message to write. When the core's `WriteMessage`
  returns, it sends a `WebSocketWritten` notice with the message's sequence
  number (the n-th record the SDK sent on this socket, from 1) and its
  error, if any: a write's result never waits behind messages the SDK has
  not read. The SDK's END means it writes no more; the socket stays open.
- `CloseWebSocket{websocket, code, reason}` is a call of its own, answered
  once the close handshake is done. A RESET of the socket's stream, or
  `Close` of its id, closes it as `Close(1000, "")` does (the public API
  has no abort).

### Events

An EVENT on stream 0 is a notice nothing answers: `SetCookie` (a jar made
with `on_set_cookie`), `StreamError`, `WebSocketWritten`. The SDK's reader
must hand a notice off and return without waiting on the connection
(`wire.Config.OnEvent`).

An EVENT opening a core stream (even id) is a reverse call:
`VerifyConnection` (a client made with `config.verify_connection`) carries
the connection's state; the SDK answers with a REPLY holding an
`EventReply`, no error to accept. The handshake waits for the answer, at
most the handshake's `DialTimeout` and only while a request of that client
still waits (each bounded by its own deadline); a RESET or a disconnect
fails it.

### Errors

`Error` is a Go error: `code`, the most specific of the codes its chain
matches; `matches`, every one of them (a proxy failure at its deadline is
`CODE_PROXY` and `CODE_DEADLINE`); `message`; and the fields of its typed
errors (`cors`, `proxy`, `pin`, `close`, `validation`). Each SDK has one
exception class per code; `matches` lets one error be caught as several.

### Strings and bytes

Protobuf strings must be UTF-8. Values that cross byte for byte and that
HTTP allows to be other bytes are `bytes`: header values, cookie names,
values and raw lines, Set-Cookie lines, Cookie header strings. Other text
(messages, statuses, reasons, cookie paths) is made valid UTF-8 by the
core, invalid bytes replaced with U+FFFD.
