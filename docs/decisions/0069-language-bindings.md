# 0069: Python and Node bindings through a per-process core child

- Status: Accepted; amended 2026-11-10 (the wire protocol's details, #126)
- Date: 2026-11-10
- Commits: `2f9c670` (the record; no code yet)

**Summary.** Python and Node reach mimicry through `mimicry-core`, a
static Go binary (no cgo) that each host process starts as its own child.
They talk over an anonymous socketpair (Python on Linux and macOS), Node's
`'pipe'` stdio (a socketpair on Unix), or anonymous pipes (Windows), with
a small framed, multiplexed protocol whose messages are protobuf. Not
in-process cgo: a Python parent that has merely loaded a Go `c-shared`
library hangs every forked child, on macOS and on Linux (measured). Not a
shared daemon, a local HTTP proxy (#110), gRPC or JSON-RPC. The SDKs use
Go's names in each language's casing; v1 has WebSockets, Windows, every
callback as data, and `VerifyConnection` as a reverse call. The protocol
and the core are built so a shared daemon (option 2) can be added later
without changing either. Nothing ships yet: this records the design for
[#111](https://github.com/roachadam/mimicry/issues/111) and the five pull
requests that will build it.

## Problem

mimicry is a Go library. Most people who need a browser's fingerprint
scrape from Python or Node, and today they use tls-client wrappers,
httpcloak or curl_cffi. #111 asks how mimicry should reach them.

A binding is not "request JSON in, response JSON out". It must carry:

- long-lived objects that refer to each other: a Transport is one tab
  ([0019](0019-transport-is-one-browsing-entity.md)); several share one
  identity's cookie jar, Alt-Svc cache, ticket cache and client hints;
- the request's kind and its page (`As`, `With`, `Exact`);
- streamed bodies both ways, with backpressure, and WebSockets;
- cancellation and deadlines;
- typed errors (`ErrNotMeasured`, `*CORSError`, `*ProxyError`,
  `*PinError`, `ErrBlocked`, `ErrPlacement`) that callers branch on;
- callbacks in `Config`: `Proxy`, `DialContext`, `VerifyConnection`,
  `GetClientCertificate`, `ConnectUDPProxy`, `KeyLogWriter`,
  `OnSetCookie`, a custom `ClientHintStore`;
- saved state: cookies, tickets (a secret,
  [sessions](../concepts/sessions.md#tickets)), Alt-Svc.

Whatever carries this must survive how Python services run (gunicorn
`--preload`, Celery prefork, `multiprocessing` with `fork`), install from
PyPI and npm on every platform including musl, and not crash the host.

## Options

| # | Option | Verdict | Main reason |
|---|---|---|---|
| 1 | In-process Go `c-shared` library through ctypes/cffi and koffi | Rejected | `fork` hangs or crashes the child; one Go runtime per process; no musl; a Go crash kills the host |
| 1b | Native extension (CPython C-API/abi3, Node-API addon, gopy) around the same library | Rejected | Everything in 1, plus C glue in two ABIs |
| 2 | Shared daemon (one core per user or host, Unix socket or TCP) | Not now; kept open | Shares one identity across processes, but brings auth, discovery, version skew and a shared blast radius |
| **3** | **Per-process child core, framed protocol over socketpair/stdio** | **Chosen** | Fork-safe, crash-isolated, static binary for every target, no ports |
| 4 | Local HTTP proxy ([#110](https://github.com/roachadam/mimicry/issues/110)) | Rejected as the binding | No request kind or page except through headers; needs a CA or `http://` rewriting; the host library adds its own headers |
| 5 | WebAssembly / WASI | Not viable | Go's `wasip1` and `js` ports have fake in-memory networking (`net/net_fake.go`); mimicry does not build for `wasip1` (`internal/dial`: `undefined: bindTo`, tried) |
| 6 | Rewrite in Rust (pyo3, napi-rs) | Out of scope | Re-implements the profile engine, the uTLS fork, QUIC and HTTP/3; two engines to keep byte-identical |

Within option 3, the wire format:

| Format | Verdict | Why (measured on macOS, prior research) |
|---|---|---|
| gRPC | Rejected | Cannot run over an inherited stdio fd in grpcio or grpc-js [INFERENCE], so it forces a socket path even for a child; Python grpcio cost 39–77 µs CPU per small call against 10–24 µs framed; needed tuned windows for bulk |
| JSON-RPC 2.0 | Rejected | Bodies go as base64: 3.6 ms of Python CPU per MB against 0.25–0.5 ms for raw frames |
| **Custom frames + protobuf messages** | **Chosen** | Raw body frames, one schema for Go, Python and TypeScript (buf), runs on socketpair, pipes, Unix sockets and TCP alike |

### What the other projects found

From a survey of 20-odd projects and their trackers (counts are rough,
from reading and sorting issues):

- **Go in-process, specifically.** tls-client and httpcloak ship Go
  `c-shared` libraries and hit the Go-only failures: `fork` deadlocks
  ([golang/go#15538](https://github.com/golang/go/issues/15538): "you can
  never use `fork` with any program that includes Go code";
  [go-jsonnet#484](https://github.com/google/go-jsonnet/issues/484)), two
  Go runtimes in one process "never really supported"
  ([golang/go#65050](https://github.com/golang/go/issues/65050), closed not
  planned), musl cannot `dlopen` a Go `c-shared` library
  ([golang/go#54805](https://github.com/golang/go/issues/54805), open), no
  unloading ([#11100](https://github.com/golang/go/issues/11100)). A user
  who has a tls-client wrapper installed next to mimicry's would be in the
  two-runtime case.
- **tls-client's ABI.** One blocking call per OS thread
  ([tls-client#300](https://github.com/bogdanfinn/tls-client/issues/300):
  64 in flight through Node's thread pool, 380 req/s against 1,156 for
  node-fetch); ~20× the body size allocated on the Go side
  ([#165](https://github.com/bogdanfinn/tls-client/issues/165)); a
  `freeMemory` contract two wrappers get wrong; sessions in a global map
  that never expire.
- **Subprocess precedents.** Playwright (Python drives a bundled driver
  over stdio pipes), esbuild (a Go child, binary protocol over stdio,
  reverse calls for plugins), typescript-go's API (~10 µs per stdio round
  trip, [typescript-go#711](https://github.com/microsoft/typescript-go/pull/711)).
  Their pain points are child lifecycle and error text when the child
  dies, not speed.
- **Loopback ports** (CycleTLS's localhost WebSocket, go-plugin's TCP on
  Windows) draw firewall, VPN and antivirus failures
  ([terraform#30532](https://github.com/hashicorp/terraform/issues/30532),
  [go-plugin#247](https://github.com/hashicorp/go-plugin/issues/247) asking
  for stdio). Stdio and socketpairs avoid that class.
- **Distribution** is the most reported pain everywhere (~200 issues):
  missing platform builds, runtime downloads failing, lockfile drift. The
  working layouts are `py3-none-<plat>` wheels carrying the binary
  (Playwright, httpcloak) and npm per-platform `optionalDependencies`
  with `os`/`cpu`/`libc` fields (esbuild, typescript-go). Downloading the
  binary at runtime is the most complained-about approach.
- **Callbacks.** No surveyed binding exposes TLS-verify, client-certificate
  or dialer callbacks to Python or JavaScript.

## Evidence

All measurements are throwaway programs, deleted afterwards. Each cell is
one run; expect ±10–20 % between runs [INFERENCE: not repeated].

**Machines.**

- macOS: Apple M3 Pro, 12 cores, macOS 27.0.1 (Darwin 27.0.0, arm64),
  Go 1.27.3, Python 3.12.3, Node 26.12.1.
- Linux: WSL2 Ubuntu 24.04, kernel 6.18.58.1-microsoft-standard-WSL2,
  AMD Ryzen 7 5800X3D (16 threads), 15 GB, Go 1.27.3, Python 3.12.3,
  Node 26.12.1 (Go and Node unpacked in `/tmp`, removed afterwards).

**Workload.** A Go child serves a framed protocol on its stdin/stdout:
10-byte header (stream u32, type u8, flags u8, length u32), a 280 B call,
a 500 B reply head, then a 200 B body ("small") or 1 MiB in 64 KiB DATA
frames ("1 MB"). All callers share one connection, demultiplexed by stream
id. Calls/s, p50/p99 latency, and host-process CPU (user + system) per
call; MB/s for 1 MB bodies. 2 s runs (Go 3 s) after a 0.3 s warm-up.

### Pipes vs socketpair, Linux

Host process only. "64" is 64 concurrent callers on one connection.

| Host | Carrier | small, 1: calls/s · p50 µs · CPU µs | small, 64: calls/s · CPU µs | 1 MB, 1: MB/s · CPU µs | 1 MB, 64: MB/s · CPU µs |
|---|---|---|---|---|---|
| Go client (baseline) | pipes | 7.9 k · 123 · 86 | 302 k · 7.7 | 957 · 812 | 1,089 · 741 |
| | socketpair | 5.9 k · 165 · 100 | 230 k · 11.4 | **2,647** · 316 | **3,809** · 291 |
| Python sync (threads, `threading.Event` per call) | pipes | 7.9 k · 121 · 67 | 7.6 k · 186 | 1,157 · 764 | 1,180 · 818 |
| | socketpair | 5.8 k · 166 · 96 | 7.7 k · 193 | 2,268 · 377 | 3,383 · 352 |
| Python asyncio, `StreamReader` | pipes | 6.5 k · 147 · 88 | 112 k · 8.9 | 547 · 1,829 | 631 · 1,659 |
| | socketpair | 5.7 k · 167 · 110 | 95 k · 10.5 | 681 · 1,456 | 840 · 1,248 |
| Python asyncio, `BufferedProtocol` (one copy into `bytes`) | socketpair | 7.1 k · 139 · 75 | 122 k · 8.2 | 2,498 · 341 | 3,380 · 304 |
| | socketpair, 4 MB `SO_SNDBUF`/`SO_RCVBUF` | 7.3 k · 130 · 73 | 119 k · 8.4 | **3,001** · 271 | **8,291** · 127 |
| Python sync | pipes, 1 MB `F_SETPIPE_SZ` | | | 1,720 · 512 | 3,589 · 339 |
| Node (`net.Socket`) | pipes (FIFOs [^fifo]) | 9.9 k · 96 · 43 | 271 k · 3.8 | 1,321 · 902 | 1,192 · 959 |
| | `spawn` `'pipe'` stdio (a socket, checked in `/proc`) | 9.2 k · 103 · 47 | 201 k · 5.1 | 2,131 · 549 | 1,896 · 634 |

[^fifo]: Node cannot create an anonymous pipe for a child, so its "pipes"
row uses two FIFOs, the same kernel pipe object.

### The same, macOS

From the prior research for #111 (Go↔Go and Python/Node host rows), plus
reruns today marked "today".

| Host | Carrier | small, 1: calls/s · CPU µs | small, 64: calls/s · CPU µs | 1 MB, 1: MB/s · CPU µs | 1 MB, 64: MB/s · CPU µs |
|---|---|---|---|---|---|
| Go↔Go (both processes) | pipes | 37.3 k · 43.7 | 370 k · 22.1 | 2,293 · 1,134 | 3,536 · 716 |
| Python asyncio | pipes | 14.5 k · 23.2 | 78.8 k · 12.3 | 315 · 3,182 (today: 2,858 · 276) | 1,623 · 570 |
| | socketpair | 17.5 k · 22.0 | 99.4 k · 9.9 | 3,571 · 250 | 4,504 · 232 |
| Python asyncio `BufferedProtocol`, today | socketpair, default buffers | 29.7 k · 12.4 | 280 k · 3.5 | 963 · 756 | 960 · 766 |
| | socketpair, 4 MB buffers | | 343 k · 2.9 | **9,483** · 79 | **15,306** · 68 |
| Python sync, today | pipes | 51.1 k · 10.7 | 56.2 k · 30.0 | 5,024 · 120 | 5,220 · 131 |
| | socketpair, default buffers | 49.2 k · 11.5 | 57.6 k · 29.8 | 1,414 · 485 | 1,385 · 512 |
| | socketpair, 4 MB buffers | | | 11,175 · 74 | |
| Node | `spawn` `'pipe'` stdio | 50.5 k · 9.1 | 342 k · 3.2 | 3,580 · 413 | 3,405 · 445 |

What the numbers say:

- **Socketpair is never the slow choice once its buffers are raised, and
  default buffers are a trap on macOS.** macOS Unix sockets default to
  8 KB (`net.local.stream.sendspace`); with them, 1 MB bodies into Python
  run at ~1 GB/s; with 4 MB `SO_SNDBUF`/`SO_RCVBUF` on both ends,
  9.5–15 GB/s. On Linux the default (208 KB) already gives 2.3–3.8 GB/s,
  and 4 MB buffers 3–8 GB/s. Linux pipes are 2–3× slower than a
  socketpair for bulk at default sizes (Go 957 vs 2,647 MB/s); raising the
  pipe to 1 MB narrows it.
- **The prior "pipes are slow on macOS" finding (300 calls/s of 1 MB) did
  not reproduce today (2,726 calls/s)**; the difference was the reader, not
  the kernel [INFERENCE]. The choice of socketpair now rests on the
  `/proc` finding below and on the buffer sizes, not on pipe speed.
- **The Python reader matters more than the carrier.** asyncio's
  `StreamReader` caps 1 MB bodies at 0.5–1 GB/s on both systems; a
  `BufferedProtocol` reading into one preallocated buffer reaches the
  carrier's speed. The SDK uses a `BufferedProtocol`.
- **A thread per call blocked on a `threading.Event` collapses on Linux**:
  64 threads, 7.7 k calls/s at 190 µs CPU per call (macOS: 57 k at 30 µs).
  The sync API must not be built that way; it should drive the same
  event-loop protocol from one background thread [INFERENCE: not
  measured].
- **Single-caller latency on WSL2 is 100–170 µs against 20–60 µs on
  macOS**, in Go too, so it is the VM's wakeup latency, not the protocol
  [INFERENCE]. At 64 callers the host CPU per small call is about the
  same on both: Python asyncio 3–13 µs, Node 3–5 µs. For scale, aiohttp
  spends 31–46 µs and Node's `fetch` 49–70 µs per request to localhost
  (macOS), and mimicry itself 35–50 µs per warm GET
  ([performance](../performance.md)).
- Node's default child stdio is already a socketpair on Linux
  (`/proc/<child>/fd/0` → `socket:[…]`), so the Node SDK needs nothing
  special beyond the buffer sizes [INFERENCE: libuv does not let us set
  them on stdio sockets without an fd; to check in PR 4].

### Fork

A Python parent loads a Go `c-shared` library, then calls `os.fork()`;
the child calls an exported function that makes five HTTP requests
through `net/http` (goroutines, timers, the netpoller, as mimicry uses
them). A child still running after 3 s is a hang. 10 forks each.

| Parent at fork | macOS | Linux |
|---|---|---|
| Library loaded, never called (`import` in a gunicorn `--preload` master) | 10 hang | **10 hang** |
| Library busy (4 threads making requests) | 9 crash (`fatal error: runtime: netpollBreak write failed`), 1 hang | **10 hang** |
| A child-process core instead: the forked child drops the parent's socket, starts a fresh core, makes 100 calls | not run | **10 ok**; the parent's core still answers afterwards |

On macOS the toy case (the child only runs Go code with no goroutines,
netpoll or timers) passed 20 of 20, which is why "it worked in my test"
reports happen. Python 3.14 made `forkserver` the default
`multiprocessing` start method on POSIX, but gunicorn `--preload`, Celery
prefork and explicit `fork` are unaffected [INFERENCE].

### `/proc/<pid>/fd` reachability (Linux)

A separate process of the same user opens `/proc/<parent>/fd/N`:

| Descriptor | Result |
|---|---|
| Read end of an anonymous pipe (`pipe:[16546]`) | **opened** |
| One end of a socketpair (`socket:[16547]`) | `ENXIO` (errno 6, "No such device or address") |

So any process of the same user (and ptrace rules aside) can read or
write a pipe-based protocol through `/proc`; a socketpair cannot be
reopened that way. This is why the carrier is a socketpair, not pipes.

### Core startup and memory

Spawn of a static core (`CGO_ENABLED=0`, 18 MB) to its reply to a hello
that creates one client (`mimicry.NewClient` with `profiles.ChromeMacOS()`).
20 trials; RSS read 1 s after the first hello.

| | spawn → hello reply (min / p50 / max) | idle RSS, one session |
|---|---|---|
| macOS (arm64) | 6.7 / 8.2 / 17.0 ms | 13.4 MB |
| Linux, WSL2 (amd64) | 4.7 / 4.9 / 5.4 ms | 8.7 MB |

A side finding: on macOS a core whose parent end of the socketpair had
leaked into it (no `SOCK_CLOEXEC` on Darwin; Go's `syscall.Socketpair`
does not set close-on-exec) never saw EOF when the parent closed its end,
so it did not exit [INFERENCE: cause not traced further]. The core's
"exit on EOF" lifetime depends on the descriptors being non-inheritable.

## Decision

Option 3. One static Go binary, `mimicry-core` (`cmd/mimicry-core`,
`CGO_ENABLED=0`), started by each host process as its own child, spoken to
over one connection with a framed, multiplexed protocol carrying protobuf
messages.

### The protocol

```
frame  = header payload
header = stream u32 | type u8 | flags u8 | length u32     (big-endian, 10 bytes)
type   = CALL | REPLY | DATA | WINDOW | RESET | EVENT
flags  = END (last frame of this direction of the stream)
length ≤ 65,536
```

- **Streams.** Each call is a stream. The SDK opens odd ids, the core
  even ones (for reverse calls such as `VerifyConnection`). Stream 0 is
  the connection.
- **CALL / REPLY.** One protobuf message each: the method and its
  arguments, then the result or a structured error. A request's body
  follows its CALL as DATA frames; a response's body follows its REPLY.
- **DATA** carries raw bytes, never base64. Bodies, WebSocket messages
  and uploads are chunked at 64 KiB.
- **WINDOW** grants credit per stream: a sender stops when its credit for
  that stream is spent, so one slow body cannot stall the connection or
  grow memory without bound.
- **RESET** cancels a stream from either side: the SDK's cancellation or
  timeout becomes the request's `context` being cancelled.
- **EVENT** is a notice from the core: one-way for `OnSetCookie` and
  connection-level notices; for `VerifyConnection` it opens a core stream
  the host answers with a REPLY.
- **Hello** is the first CALL on every connection: protocol major/minor,
  core version, feature bits. A mismatch fails at start with both
  versions named.
- Messages are protobuf, in `proto/`; buf generates Go, Python and
  TypeScript types. Each SDK hand-writes its idiomatic layer on top.

### Objects

Every object the core holds (client/Transport, cookie jar, Alt-Svc cache,
ticket cache, client-hint store, edited profile, response body,
WebSocket) has an id owned by the connection that created it. The host
holds the id, frees it with `close()` (plus `weakref.finalize` /
`FinalizationRegistry` as a backstop), and the core frees everything a
connection owned when it drops. The core has no global state.

A WebSocket is one bidirectional stream: `DialWebSocket` is its CALL and
REPLY (with the handshake response), then each message is DATA frames in
either direction (END closes the message, a one-byte message type leads
it), `Close(code, reason)` is a CALL on the connection naming the
WebSocket, and RESET aborts it. Credit applies per direction, so a reader
that stops reading stops the server's messages at the core, not in host
memory.

### What keeps option 2 open, at no cost now

- The server is `Serve(io.ReadWriteCloser)`, not stdio-specific; the
  `cmd` wires it to stdin/stdout today and could wire it to an accepted
  Unix-socket connection later.
- Hello carries versions and features from v1, which a daemon (possibly
  older than its client) would need.
- Objects belong to their connection; nothing global to share or leak
  between clients.
- Each SDK's connection takes any socket, not only a child it spawned.

### The SDKs' API: Go's names

Both SDKs mirror Go's names in their own casing: Python `snake_case` for
functions, methods, fields and arguments, `UPPER_CASE` enum members;
Node `camelCase` members and `PascalCase` types and enum members.
Initialisms keep Go's capitals in Node (`disableHTTP3`, `noCORS`) and are
lower-case words in Python (`disable_http3`, `no_cors`). A
[`docs/concepts/`](../concepts/) page per SDK is the translation of the
Go guides, not a second design.

Three Go names cannot be kept. Python reserves `as`, `with` and `from`,
and neither SDK uses a trailing-underscore spelling, so both use the
same alternatives: `As` → `as_kind`/`asKind`, `With` →
`with_options`/`withOptions`, and `From` → `page` (it is the page making
the request). `With`'s argument, Go's `mimicry.Request`, is `Options` in
both SDKs: Python's HTTP libraries and Node's `fetch` already have a
`Request` class.

| Go | Python | Node |
|---|---|---|
| `mimicry.NewClient(cfg)` | `mimicry.new_client(config)` | `newClient(config)` |
| `mimicry.NewTransport(cfg)` | `mimicry.new_transport(config)` | `newTransport(config)` |
| `client.Do(req)` | `client.do(req)` / `await client.do(req)` | `await client.do(req)` |
| `Config{DialTimeout, DisableHTTP3, MaxConnsPerHost, HTTP3OverSOCKS5, …}` | `Config(dial_timeout=, disable_http3=, max_conns_per_host=, http3_over_socks5=, …)` | `{dialTimeout, disableHTTP3, maxConnsPerHost, http3OverSOCKS5, …}` |
| `mimicry.Navigate`, `AsyncScript`, `Fetch`, `XHR`, … (`Kind`) | `Kind.NAVIGATE`, `Kind.ASYNC_SCRIPT`, `Kind.FETCH`, `Kind.XHR` | `Kind.Navigate`, `Kind.AsyncScript`, `Kind.Fetch`, `Kind.XHR` |
| `mimicry.As(req, k)` | `as_kind(req, k)` | `asKind(req, k)` |
| `mimicry.With(req, mimicry.Request{…})` | `with_options(req, Options(…))` | `withOptions(req, {…}: Options)` |
| `Request.Kind`, `From`, `Priority`, `CrossOrigin`, `Credentials`, `NoCORS`, `Stylesheet`, `IgnoreCORS`, `HeaderPlacement`, `ClientHints` | `Options.kind`, `page`, `priority`, `cross_origin`, `credentials`, `no_cors`, `stylesheet`, `ignore_cors`, `header_placement`, `client_hints` | `Options.kind`, `page`, `priority`, `crossOrigin`, `credentials`, `noCORS`, `stylesheet`, `ignoreCORS`, `headerPlacement`, `clientHints` |
| `mimicry.Exact(req)`, `HeaderOrderKey` | `exact(req)`, `header_order=` | `exact(req)`, `headerOrder` |
| `Transport.Plan(req)` | `transport.plan(req)` | `transport.plan(req)` |
| `Transport.DialWebSocket(ctx, url, WebSocketOptions{From, Protocols, Header})` | `transport.dial_websocket(url, WebSocketOptions(page=, protocols=, header=))` | `transport.dialWebSocket(url, {page, protocols, header})` |
| `WebSocket.ReadMessage`, `WriteMessage`, `Close(code, reason)` | `read_message`, `write_message`, `close(code, reason)` | `readMessage`, `writeMessage`, `close(code, reason)` |
| `Jar`, `TicketCache`, `AltSvcCache` `.WriteJSON(w)` / `.ReadJSON(r)` | `.write_json(f)` / `.read_json(f)` | `.writeJSON(stream)` / `.readJSON(stream)` |
| `ErrNotMeasured`, `ErrBlocked`, `ErrPlacement` | `NotMeasuredError`, `BlockedError`, `PlacementError` | same as Python |
| `*CORSError`, `*ProxyError`, `*PinError`, `*CloseError` | `CORSError`, `ProxyError`, `PinError`, `CloseError` | same |
| `context.Context` | `timeout=` and asyncio cancellation | `signal: AbortSignal` |

### Callbacks: data, plus VerifyConnection

| Go | v1 binding |
|---|---|
| `Proxy` | Data: a proxy URL per client plus a per-request override |
| `ConnectUDPProxy` | Data: proxy → MASQUE endpoint map |
| `GetClientCertificate` | Data: certificate and key files per host. A signer callback (keys in an HSM or keychain) later, on request |
| `KeyLogWriter` | Data: a file path the core opens |
| `Resolver` | Data: a DNS server address |
| `http.Client.CheckRedirect` | Data: follow or not, a maximum number of hops |
| `VerifyConnection` | **v1**: the core sends an EVENT on a stream of its own (even id) with the connection state as data; the handshake waits for the host's REPLY on that stream (accept, or an error that fails the connection). A RESET or a disconnect fails the handshake |
| `OnSetCookie` | EVENT, no reply |
| `DialContext` | Not offered (it returns a `net.Conn`); `LocalAddrs`, proxies and the DNS server cover the uses |
| Custom `ClientHintStore` | Not offered; the built-in store, saved and loaded |

### Errors

A REPLY error carries a code (one per sentinel and error type:
`NOT_MEASURED`, `CORS`, `PROXY`, `PIN`, `BLOCKED`, `PLACEMENT`, `CLOSE`,
`VALIDATION`, `CANCELED`, `DEADLINE`, `NET`, `INTERNAL`), the typed
fields (`ProxyError.StatusCode` and header, `CORSError`'s reason,
`PinError`'s host), the message, and the flattened set of `errors.Is`
matches, so a proxy error that is also a deadline maps to an exception
both `except ProxyError` and `except TimeoutError` catch. Each SDK has one
exception class per code under a common base. A core that dies raises a
`CoreExited` error with its exit status and the last lines of its stderr.

### Cookies, tickets, Alt-Svc

These are core objects with ids, as in Go: the jar (`cookies.Jar`), the
`TicketCache` and the `AltSvcCache`, shared by the clients that name
them. The SDK gets and sets their contents (the jar's cookies by URL or
all of them, `set`, `clear`) and saves and loads them with Go's
serializations (`write_json`/`read_json`, the jar's Netscape format too).
The bytes cross the connection; a file the SDK writes is created 0600.

### Packaging

- One repository: `bindings/python`, `bindings/node`, `proto/`,
  `cmd/mimicry-core`, `internal/rpc/{wire,server,convert}`. SDK versions
  move in lockstep with the core.
- Six static binaries: linux, darwin and windows, each amd64 and arm64.
  One Linux binary per architecture serves glibc and musl.
- Python: `py3-none-<platform>` wheels, manylinux, musllinux, macOS and
  Windows, each carrying its binary. Node: one package per platform in
  `optionalDependencies` with `os`, `cpu` and `libc`. No install scripts,
  no runtime download.
- Windows is in v1, with Windows CI for the SDKs.

## Security

- **Socketpair, not pipes, on Linux and macOS.** A pipe end is openable
  through `/proc/<pid>/fd` by any same-user process (measured); a socket
  is not (`ENXIO`).
- **Non-inheritable descriptors.** Both ends are close-on-exec (set
  explicitly where `SOCK_CLOEXEC` is missing); only the child's end is
  passed, as its stdin and stdout. A leaked end also defeats exit-on-EOF.
- **The core is started by absolute path inside the installed package**,
  never looked up in `PATH`.
- **The binary ships inside the wheel and the npm package**, never
  downloaded at runtime, and is published with provenance (PyPI Trusted
  Publishing, npm `--provenance`).
- **Saved state is written 0600**: cookies and tickets are credentials
  ([sessions](../concepts/sessions.md#tickets)).
- **No secrets on the core's stderr**: no cookies, header values, ticket
  bytes, proxy credentials or key material in logs or crash text.
- **Windows, Python (PR 3): anonymous pipes, never `socket.socketpair()`.**
  Windows has no `AF_UNIX` socketpair in Python; `socket.socketpair()`
  falls back to a pair of loopback TCP sockets, and before CPython
  3.10.15, 3.11.10, 3.12.5 and 3.13.0 it did not check that the accepted
  connection was its own, so a local process could win the race
  (CVE-2024-3219, [PSF-2024-7](https://osv.dev/vulnerability/PSF-2024-7),
  [cpython#122133](https://github.com/python/cpython/issues/122133); last
  affected releases 3.10.14, 3.11.9, 3.12.4, 3.13.0b4, from OSV's data).
  3.10 and 3.11 were in security-only maintenance by then, which ships no
  Windows installers, so Windows users of those lines are likely still on
  affected builds [INFERENCE]. Even fixed, it is a loopback TCP connection
  that firewalls and antivirus see. The sync driver uses `subprocess.PIPE`
  (`_winapi.CreatePipe`, anonymous pipes). If the async driver uses
  `asyncio.create_subprocess_exec`, its pipes are named pipes
  (`asyncio.windows_utils.pipe`: a random name, one instance,
  `FILE_FLAG_FIRST_PIPE_INSTANCE`, the client end opened at once); PR 3
  either uses those after checking that no other client can connect, or
  drives the anonymous pipes from a thread. asyncio on Windows needs the
  Proactor loop for subprocess pipes (the survey's Playwright
  [#2696](https://github.com/microsoft/playwright-python/issues/2696)).
- **Windows, Node (PR 4): check that the child's stdio pipes accept no
  other client.** libuv creates them as named pipes
  (`\\?\pipe\uv\<number>-<pid>`, one instance,
  `FILE_FLAG_FIRST_PIPE_INSTANCE`, the default security descriptor) and
  opens the client end immediately
  ([`src/win/pipe.c`](https://github.com/libuv/libuv/blob/v1.x/src/win/pipe.c),
  `uv__pipe_server`, `uv__create_stdio_pipe_pair`). With one instance,
  another process that connects first should make the spawn fail rather
  than take over the channel [INFERENCE from the source]; the name is
  guessable, so PR 4 tests it (a third client racing the spawn, another
  user connecting) and fails closed.
- **For a future daemon** (not built): a Unix socket 0600 in a 0700
  directory plus a peer-credential check (`SO_PEERCRED`, `getpeereid`);
  never plain HTTP on localhost; TCP only with TLS and a token.

## Consequences

- Fork-safe: a child that inherits the SDK detects it (`os.register_at_fork`
  or a pid check), drops the parent's connection and starts its own core
  (measured: 10 of 10). A core crash is an exception, not a dead host.
- Every platform with a static Go build is supported, musl and Windows
  included, with no C toolchain in the release.
- Each host process pays ~5–8 ms at first use and ~9–14 MB per core, and
  each call 3–25 µs of host CPU (more for a single caller on WSL2).
  Windows is not measured; PRs 3 and 4 record it.
- Users of the Go guides find the same names in both SDKs, except
  `as_kind`, `with_options`, `Options` and `page`.

What users lose without a daemon, and what they do instead:

| Lost | Workaround |
|---|---|
| One browser identity used from several processes (gunicorn workers, Celery) | One process = one browser identity; async for concurrency inside it |
| Cookies and tickets surviving the process | Save and load: the jar's, `TicketCache`'s and `AltSvcCache`'s `write_json`/`read_json` |
| A ticket cache owned by one process at a time across workers | Load it into one process only, as [sessions](../concepts/sessions.md#tickets) already requires |
| One egress point for many processes | Proxies |
| `DialContext`, custom `ClientHintStore`, a client-certificate signer | `LocalAddrs`, proxies, a DNS server; the built-in store, saved and loaded; certificate files (a signer later, on request) |

## Open questions

- **Package names**, decided in the Python SDK pull request. `mimicry` is
  taken on PyPI (an unrelated optimisation library) and on npm (an
  inactive 2022 browser-mimic client); the `@mimicry` npm scope appears
  taken; `mimicry-http` was free on both on 2026-11-10.

## Plan

1. **Wire protocol** (`internal/rpc/wire`): frames, streams, credit,
   RESET, Hello; language-neutral test vectors (byte files) every SDK
   runs.
2. **Core server, schema and command**: `proto/`, `internal/rpc/server`
   and `convert`, `cmd/mimicry-core`; every callback as data,
   `VerifyConnection` as EVENT + reply, WebSockets as one stream; a
   harness e2e test proving requests through the core are byte-identical
   to the Go client's.
3. **Python SDK**: async first, then sync on the same protocol; Go's
   names; WebSockets; `VerifyConnection`; Windows with anonymous pipes and
   Windows CI; wheels built in CI, not published; package name decided.
4. **Node SDK**: the same, with the Windows named-pipe check.
5. **Release pipeline**: six cross-compiled binaries; wheels including
   musllinux and Windows; npm per-platform packages with
   `os`/`cpu`/`libc`; Trusted Publishing and provenance.

## Revisit when

- Users ask to share one identity across processes and save/load does
  not serve them: add option 2 (a daemon on the same protocol).
- Go supports `fork` after the runtime starts
  ([#67174](https://github.com/golang/go/issues/67174),
  [#77032](https://github.com/golang/go/issues/77032)), one runtime per
  process stops being a limit, and musl can load `c-shared`: in-process
  could be reconsidered for its lower single-caller latency.
- The host CPU per call shows up in a user's profile next to their HTTP
  library's own cost.
- Users ask for a client-certificate signer callback, or Windows numbers
  show the pipes cost more than the socketpair numbers here.

## Amendment 2026-11-10: the wire protocol's details

The first pull request ([#126](https://github.com/roachadam/mimicry/issues/126),
`internal/rpc/wire`) settled what the record left open. The rules, written
for the SDKs, and the test vectors every implementation replays are in
[`proto/testdata/wire/`](../../proto/testdata/wire/README.md); this is why
they are what they are.

- **Types are 1 to 6; 0 is invalid**, so a run of zero bytes fails at once
  (`UNKNOWN_TYPE`).
- **Who opens with what.** The SDK opens a stream with CALL, the core with
  EVENT (reverse calls); EVENT on stream 0 is a notice nothing answers. A
  CALL from the core or an EVENT from the SDK is a protocol error, so each
  frame type has one meaning per direction.
- **The window and the stream limit are agreed values, not negotiated:**
  256 KiB of credit per direction of each stream and 256 streams per side,
  the same on both ends (`wire.Config`; the vectors use 8 bytes and 2).
  The receiver enforces them: DATA beyond the credit resets that stream
  (`CREDIT_OVERRUN`), and a peer opening one stream too many fails the
  connection (`STREAM_LIMIT`), while the opener waits for a free stream.
  Rejected: HTTP/2's REFUSED_STREAM, which turns load into errors the SDK
  must retry, and leaves the refusing side either tracking each refused
  stream until its in-flight DATA is over (unbounded) or ignoring frames on
  unknown streams (and so detecting no stray frame at all). If the values
  ever change, Hello carries them.
- **A RESET is answered by a RESET**, unless the answering side already
  sent END. Each side then knows exactly when the peer stopped sending on a
  stream, frees it then, and needs to remember only the highest id each
  side opened: any frame on a closed stream other than a WINDOW or RESET in
  flight is an error (`STREAM_CLOSED`). Rejected: remembering reset ids
  (unbounded) and HTTP/2's leniency towards frames on recently closed
  streams (a timing rule).
- **A stream holds its place in the limit until its RESET is written.**
  Otherwise a peer that stops reading could open and reset streams in a
  loop, each answering RESET (and the stream behind it) queued without
  bound; holding the place caps the queue at one RESET per stream allowed,
  and the peer, still counting the stream until the answer reaches it,
  never notices.
- **Errors have a precedence** (the header, the frame's shape, the
  stream's state, the order of its frames, credit; the README lists it),
  so every implementation names the same error when a frame breaks two
  rules, and the vectors pin each overlap.
- **A connection failure always wraps `ErrConnClosed`** with its cause,
  and a stream failing alone never does, so the layer above tells a lost
  connection from a reset call. `Stream.Reset` sends any code; `Close` is
  `Reset(CANCEL)`.
- **Credit returns at half the window**: once the bytes the application
  read since the last WINDOW reach half the window, they go back in one
  WINDOW, never after END. It is the rule the vectors assume, so every SDK
  sends the same frames.
- **Protocol errors fail the connection** with a RESET on stream 0 naming
  the error (a code per error, `CANCEL` 0 to `BAD_FRAME` 9), except a
  credit overrun, which resets only its stream: a peer that broke the
  framing rules cannot be trusted with the rest of the connection, while
  an overrun is a per-stream accounting mistake.
- **Fairness**: one writer takes frames first come, first served, and each
  stream has at most one frame of its caller's queued, so a frame waits
  for at most the one being written and one queued frame per other active
  stream; a 1 MiB body goes out one 64 KiB frame at a time between other
  streams' frames (`TestNoStall`).
- **No timer in the protocol.** Deadlines are the callers' contexts; an
  ended context resets its stream.
- **Hello is a message**, the SDK's first CALL, built and checked in the
  second pull request; the wire carries it like any other call.

Open for the second pull request: the record says a WebSocket message's
DATA frames end with END, but END ends a direction of the stream, which a
WebSocket keeps open across messages. Message boundaries need to live
inside the DATA bytes (a length or a flag leading each message), decided
with the schema.
