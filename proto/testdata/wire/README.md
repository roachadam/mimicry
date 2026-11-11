# Wire protocol test vectors

The framed protocol between `mimicry-core` and the SDKs
([decision 0069](../../../docs/decisions/0069-language-bindings.md)), as
`internal/rpc/wire` implements it, and the vectors every implementation
replays: Go's (`TestVectors`), Python's and Node's.

The files are generated from the table in
`internal/rpc/wire/vectors_test.go`: edit the table, then run
`go test ./internal/rpc/wire -run TestVectorFiles -update`.
`TestVectorFiles` fails while the files differ from the table.

## The protocol

### Frames

```
frame   = header payload
header  = stream u32 | type u8 | flags u8 | length u32     (big-endian, 10 bytes)
payload = length bytes, at most 65,536
```

| Type | Value | Payload |
|---|---|---|
| CALL | 1 | opaque: the call |
| REPLY | 2 | opaque: its result |
| DATA | 3 | bytes of a body or a message |
| WINDOW | 4 | u32 credit increment, 1 to 2^31−1 |
| RESET | 5 | u32 code |
| EVENT | 6 | opaque |

The one flag is END (`0x01`): the last frame of its direction of the
stream. It may be set on CALL, REPLY, DATA and an EVENT that opens a
stream, never on WINDOW, RESET or a stream-0 EVENT.

A receiver checks a header in this order before reading its payload: the
type (`UNKNOWN_TYPE`), the flags (`UNKNOWN_FLAGS`), the length
(`FRAME_TOO_LARGE`).

### Sides and streams

- The SDK opens streams with CALL on odd ids; the core opens streams with
  EVENT on even ids (reverse calls such as `VerifyConnection`). A CALL from
  the core or an EVENT from the SDK is `BAD_FRAME`; a stream opened on the
  other side's parity is `WRONG_PARITY`.
- Each side's ids increase; gaps are allowed. An opening frame on an id at
  or below the highest that side opened is `STREAM_CLOSED`.
- Stream 0 is the connection. It carries only EVENT from the core (a
  notice; nothing answers it) and RESET (below); anything else is
  `BAD_FRAME`.
- Each side has at most `maxStreams` of its own streams open at once (by
  default 256; both sides use the same value). The opener waits for one
  to close; a peer that opens one more is `STREAM_LIMIT`. A stream counts
  until it is closed and any RESET its side sent on it has been written,
  so a side never queues more RESETs than the streams it allows open.
  Because frames arrive in order, the opener's count of its open streams
  is never below the receiver's.

### A stream

- The opener sends its opening frame (END if no body follows), then DATA,
  the last with END. It may send DATA before the REPLY arrives.
- The responder sends one REPLY (END if no body follows), then DATA, the
  last with END. DATA before the REPLY, a REPLY from the opener and a
  second REPLY are `BAD_FRAME`.
- REPLY or DATA in a direction after its END is `STREAM_CLOSED`.
- A stream is closed once each side has ended its direction with END, or
  as a reset below says. On a closed stream, WINDOW and RESET are ignored
  (they may have been in flight); any other frame is `STREAM_CLOSED`. Any
  frame on an id above the highest its side opened is `STREAM_NOT_OPEN`.

### Credit

- Each direction of each stream starts with `window` bytes of credit (by
  default 262,144; both sides use the same value). DATA payloads spend it;
  a sender with none left waits.
- The receiver grants more with WINDOW. One of 0, above 2^31−1, without a
  4-byte payload or with END is `BAD_FRAME`, and so is one taking the
  credit above 2^31−1. A WINDOW that arrives after its receiver ended its
  own DATA is ignored.
- When to grant (the vectors depend on it): once the bytes the application
  has read since the last grant reach half the window, rounded up, send
  them all in one WINDOW; never after END in that direction.
- DATA beyond the credit is dropped and the stream is reset with
  `CREDIT_OVERRUN`; if that frame carries END, the END still ends the
  peer's direction, so the stream is closed at once (see Reset). The
  connection goes on.

### Reset

- RESET on a stream cancels it, from either side.
- The side that sends it stops sending on the stream and drops the peer's
  frames on it (they were in flight) until a frame with END or a RESET
  arrives; then the stream is closed. If the peer's END had already
  arrived, it is closed at once.
- The side that receives it fails the stream with the peer's code, answers
  with a RESET (CANCEL) unless it had sent END, and the stream is closed.
- RESET on stream 0 aborts the connection. An endpoint that finds a
  protocol error sends RESET 0 with its code, then closes the connection;
  one that receives RESET 0 fails with the peer's code and sends nothing.
  A peer that stopped reading cannot hold a failed connection open: the
  endpoint gives its last writes a bounded time (Go: one second), then
  closes the connection unwritten.
  A stream whose credit was overrun is the one protocol error that resets
  only its stream.

### Codes and errors

| Code | Name | |
|---|---|---|
| 0 | `CANCEL` | the stream's caller gave up |
| 1 | `STREAM_LIMIT` | more than `maxStreams` open |
| 2 | `CREDIT_OVERRUN` | DATA beyond the credit (resets the stream) |
| 3 | `FRAME_TOO_LARGE` | length above 65,536 |
| 4 | `UNKNOWN_TYPE` | type 0, or above 6 |
| 5 | `UNKNOWN_FLAGS` | a flag bit other than END |
| 6 | `STREAM_CLOSED` | a frame on a closed stream or ended direction, or an id reused |
| 7 | `STREAM_NOT_OPEN` | a frame on a stream never opened |
| 8 | `WRONG_PARITY` | a stream opened with the other side's parity |
| 9 | `BAD_FRAME` | a frame the protocol does not allow there |

A RESET may carry any code. One the receiver does not know still resets
the stream, failing it with that code. Two errors are never sent:
`TRUNCATED`, the connection ending inside a frame, and `CONN_CLOSED`, it
ending between frames or being closed. Either fails every open stream.

### Precedence

When a frame breaks several rules, the first that applies, in this order,
names the error:

1. The header: type (`UNKNOWN_TYPE`), flags (`UNKNOWN_FLAGS`), length
   (`FRAME_TOO_LARGE`).
2. The frame's shape, whatever the stream's state: a type the sender's
   side never sends (CALL from the core, EVENT from the SDK); on stream 0,
   a type other than EVENT or RESET, or END; a WINDOW or RESET with END or
   without a 4-byte payload, or a WINDOW of 0 or above 2^31−1 (all
   `BAD_FRAME`); an opening frame on the other side's parity
   (`WRONG_PARITY`).
3. The stream's state: an opening frame on an id already used
   (`STREAM_CLOSED`), then one past `maxStreams` (`STREAM_LIMIT`); any
   other frame on an id never opened (`STREAM_NOT_OPEN`); on a closed
   stream, WINDOW and RESET ignored, anything else `STREAM_CLOSED`; on a
   stream this side reset, everything dropped (Reset); REPLY or DATA in a
   direction after its END (`STREAM_CLOSED`); a WINDOW after its receiver's
   own END, ignored.
4. The order of a stream's frames: a REPLY from the opener, a second
   REPLY, DATA before the REPLY (all `BAD_FRAME`).
5. Credit: DATA beyond it (`CREDIT_OVERRUN`, the stream reset), a WINDOW
   taking it past 2^31−1 (`BAD_FRAME`).

## The vectors

`manifest.json`:

```json
{
  "version": 1,
  "window": 8,
  "maxStreams": 2,
  "vectors": [
    {
      "name": "response-body",
      "description": "…",
      "role": "sdk",
      "file": "response-body.bin",
      "frames": [
        {"dir":"out","size":13,"stream":1,"type":"CALL","end":true,"payload":"676574"},
        {"dir":"in","size":14,"stream":1,"type":"DATA","payload":"61626364"},
        {"dir":"out","size":14,"stream":1,"type":"WINDOW","payload":"00000004","auto":true}
      ]
    }
  ]
}
```

- `window` and `maxStreams` are what both sides use in every vector.
- `role` is the side under test, `sdk` or `core`; the harness plays the
  other.
- `file` holds every frame of the conversation, both directions, in order.
  Each frame's `size` is its length in the file, so the file splits without
  parsing.
- A frame: `dir` is `in` (the harness sends it to the endpoint) or `out`
  (the endpoint sends it). A well-formed frame has `stream`, `type`, `end`
  (absent: false) and `payload` (hex; absent: empty). Malformed input has a
  `note` instead.
- `out` frames: the harness makes the endpoint send it (Actions below),
  unless `auto`, which the endpoint sends by itself because of what came
  before (a WINDOW after reads, a RESET answering one, a reset after an
  error).
- `in` frames, `expect`:
  - absent: delivered (Delivery below);
  - `ignore`: nothing to observe;
  - `stream-error`: the frame's stream fails with `error`, raised by the
    endpoint itself; the connection goes on;
  - `conn-error`: the connection fails with `error`; with `remote`, it is
    the peer's code from a RESET on stream 0. Always the last `in` frame.

### Replaying a vector

1. Make an endpoint of `role` with the manifest's `window` and
   `maxStreams`, on a connection whose other end the harness holds.
2. For each frame, in order:
   - `out`, not `auto`: perform its action, then read `size` bytes from
     the endpoint; they must equal the frame.
   - `out`, `auto`: read `size` bytes; they must equal the frame.
   - `in`: write its bytes. For `conn-error`, close the harness's sending
     side at once (a truncated frame needs it); otherwise check `expect`.
3. Close the sending side if it is still open. The endpoint must fail with
   the vector's `conn-error`, or `CONN_CLOSED` if none, then close the
   connection, having sent nothing after the last `out` frame.

Every wait needs a deadline: a vector that hangs fails.

### Actions

| `out` frame | Action |
|---|---|
| CALL (sdk), EVENT on a stream other than 0 (core) | open a stream with the payload, ending it if `end`; its id must be the frame's |
| EVENT on stream 0 (core) | send a notice |
| REPLY | reply on the stream, ending it if `end` |
| DATA with a payload | write it (the credit always covers it) |
| DATA with `end`, empty | end the stream's direction |
| RESET | reset the stream with the payload's code (CANCEL: close it) |

### Delivery

| `in` frame | The endpoint |
|---|---|
| CALL (core), EVENT on a stream other than 0 (sdk) | accepts a stream with that id and payload |
| EVENT on stream 0 (sdk) | delivers a notice with the payload |
| REPLY | gives the payload as the stream's reply |
| DATA | gives the payload when the stream is read: read exactly its length before the next frame, since WINDOW frames follow from what was read |
| WINDOW | nothing to observe |
| RESET | fails the stream with the peer's code, known or not |

After a CALL, EVENT, REPLY or DATA with `end`, the next read of the
stream ends (EOF).
