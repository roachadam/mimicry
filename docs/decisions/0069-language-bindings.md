# 0069: Python and Node bindings through a per-process core child

- Status: Proposed
- Date: 2026-11-10
- Commits: none yet (this record is for review before any code)

**Summary.** Python and Node reach mimicry through `mimicry-core`, a
static Go binary (no cgo) that each host process starts as its own child.
They talk over an anonymous socketpair (Python) or Node's `'pipe'` stdio
(a socketpair on Unix), with a small framed, multiplexed protocol whose
messages are protobuf. Not in-process cgo: a Python parent that has merely
loaded a Go `c-shared` library hangs every forked child, on macOS and on
Linux (measured). Not a shared daemon, a local HTTP proxy (#110), gRPC or
JSON-RPC. The protocol and the core are built so a shared daemon (option 2)
can be added later without changing either. Nothing ships yet: this
records the design for [#111](https://github.com/roachadam/mimicry/issues/111)
and the five pull requests that will build it.

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
- **EVENT** is one-way: `OnSetCookie`, key log lines, connection-level
  notices.
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

### What keeps option 2 open, at no cost now

- The server is `Serve(io.ReadWriteCloser)`, not stdio-specific; the
  `cmd` wires it to stdin/stdout today and could wire it to an accepted
  Unix-socket connection later.
- Hello carries versions and features from v1, which a daemon (possibly
  older than its client) would need.
- Objects belong to their connection; nothing global to share or leak
  between clients.
- Each SDK's connection takes any socket, not only a child it spawned.

### Callbacks: data where possible

| Go callback | Binding |
|---|---|
| `Proxy` | Data: a proxy URL per client plus a per-request override |
| `GetClientCertificate` | Data: certificate chain and key (PEM, PKCS#12) |
| `ConnectUDPProxy` | Data: proxy → MASQUE endpoint map |
| `KeyLogWriter` | Data: a file path the core opens |
| `OnSetCookie` | EVENT, no reply |
| `CheckRedirect` | Data: follow or not, a hop limit |
| `VerifyConnection` | Later: an EVENT the host answers with a reply on the same stream (the handshake waits) |
| `DialContext` | Not offered (it returns a `net.Conn`); `LocalAddrs`, `Proxy` and a resolver as data cover the uses |
| Custom `ClientHintStore` | Not offered; the built-in store plus save/load |

Which of the optional callbacks are in v1 is open (below).

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

### Saved state

The SDK exposes save and load for the stores mimicry already serializes:
cookies (`cookies.Jar.WriteJSON`/`ReadJSON`, Netscape), tickets
(`TicketCache`), Alt-Svc (`AltSvcCache`). The bytes cross the connection;
the SDK writes files 0600.

### Packaging

- One repository: `bindings/python`, `bindings/node`, `proto/`,
  `cmd/mimicry-core`, `internal/rpc/{wire,server,convert}`. SDK versions
  move in lockstep with the core.
- Six static binaries: linux, darwin and windows, each amd64 and arm64.
  One Linux binary per architecture serves glibc and musl.
- Python: `py3-none-<platform>` wheels, manylinux and musllinux, each
  carrying its binary. Node: one package per platform in
  `optionalDependencies` with `os`, `cpu` and `libc`. No install scripts,
  no runtime download.

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
- **For a future daemon** (not built): a Unix socket 0600 in a 0700
  directory plus a peer-credential check (`SO_PEERCRED`, `getpeereid`);
  never plain HTTP on localhost; TCP only with TLS and a token.

## Consequences

- Fork-safe: a child that inherits the SDK detects it (`os.register_at_fork`
  or a pid check), drops the parent's connection and starts its own core
  (measured: 10 of 10). A core crash is an exception, not a dead host.
- Every platform with a static Go build is supported, musl included, with
  no C toolchain in the release.
- Each host process pays ~5–8 ms at first use and ~9–14 MB per core, and
  each call 3–25 µs of host CPU (more for a single caller on WSL2).

What users lose without a daemon, and what they do instead:

| Lost | Workaround |
|---|---|
| One browser identity used from several processes (gunicorn workers, Celery) | One process = one browser identity; async for concurrency inside it |
| Cookies and tickets surviving the process | Save and load: `cookies.Jar.WriteJSON/ReadJSON`, `TicketCache`, `AltSvcCache` |
| A ticket cache owned by one process at a time across workers | Load it into one process only, as [sessions](../concepts/sessions.md#tickets) already requires |
| One egress point for many processes | Proxies |
| `DialContext`, custom `ClientHintStore` | `LocalAddrs`, proxies, resolver as data; the built-in store plus save/load |

Windows has no socketpair for child stdio; anonymous pipes there are
reachable only through handle duplication by a process with access to the
host [INFERENCE: untested]. Windows support is an open question.

## Open questions (for the maintainer)

- **Package names.** `mimicry` is taken on PyPI (an unrelated optimisation
  library) and on npm (an inactive 2022 browser-mimic client); the
  `@mimicry` npm scope appears taken; `mimicry-http` was free on both on
  2026-11-10.
- **Python API shape.** `get`/`fetch` helpers, or explicit `kind=` and
  `from_=` on one `request`.
- **WebSockets in v1.**
- **Windows in v1.**
- **Which callbacks in v1** (`VerifyConnection` as EVENT + reply, a
  signer for client certificates in an HSM, a proxy callback).

## Plan

1. **Wire protocol** (`internal/rpc/wire`): frames, streams, credit,
   RESET, Hello; language-neutral test vectors (byte files) every SDK
   runs.
2. **Core server, schema and command**: `proto/`, `internal/rpc/server`
   and `convert`, `cmd/mimicry-core`; a harness e2e test proving requests
   through the core are byte-identical to the Go client's.
3. **Python SDK**: async first, then sync on the same protocol; wheels
   built in CI, not published.
4. **Node SDK**.
5. **Release pipeline**: six cross-compiled binaries; wheels including
   musllinux; npm per-platform packages with `os`/`cpu`/`libc`; Trusted
   Publishing and provenance.

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
