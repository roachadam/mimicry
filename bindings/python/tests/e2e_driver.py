"""The Python side of the harness's TestPythonSDKMatchesGo*
(internal/harness/e2e/python_test.go): a page's requests and WebSockets sent
through the SDK's async API, as the Go test asks.

One JSON command per line on stdin, ``{"id", "op", "args"}``; one answer per
line on stdout, ``{"id", "result"}`` or ``{"id", "error"}``. Commands run
concurrently (a WebSocket's reads wait while its writes go on). The core is
``$MIMICRY_CORE``, the one the test built. Bytes cross as base64 (Go's
encoding of []byte in JSON).
"""

from __future__ import annotations

import asyncio
import base64
import json
import sys
import threading
from typing import Any

import mimicry
from mimicry import Kind, MessageType, Options, Place, Request, aio


class Driver:
    def __init__(self, loop: asyncio.AbstractEventLoop) -> None:
        self.loop = loop
        self.client: aio.AsyncClient | None = None
        self.sockets: dict[int, aio.AsyncWebSocket] = {}
        self.out = threading.Lock()

    def answer(self, msg: dict[str, Any]) -> None:
        line = json.dumps(msg) + "\n"
        with self.out:
            sys.stdout.write(line)
            sys.stdout.flush()

    async def run(self, cmd: dict[str, Any]) -> None:
        try:
            result = await getattr(self, "op_" + cmd["op"])(cmd.get("args") or {})
            self.answer({"id": cmd["id"], "result": result})
        except Exception as e:  # noqa: BLE001 - every failure is the test's to report
            self.answer({"id": cmd["id"], "error": f"{type(e).__name__}: {e}"})

    async def op_init(self, a: dict[str, Any]) -> None:
        profile = await aio.parse_profile(a["profile"])
        self.client = await aio.new_client(
            mimicry.Config(
                profile=profile,
                root_cas=a["root_cas"].encode(),
                dns_server=a.get("dns_server", ""),
                proxy=a.get("proxy", ""),
                browser_proxy_auth=a.get("browser_proxy_auth", False),
            )
        )

    async def op_send(self, a: dict[str, Any]) -> None:
        assert self.client is not None
        o = a["options"]
        body: Any = a["body"].encode() if a["body"] else None
        if a["stream"]:
            body = iter([a["body"].encode()])
        req = Request(
            a["method"],
            a["url"],
            headers=[(name, value) for name, value in a["headers"]],
            body=body,
            header_order=a["header_order"],
            options=Options(
                kind=Kind(o["kind"]),
                page=o["page"] or None,
                priority=mimicry.Priority(o["priority"]),
                cross_origin=mimicry.CrossOrigin(o["cross_origin"]),
                credentials=mimicry.Credentials(o["credentials"]),
                no_cors=o["no_cors"],
                stylesheet=o["stylesheet"] or None,
                ignore_cors=o["ignore_cors"],
                header_placement=[Place(p["header"], p["after"], p["before"]) for p in o["header_placement"] or []],
                client_hints=mimicry.HintDelegation(o["client_hints"]),
            ),
        )
        if a["exact"]:
            req = mimicry.exact(req)
        resp = await self.client.do(req)
        await resp.read()

    async def op_dial(self, a: dict[str, Any]) -> int:
        assert self.client is not None
        ws = await self.client.dial_websocket(
            a["url"],
            mimicry.WebSocketOptions(
                page=a["page"] or None,
                protocols=a["protocols"] or (),
                header=[(name, value) for name, value in a["headers"]],
            ),
        )
        sid = len(self.sockets) + 1
        self.sockets[sid] = ws
        return sid

    async def op_write(self, a: dict[str, Any]) -> None:
        data = base64.b64decode(a["data"] or "")
        await self.sockets[a["id"]].write_message(MessageType.BINARY if a["binary"] else MessageType.TEXT, data)

    async def op_read(self, a: dict[str, Any]) -> str:
        _, data = await self.sockets[a["id"]].read_message()
        return base64.b64encode(data).decode()

    async def op_close(self, a: dict[str, Any]) -> None:
        await self.sockets[a["id"]].close(1000, "")


def main() -> None:
    loop = asyncio.new_event_loop()
    d = Driver(loop)
    tasks: set[asyncio.Future[None]] = set()

    def read() -> None:
        for line in sys.stdin:
            if line.strip():
                f = asyncio.run_coroutine_threadsafe(d.run(json.loads(line)), loop)
                tasks.add(f)  # type: ignore[arg-type]
        loop.call_soon_threadsafe(loop.stop)

    threading.Thread(target=read, daemon=True).start()
    loop.run_forever()
    if d.client is not None:
        loop.run_until_complete(d.client.close())
    loop.run_until_complete(d.client.core.close() if d.client is not None else asyncio.sleep(0))
    loop.close()


if __name__ == "__main__":
    main()
