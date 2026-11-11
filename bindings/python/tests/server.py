"""A small HTTPS (HTTP/1.1) and WebSocket server for the SDK's tests, on its
own thread and event loop, with the test CA's certificate for localhost,
127.0.0.1 and ::1.

Routes:

- ``/echo``: the request as JSON (method, path, headers in order, body
  length and SHA-256).
- ``/bytes/<n>``: n bytes, written 64 KiB at a time.
- ``/hang``: never answers; the connection's end is recorded.
- ``/stall``: a response head and 64 KiB of body, then nothing.
- ``/redirect``: 302 to ``/echo``.
- ``/cookies``: sets two cookies.
- ``/ws``: a WebSocket echoing each message; a close is answered in kind.
"""

from __future__ import annotations

import asyncio
import base64
import hashlib
import json
import ssl
import struct
import threading
from dataclasses import dataclass, field
from pathlib import Path

TESTDATA = Path(__file__).parent / "testdata"
CA = (TESTDATA / "ca.pem").read_bytes()
_WS_GUID = b"258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
_CHUNK = b"\x5a" * (64 << 10)


@dataclass
class Seen:
    """What the server saw of the requests on one path."""

    requests: int = 0
    closed: threading.Event = field(default_factory=threading.Event)


class Server:
    def __init__(self) -> None:
        self.loop = asyncio.new_event_loop()
        self.seen: dict[str, Seen] = {}
        self._lock = threading.Lock()
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(TESTDATA / "server.pem", TESTDATA / "server-key.pem")
        ctx.set_alpn_protocols(["http/1.1"])
        self._ctx = ctx
        ready = threading.Event()
        self._thread = threading.Thread(target=self._run, args=(ready,), name="test server", daemon=True)
        self._thread.start()
        ready.wait()

    def _run(self, ready: threading.Event) -> None:
        asyncio.set_event_loop(self.loop)
        self._server = self.loop.run_until_complete(
            asyncio.start_server(self._conn, "127.0.0.1", 0, ssl=self._ctx, limit=1 << 20)
        )
        self.port = self._server.sockets[0].getsockname()[1]
        ready.set()
        self.loop.run_forever()

    def url(self, path: str, host: str = "127.0.0.1") -> str:
        return f"https://{host}:{self.port}{path}"

    def ws_url(self, path: str = "/ws") -> str:
        return f"wss://127.0.0.1:{self.port}{path}"

    def path(self, path: str) -> Seen:
        with self._lock:
            return self.seen.setdefault(path, Seen())

    def reset(self, path: str) -> Seen:
        with self._lock:
            s = self.seen[path] = Seen()
            return s

    def close(self) -> None:
        async def stop() -> None:
            self._server.close()
            await self._server.wait_closed()

        asyncio.run_coroutine_threadsafe(stop(), self.loop).result(5)
        self.loop.call_soon_threadsafe(self.loop.stop)
        self._thread.join(5)

    async def _conn(self, r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        try:
            while await self._request(r, w):
                pass
        except (ConnectionError, asyncio.IncompleteReadError, ssl.SSLError):
            pass
        finally:
            w.close()

    async def _request(self, r: asyncio.StreamReader, w: asyncio.StreamWriter) -> bool:
        line = await r.readline()
        if not line:
            return False
        method, target, _ = line.decode("latin-1").rstrip("\r\n").split(" ", 2)
        headers: list[tuple[str, str]] = []
        while (h := await r.readline()) not in (b"\r\n", b"\n", b""):
            name, value = h.decode("latin-1").rstrip("\r\n").split(":", 1)
            headers.append((name, value.strip()))
        lower = {k.lower(): v for k, v in headers}
        body = b""
        if "content-length" in lower:
            body = await r.readexactly(int(lower["content-length"]))
        elif lower.get("transfer-encoding", "").lower() == "chunked":
            parts = []
            while True:
                size = int((await r.readline()).split(b";")[0], 16)
                if size == 0:
                    while await r.readline() not in (b"\r\n", b""):
                        pass
                    break
                parts.append(await r.readexactly(size))
                await r.readline()
            body = b"".join(parts)
        path = target.split("?", 1)[0]
        seen = self.path(path)
        seen.requests += 1
        if path == "/ws":
            await self._websocket(r, w, lower)
            return False
        if path == "/hang":
            try:
                await r.read()
            finally:
                seen.closed.set()
            return False
        if path == "/stall":
            w.write(b"HTTP/1.1 200 OK\r\nContent-Length: 10485760\r\n\r\n" + _CHUNK)
            await w.drain()
            try:
                await r.read()
            finally:
                seen.closed.set()
            return False
        if path.startswith("/bytes/"):
            n = int(path[len("/bytes/") :])
            w.write(
                f"HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: {n}\r\n\r\n".encode()
            )
            while n > 0:
                k = min(n, len(_CHUNK))
                w.write(_CHUNK[:k])
                await w.drain()
                n -= k
            return True
        if path == "/redirect":
            w.write(b"HTTP/1.1 302 Found\r\nLocation: /echo\r\nContent-Length: 0\r\n\r\n")
            await w.drain()
            return True
        if path == "/cookies":
            w.write(
                b"HTTP/1.1 200 OK\r\nSet-Cookie: a=1; Path=/; Max-Age=3600; Secure\r\n"
                b"Set-Cookie: b=2; Path=/\r\nContent-Length: 0\r\n\r\n"
            )
            await w.drain()
            return True
        out = json.dumps(
            {
                "method": method,
                "target": target,
                "headers": headers,
                "body_length": len(body),
                "body_sha256": hashlib.sha256(body).hexdigest(),
            }
        ).encode()
        w.write(
            b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n"
            + f"Content-Length: {len(out)}\r\n\r\n".encode()
            + out
        )
        await w.drain()
        return True

    async def _websocket(self, r: asyncio.StreamReader, w: asyncio.StreamWriter, h: dict[str, str]) -> None:
        accept = base64.b64encode(hashlib.sha1(h["sec-websocket-key"].encode() + _WS_GUID).digest()).decode()  # noqa: S324 - RFC 6455
        w.write(
            "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
            f"Sec-WebSocket-Accept: {accept}\r\n\r\n".encode()
        )
        await w.drain()
        message = b""
        mtype = 0
        while True:
            b0, b1 = await r.readexactly(2)
            op, fin = b0 & 0x0F, b0 & 0x80
            n = b1 & 0x7F
            if n == 126:
                (n,) = struct.unpack(">H", await r.readexactly(2))
            elif n == 127:
                (n,) = struct.unpack(">Q", await r.readexactly(8))
            mask = await r.readexactly(4) if b1 & 0x80 else b"\0\0\0\0"
            data = bytearray(await r.readexactly(n))
            for i in range(n):
                data[i] ^= mask[i % 4]
            if op == 8:
                w.write(_ws_frame(8, bytes(data[:2]) or struct.pack(">H", 1000)))
                await w.drain()
                return
            if op == 9:
                w.write(_ws_frame(10, bytes(data)))
            elif op in (1, 2, 0):
                if op:
                    mtype, message = op, b""
                message += data
                if fin:
                    w.write(_ws_frame(mtype, message))
            await w.drain()


def _ws_frame(op: int, data: bytes) -> bytes:
    n = len(data)
    if n < 126:
        head = struct.pack(">BB", 0x80 | op, n)
    elif n < 1 << 16:
        head = struct.pack(">BBH", 0x80 | op, 126, n)
    else:
        head = struct.pack(">BBQ", 0x80 | op, 127, n)
    return head + data
