# mimicry for Node

A browser's requests from Node, byte for byte: [mimicry](https://github.com/roachadam/mimicry)'s
Go client, reached through `mimicry-core`, a static binary this package
starts as a child of your process ([decision 0069](../../docs/decisions/0069-language-bindings.md)).
The names are Go's, in Node's casing.

> The package name `mimicry-http` is provisional; it is decided with the
> Python SDK's and written only in `package.json`.

```js
import { newClient, asKind, Kind } from "mimicry-http";

const client = await newClient({ profile: "chrome-157-macos" });

const page = await client.do({ url: "https://example.com/" });   // a typed navigation
console.log(page.statusCode, await page.text());

const api = await client.do(asKind({ url: "https://example.com/api" }, Kind.Fetch));
console.log(await api.json());

await client.close();
```

- Node 22.12 or later (the current LTS lines: 22, 24, 26). ES modules with
  types; `require("mimicry-http")` works too, through Node's `require(esm)`.
- One dependency, `@bufbuild/protobuf`, plus the core for your platform,
  an optional dependency (`mimicry-http-<platform>-<arch>`: macOS, Linux
  and Windows, x64 and arm64; one static Linux build serves glibc and
  musl). No install scripts and nothing downloaded at run time.

## From Go to Node

| Go | Node |
|---|---|
| `mimicry.NewClient(cfg)` | `await newClient(config)` |
| `mimicry.NewTransport(cfg)` | `await newTransport(config)` |
| `client.Do(req)` | `await client.do(req)` |
| `transport.RoundTrip(req)` | `await transport.roundTrip(req)` |
| `Config{DialTimeout, DisableHTTP3, MaxConnsPerHost, HTTP3OverSOCKS5, …}` | `{dialTimeout, disableHTTP3, maxConnsPerHost, http3OverSOCKS5, …}` (durations in ms) |
| `mimicry.Navigate`, `Fetch`, `XHR`, … | `Kind.Navigate`, `Kind.Fetch`, `Kind.XHR`, … |
| `mimicry.As(req, k)` | `asKind(req, k)` |
| `mimicry.With(req, mimicry.Request{From: page, …})` | `withOptions(req, {page, …})` |
| `mimicry.Exact(req)`, `HeaderOrderKey` | `exact(req)`, `req.headerOrder` |
| `transport.Plan(req)` | `await client.plan(req)` / `transport.plan(req)` |
| `transport.DialWebSocket(ctx, url, WebSocketOptions{From, …})` | `await client.dialWebSocket(url, {page, protocols, header})` |
| `ws.ReadMessage`, `WriteMessage`, `Close(code, reason)` | `readMessage()`, `writeMessage(type, data)`, `close(code, reason)` |
| `Jar`, `TicketCache`, `AltSvcCache` `.WriteJSON(w)` / `.ReadJSON(r)` | `.writeJSON(pathOrStream)` / `.readJSON(pathOrBytesOrStream)` |
| `context.Context` | `signal: AbortSignal` |
| `ErrNotMeasured`, `*CORSError`, `*ProxyError`, … | `NotMeasuredError`, `CORSError`, `ProxyError`, … |

A request is a plain object: `{method, url, header, body, contentLength,
signal, proxy}`; `asKind`, `withOptions` and `exact` return a copy with
`options`/`exact` set, as Go's return a shallow copy. A `Header` is Go's
`http.Header` (canonical names, every value); header values are
ByteStrings, one byte per character, so bytes that are not UTF-8 cross
intact.

Callbacks are data (a proxy URL, a DNS server, certificate files, a key
log path), except `verifyConnection`, an async function the core asks
before using each connection:

```js
const client = await newClient({
  profile: "firefox-159-windows",
  proxy: "http://user:pass@proxy:3128",
  verifyConnection: async (state) => {
    if (state.peerCertificates[0].issuer.includes("Evil CA")) {
      throw new Error("not this issuer");   // the request fails with VerifyError
    }
  },
});
```

## Bodies and cancellation

A response's `body` is a WHATWG `ReadableStream<Uint8Array>`, read as you
consume it: the core sends at most 256 KiB ahead of your reader, so a
body you do not read holds the server back instead of filling memory.
`bytes()`, `text()`, `json()` and `arrayBuffer()` read it whole. Read it
to its end or `cancel()` it; one that is garbage collected is cancelled.

A request body is a string, bytes, a `Blob` (sent whole; it can be sent
again for a redirect that keeps it) or a `ReadableStream` / async iterable
(streamed; set `contentLength` if you know it).

`signal` cancels the request at any point, its body included: the core
resets the request as Go's context would, and the call rejects with the
signal's reason (`AbortError`, or `TimeoutError` for
`AbortSignal.timeout(ms)`).

## Errors

Every error the core returns is a `MimicryError` with `code` (the most
specific) and `matches` (every code its Go error chain matches). Each code
has a class, and `instanceof` follows `matches`: a proxy failure at its
deadline is both a `ProxyError` and a `DeadlineError`.

| Code | Class | Fields |
|---|---|---|
| `NOT_MEASURED` | `NotMeasuredError` | |
| `CORS` | `CORSError` | `url`, `redirect`, `reason`, `status`, `header` |
| `PROXY` | `ProxyError` | `proxy`, `statusCode`, `header` |
| `PIN` | `PinError` | `host`, `presented` |
| `BLOCKED` | `BlockedError` | |
| `PLACEMENT` | `PlacementError` | |
| `CLOSE` | `CloseError` | `closeCode` (Go's `Code`), `reason` |
| `VALIDATION` | `ValidationError` | `problems` |
| `CANCELED` | `CanceledError` | |
| `DEADLINE` | `DeadlineError` | |
| `NET` | `NetError` | |
| `INVALID` | `InvalidError` | |
| `VERIFY` | `VerifyError` | |
| `INTERNAL` | `InternalError` | |

A failed WebSocket handshake's error carries the server's response
(`error.response`). Two errors are not the core's: `CoreExited` (the core
died; `exitCode`, `signal` and the end of its `stderr`) and
`ProtocolError` (the connection to it broke the protocol; it is stopped).

## The core's lifetime

One core serves the process, started at the first call (5–10 ms) and
again after one dies. It is unref'd whenever no call waits on it, so it
never keeps Node running, and it exits when your process does. Objects
the core holds (clients, transports, jars, caches, WebSockets, parsed
profiles) are freed by `close()`, `await using` (Node 24+), or when they
are garbage collected.

Saved state is written with Go's serializations; a file path given to
`writeJSON` or `writeNetscape` is written 0600 (cookies and tickets are
credentials) and replaced whole.

`MIMICRY_CORE`, an absolute path, runs another core binary
(`go build ./cmd/mimicry-core`, for development); otherwise the one in
the platform package is used, never one found in `PATH`.

## Development

```sh
npm ci
npm run check    # tsc (strict), eslint, the generated code's drift check
npm test         # builds .core/mimicry-core with Go, then node:test
npm run gen      # regenerate src/gen from proto/ (buf, protoc-gen-es)
npm run pack:all # build/packages/: the main package and one per platform
```

The tests run `src/` as it is (Node strips the types), so the sources use
only erasable TypeScript. `test/wire-vectors.test.ts` replays every
vector in `proto/testdata/wire/`; the harness's `TestNodeMatchesGo*`
(`internal/harness/e2e`) prove requests through this SDK are
byte-identical to the Go client's.
