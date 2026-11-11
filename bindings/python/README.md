# mimicry for Python

HTTP requests the way Chrome, Firefox and Safari send them (the TLS handshake, HTTP/2 and HTTP/3, the headers and their order, cookies and redirects), from Python. This is the Python SDK of [mimicry](https://github.com/roachadam/mimicry), a Go library: each Python process starts its own `mimicry-core`, mimicry built as a small static program shipped inside the wheel, and talks to it over a socketpair (two anonymous pipes on Windows). Why it is built this way, and what was measured, is in [decision 0069](../../docs/decisions/0069-language-bindings.md).

> [!WARNING]
> Pre-release, not published yet. The distribution name `mimicry-http` is provisional; the import name is `mimicry`. The API may change before 1.0.

Python 3.11 or later, on Linux (glibc or musl), macOS 13 or later, and Windows; x86-64 and arm64. The one dependency is `protobuf` (6.30 or later).

```sh
pip install mimicry-http
```

## Quick start

```python
import mimicry
from mimicry import Config, Kind, Request, profiles

client = mimicry.new_client(Config(profile=profiles.chrome_macos()))

# A plain request is a page load, as if typed into the address bar.
page = client.do(Request("GET", "https://www.shop.example/p/42"))
html = page.read()

# A fetch() from that page: Origin, Referer, sec-fetch-* and cookies follow.
api = Request("GET", "https://www.shop.example/api/stock/42")
resp = client.do(mimicry.as_kind(api, Kind.FETCH))
```

The same with asyncio, from `mimicry.aio` (every name is the same; the calls are awaited):

```python
from mimicry import Config, Kind, Request, aio, as_kind, profiles


async def main() -> None:
    async with await aio.new_client(Config(profile=profiles.chrome_macos())) as client:
        page = await client.do(Request("GET", "https://www.shop.example/p/42"))
        html = await page.read()
        resp = await client.do(as_kind(Request("GET", "https://www.shop.example/api/stock/42"), Kind.FETCH))
        async for chunk in resp.aiter_bytes():
            ...
```

A request the profile can't describe fails with `NotMeasuredError` before anything is sent, rather than guessing.

## Go's names

The SDK uses mimicry's Go names in Python casing, so [the Go guides](../../docs/README.md) apply as they are. Three Go names are Python keywords or clash, and change:

| Go | Python |
|---|---|
| `mimicry.NewClient(cfg)`, `NewTransport(cfg)` | `mimicry.new_client(config)`, `new_transport(config)` |
| `client.Do(req)`, `transport.RoundTrip(req)` | `client.do(req)`, `transport.round_trip(req)` |
| `mimicry.Config{DialTimeout: 10 * time.Second, DisableHTTP3: true}` | `Config(dial_timeout=10, disable_http3=True)` (durations in seconds or `timedelta`) |
| `profiles.ChromeMacOS()`, `profiles.ByName("chrome-157-macos")` | `profiles.chrome_macos()` (the newest the core has), `"chrome-157-macos"` |
| `mimicry.Fetch`, `AsyncScript`, ... | `Kind.FETCH`, `Kind.ASYNC_SCRIPT`, ... |
| `mimicry.As(req, k)` | `as_kind(req, k)` |
| `mimicry.With(req, mimicry.Request{From: page, Kind: k})` | `with_options(req, Options(page=page, kind=k))` |
| `mimicry.Exact(req)`, `HeaderOrderKey` | `exact(req)`, `Request(..., header_order=[...])` |
| `Transport.Plan(req)` | `client.plan(req)` / `transport.plan(req)`: what would be sent, `str()` is Go's `Plan.String()` |
| `Transport.DialWebSocket(ctx, url, WebSocketOptions{From, Protocols})` | `client.dial_websocket(url, WebSocketOptions(page=, protocols=))` |
| `ws.ReadMessage`, `WriteMessage`, `Close(code, reason)` | `ws.read_message()`, `write_message(MessageType.TEXT, data)`, `close(code, reason)` |
| `jar.WriteJSON(w)` / `ReadJSON(r)` (`cookies.Jar`, `TicketCache`, `AltSvcCache`, `ClientHintStore`) | `write_json(path_or_file)` / `read_json(path_or_file)` |
| `context.Context` | `Request(timeout=...)`, and asyncio cancellation |

A `Request` is `Request(method, url, headers=, body=, ...)`; `headers` is a mapping or `(name, value)` pairs, a value of `None` removing a header the profile would send (as an empty Go `Header` entry does). A `body` of bytes is sent whole and sent again on a redirect that keeps it; an iterable of bytes (an async iterable in the async API) is streamed, and cannot be.

Callbacks are data, as decision 0069 lists: `proxy=` (a URL, per client or per `Request`), `connect_udp_proxy=`, `client_certificates=` (PEM bytes per host), `key_log_file=`, `dns_server=`, `follow_redirects=`/`max_redirects=` on `new_client`. Two are callables:

- `Config(verify_connection=fn)`: `fn(state: ConnectionState)` sees each verified TLS connection; raising rejects it (the request fails with `VerifyError`). In the async API it may be a coroutine function. `fetch_pins` ignores it (and `pins`), as Go's `FetchPins` does.
- `new_jar(on_set_cookie=fn)`: `fn(event: SetCookieEvent)` for each Set-Cookie line the jar receives.

In the sync API both run on worker threads (never the SDK's own), so they may block and call the SDK.

## Errors

Every exception is a `MimicryError`, with one class per kind of Go error: `NotMeasuredError`, `CORSError`, `ProxyError`, `PinError`, `BlockedError`, `PlacementError`, `CloseError`, `ValidationError`, `CanceledError`, `DeadlineError`, `NetError`, `InvalidError`, `VerifyError`, `InternalError`. Each carries `code`, `message` and `matches`, every kind its Go error chain matches (`errors.Is`/`errors.As`); the exception is an instance of each of them. A proxy that times out raises an error both `except ProxyError` and `except DeadlineError` catch. `DeadlineError` is also a `TimeoutError`, `NetError` a `ConnectionError` and `InvalidError` a `ValueError`. The typed fields are attributes: `ProxyError.status_code`, `CORSError.reason`, `CloseError.close_code`, `PinError.host`, `ValidationError.problems`.

If `mimicry-core` dies, every call on it raises `CoreExited`, with its exit status (`returncode`, negative for the signal that ended it) and the last of its stderr; its objects are gone. The next client made without an explicit core starts a new one. A broken connection with the core still running raises `ProtocolError` (and the core is stopped).

## Processes, threads and asyncio

- **One core per process.** The sync API's objects share one core, started on first use and driven by one background thread running an event loop; any number of threads may call them. The async API's objects use their event loop's core. A `mimicry.Core` / `aio.AsyncCore` you start yourself can be passed as `core=`.
- **fork.** A forked child never touches its parent's core: the SDK drops the inherited connection in the child (`os.register_at_fork`), and the child's first new object starts the child's own core. Objects made before the fork (a client made at import in a gunicorn `--preload` app, say) raise `MimicryError` in the child: their state (cookies, tickets) lives in the parent's core. Make clients after the fork, or save state in the parent and load it in each child. In the child, use the async API from a new event loop (`asyncio.run`), as asyncio requires after a fork anyway.
- **Event loops.** An async object belongs to the loop it was made on.
- **Cancellation.** Canceling a call (`task.cancel()`, a timeout) resets it in the core. A WebSocket read canceled part way loses nothing (the next read returns the message), and a write canceled part way still sends its message whole.
- **Windows.** The core's stdin and stdout are anonymous pipes driven by two threads, not `socket.socketpair()` (loopback TCP on Windows; decision 0069). Any event loop works.
- **Closing.** `close()` (or `with` / `async with`) frees an object in the core; one collected unclosed is freed in the background, and a response collected unread cancels its request. Closing the core, or the process exiting, ends the core.

## Saving an identity

Cookies, TLS session tickets, Alt-Svc records and client hints save and load as JSON, to carry an identity to another process ([sessions](../../docs/concepts/sessions.md)):

```python
jar = mimicry.new_jar(rules=profiles.chrome_macos())
tickets = mimicry.new_ticket_cache()
client = mimicry.new_client(Config(profile=profiles.chrome_macos(), cookies=jar, tickets=tickets))
...
jar.write_json("cookies.json")
tickets.write_json("tickets.json")
```

Cookies and tickets are credentials: a path given to `write_json` (and `write_netscape`) is written with mode 0600 and replaces the old file atomically. A file object you pass is written as you opened it. Load tickets into one process at a time.

## Development

From the repository root, with Go and [uv](https://docs.astral.sh/uv/):

```sh
moon run python:test python:lint   # builds the core into src/mimicry/_bin, then pytest; ruff and mypy
moon run python:proto              # after editing proto/: regenerates src/mimicry/_pb (buf, pinned)
bash bindings/python/build-wheels.sh dist   # every platform's wheel, its core cross-compiled
```

Without moon: `CGO_ENABLED=0 go build -o bindings/python/src/mimicry/_bin/ ./cmd/mimicry-core` (PowerShell: `$env:CGO_ENABLED=0` first; with a directory for `-o`, Go names the binary, `mimicry-core.exe` on Windows), then `uv run pytest` in `bindings/python`. `MIMICRY_CORE` (an absolute path) runs another core build. The tests replay the wire protocol's vectors (`proto/testdata/wire`), check the generated code against the schema, and run the SDK against a local TLS and WebSocket server; `internal/harness/e2e/python_test.go` checks that requests through the SDK are the Go client's on the wire (`MIMICRY_PYTHON=bindings/python/.venv/bin/python go test -run TestPythonSDK ./e2e` in `internal/harness`; a relative path there is the repository root's).
