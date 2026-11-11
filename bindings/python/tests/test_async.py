"""The async API against the test server: what #128 asks each platform to
show, through mimicry-core."""

from __future__ import annotations

import asyncio
import gc
import hashlib
import io
import json
import os
import stat
import sys
import tracemalloc
from collections.abc import AsyncIterator, Awaitable, Callable
from pathlib import Path
from typing import Any

import pytest

import mimicry
from mimicry import Kind, MessageType, Options, Request, aio

from .conftest import config
from .server import Server

POSIX = os.name == "posix"


def run(fn: Callable[[], Awaitable[Any]]) -> Any:
    return asyncio.run(asyncio.wait_for(fn(), 120))


async def echo(resp: aio.AsyncResponse) -> dict[str, Any]:
    assert resp.status_code == 200, resp.status_code
    data: dict[str, Any] = json.loads(await resp.read())
    data["headers"] = {k.lower(): v for k, v in data["headers"]}
    return data


# Sec-Fetch-Dest of a request of each kind, made from a page of its origin.
KINDS = {
    Kind.NAVIGATE: "document",
    Kind.FRAME: "iframe",
    Kind.SCRIPT: "script",
    Kind.ASYNC_SCRIPT: "script",
    Kind.DEFER_SCRIPT: "script",
    Kind.INJECTED_SCRIPT: "script",
    Kind.PRELOAD_SCRIPT: "script",
    Kind.STYLE: "style",
    Kind.IMAGE: "image",
    Kind.INJECTED_IMAGE: "image",
    Kind.FONT: "font",
    Kind.PRELOAD_FONT: "font",
    Kind.FETCH: "empty",
    Kind.XHR: "empty",
    Kind.BEACON: "empty",
}


def test_every_kind(server: Server) -> None:
    """A request of each kind goes out as that kind (its Sec-Fetch-Dest)."""

    async def main() -> None:
        async with await aio.new_client(config()) as c:
            page = server.url("/echo")
            await (await c.do(Request("GET", page))).read()
            assert set(KINDS) == set(Kind) - {Kind.PREFLIGHT}
            for kind, dest in KINDS.items():
                method = "POST" if kind is Kind.BEACON else "GET"
                req = mimicry.with_options(
                    Request(method, server.url(f"/echo?{kind.name}"), body=b"x" if method == "POST" else None),
                    Options(kind=kind, page=None if kind is Kind.NAVIGATE else page),
                )
                got = await echo(await c.do(req))
                assert got["headers"].get("sec-fetch-dest") == dest, (kind, got["headers"])
            got = await echo(await c.do(mimicry.as_kind(Request("GET", server.url("/echo")), Kind.FETCH)))
            assert got["headers"]["sec-fetch-dest"] == "empty"

    run(main)


def test_bodies_order_exact_and_plan(server: Server) -> None:
    """Bodies inline, streamed (async and sync iterables); a caller's header
    order; Exact; Plan."""
    body = os.urandom(300_000)
    want = hashlib.sha256(body).hexdigest()

    async def chunks() -> AsyncIterator[bytes]:
        for i in range(0, len(body), 70_000):
            yield body[i : i + 70_000]
            await asyncio.sleep(0)

    async def main() -> None:
        async with await aio.new_client(config()) as c:
            for b in (body, chunks(), iter([body[:1], body[1:]])):
                req = mimicry.as_kind(Request("POST", server.url("/echo"), body=b), Kind.FETCH)
                got = await echo(await c.do(req))
                assert (got["body_length"], got["body_sha256"]) == (len(body), want)

            req = mimicry.as_kind(
                Request(
                    "GET",
                    server.url("/echo"),
                    headers={"X-B": "2", "X-A": "1"},
                    header_order=["x-a", "accept", "user-agent", "x-b"],
                ),
                Kind.FETCH,
            )
            names = [k for k, _ in json.loads(await (await c.do(req)).read())["headers"]]
            lower = [n.lower() for n in names]
            assert lower.index("x-a") < lower.index("accept") < lower.index("user-agent") < lower.index("x-b")

            ex = mimicry.exact(Request("GET", server.url("/echo"), headers={"Accept": "*/*", "X-Exact": "1"}))
            got = await echo(await c.do(ex))
            assert "user-agent" not in got["headers"] and got["headers"]["x-exact"] == "1"

            plan = await c.plan(mimicry.as_kind(Request("GET", server.url("/api")), Kind.FETCH))
            assert plan.kind is Kind.FETCH and "sec-fetch-mode" in plan.text

    run(main)


def test_redirects(server: Server) -> None:
    """A client follows a redirect, unless told not to; a transport returns
    it. (The server speaks HTTP/1.1 only, over which no profile measured a
    redirected request: following one fails with NotMeasuredError naming
    the hop, which shows it was followed.)"""

    async def main() -> None:
        req = mimicry.as_kind(Request("GET", server.url("/redirect")), Kind.FETCH)
        async with await aio.new_client(config()) as c:
            with pytest.raises(mimicry.NotMeasuredError, match="/echo"):
                await c.do(req)
        async with await aio.new_client(config(), follow_redirects=False) as c:
            r = await c.do(req)
            assert r.status_code == 302 and r.headers["location"] == "/echo"
            await r.read()
        async with await aio.new_transport(config()) as tr:
            r = await tr.round_trip(req)
            assert r.status_code == 302 and r.headers["location"] == "/echo"
            await r.close()

    run(main)


def test_streamed_download_bounded_memory(server: Server) -> None:
    """100 MB streamed through the core while the SDK holds at most a few
    MB: the window per stream bounds what is buffered."""
    total = 100 << 20

    async def main() -> None:
        async with await aio.new_client(config()) as c:
            r = await c.do(Request("GET", server.url(f"/bytes/{total}")))
            assert r.content_length == total
            gc.collect()
            tracemalloc.start()
            try:
                n = 0
                async for chunk in r.aiter_bytes():
                    n += len(chunk)
                peak = tracemalloc.get_traced_memory()[1]
            finally:
                tracemalloc.stop()
            assert n == total
            assert peak < 16 << 20, f"peak {peak} bytes traced while streaming"

    run(main)


def test_cancel_reaches_the_core(server: Server) -> None:
    """Canceling a call resets its stream: the core cancels the request and
    the server sees its connection end. So does closing a body half read."""

    async def main() -> None:
        async with await aio.new_client(config()) as c:
            seen = server.reset("/hang")
            task = asyncio.ensure_future(c.do(Request("GET", server.url("/hang"))))
            while seen.requests == 0:
                await asyncio.sleep(0.01)
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task
            assert await asyncio.to_thread(seen.closed.wait, 10)

            seen = server.reset("/stall")
            r = await c.do(Request("GET", server.url("/stall")))
            assert await r._next_chunk()
            await r.close()
            assert await asyncio.to_thread(seen.closed.wait, 10)
            # The connection still serves other calls.
            await echo(await c.do(Request("GET", server.url("/echo"))))

    run(main)


def test_websocket_echo_and_close(server: Server) -> None:
    async def main() -> None:
        async with await aio.new_client(config()) as c:
            ws = await c.dial_websocket(server.ws_url(), mimicry.WebSocketOptions(page=server.url("/echo")))
            assert ws.response.status_code == 101
            await ws.write_message(MessageType.TEXT, "hello")
            assert await ws.read_message() == (MessageType.TEXT, b"hello")
            big = os.urandom(200_000)
            await ws.write_message(MessageType.BINARY, big)
            assert await ws.read_message() == (MessageType.BINARY, big)
            await ws.close(1000, "")
            with pytest.raises(mimicry.CloseError) as e:
                await ws.read_message()
            assert e.value.close_code == 1000
            with pytest.raises(mimicry.CloseError):
                await ws.read_message()

    run(main)


def test_verify_connection_rejects_a_host(server: Server) -> None:
    """verify_connection (sync and async callables) sees each verified
    connection and fails the ones it rejects."""
    seen: list[str] = []

    def verify(state: mimicry.ConnectionState) -> None:
        seen.append(state.server_name)
        assert state.peer_certificates
        if state.server_name == "localhost":
            raise ValueError("localhost is not allowed")

    async def averify(state: mimicry.ConnectionState) -> None:
        await asyncio.sleep(0)
        verify(state)

    async def main() -> None:
        for fn in (verify, averify):
            async with await aio.new_client(config(verify_connection=fn)) as c:
                await echo(await c.do(Request("GET", server.url("/echo"))))
                with pytest.raises(mimicry.VerifyError, match="localhost is not allowed"):
                    await c.do(Request("GET", server.url("/echo", host="localhost")))

    run(main)
    assert "localhost" in seen and "127.0.0.1" in seen


def test_core_killed_mid_request(server: Server) -> None:
    async def main() -> None:
        async with await aio.AsyncCore.spawn() as core:
            c = await aio.new_client(config(), core=core)
            task = asyncio.ensure_future(c.do(Request("GET", server.url("/hang"))))
            seen = server.reset("/hang")
            while seen.requests == 0:
                await asyncio.sleep(0.01)
            core.kill()
            with pytest.raises(mimicry.CoreExited) as e:
                await task
            assert e.value.returncode not in (None, 0)
            if POSIX:
                assert e.value.returncode == -9
            with pytest.raises(mimicry.CoreExited):
                await c.do(Request("GET", server.url("/echo")))
        # The loop's default core is another one, unharmed.
        async with await aio.new_client(config()) as c2:
            await echo(await c2.do(Request("GET", server.url("/echo"))))

    run(main)


def test_core_exited_status_and_stderr(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """A core that exits says how, with the end of its stderr."""
    if POSIX:
        fake = tmp_path / "fake-core"
        fake.write_text("#!/bin/sh\necho 'mimicry-core: something broke' >&2\nexit 3\n")
        fake.chmod(0o755)
    else:
        fake = tmp_path / "fake-core.bat"
        fake.write_text("@echo mimicry-core: something broke 1>&2\r\n@exit /b 3\r\n")
    monkeypatch.setenv("MIMICRY_CORE", str(fake))

    async def main() -> None:
        with pytest.raises(mimicry.CoreExited) as e:
            await aio.AsyncCore.spawn()
        assert e.value.returncode == 3
        assert "something broke" in e.value.stderr
        assert "status 3" in str(e.value)

    run(main)


def test_core_path_must_be_absolute(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("MIMICRY_CORE", "mimicry-core")
    with pytest.raises(mimicry.MimicryError, match="absolute"):
        run(aio.AsyncCore.spawn)


def test_saved_state(server: Server, tmp_path: Path) -> None:
    """Cookies, tickets, Alt-Svc and client hints save and load; files are
    written 0600 (they hold credentials), replacing what was there."""

    async def main() -> None:
        jar = await aio.new_jar(rules=mimicry.profiles.chrome_macos())
        tickets = await aio.new_ticket_cache()
        alt = await aio.new_alt_svc_cache()
        hints = await aio.new_client_hint_store()
        async with await aio.new_client(
            config(cookies=jar, tickets=tickets, alt_svc=alt, client_hint_store=hints)
        ) as c:
            assert c.cookies is jar
            await (await c.do(Request("GET", server.url("/cookies")))).read()
        names = sorted(ck.name for ck in await jar.all())
        assert names == [b"a", b"b"]
        await hints.set_client_hints("https://127.0.0.1", ["sec-ch-ua-arch"])

        stale = tmp_path / "cookies.json"
        stale.write_text("old")
        stale.chmod(0o644)
        for obj, name in (
            (jar, "cookies.json"),
            (tickets, "tickets.json"),
            (alt, "altsvc.json"),
            (hints, "hints.json"),
        ):
            path = tmp_path / name
            await obj.write_json(path)
            if POSIX:
                assert stat.S_IMODE(path.stat().st_mode) == 0o600, name
            buf = io.BytesIO()
            await obj.write_json(buf)
            assert buf.getvalue() == path.read_bytes()
        await jar.write_netscape(tmp_path / "cookies.txt")
        if POSIX:
            assert stat.S_IMODE((tmp_path / "cookies.txt").stat().st_mode) == 0o600

        jar2 = await aio.new_jar()
        await jar2.read_json(tmp_path / "cookies.json")
        assert sorted(ck.name for ck in await jar2.all()) == names
        jar3 = await aio.new_jar()
        await jar3.read_netscape(tmp_path / "cookies.txt")
        assert sorted(ck.name for ck in await jar3.all()) == names
        hints2 = await aio.new_client_hint_store()
        await hints2.read_json(tmp_path / "hints.json")
        assert await hints2.client_hints("https://127.0.0.1") == ["sec-ch-ua-arch"]
        for obj in (jar, jar2, jar3, tickets, alt, hints, hints2):
            await obj.close()

    run(main)


def test_jar_and_set_cookie_events(server: Server) -> None:
    events: list[mimicry.SetCookieEvent] = []

    async def main() -> None:
        jar = await aio.new_jar(on_set_cookie=events.append)
        async with await aio.new_client(config(cookies=jar)) as c:
            await (await c.do(Request("GET", server.url("/cookies")))).read()
            await asyncio.sleep(0.05)
            req = mimicry.CookieRequest(url=server.url("/x"), navigation=True)
            assert sorted(x.name for x in await jar.cookies(req)) == [b"a", b"b"]
            await jar.set_string("example.com", "k=v")
            assert await jar.string("example.com") == "k=v"
            assert await jar.clear_session() >= 1
            clone = await jar.clone()
            assert [x.name for x in await clone.all()] == [b"a"]

    run(main)
    assert sorted(e.line.split(b";")[0] for e in events) == [b"a=1", b"b=2"]


def test_errors(server: Server) -> None:
    async def main() -> None:
        async with await aio.new_client(config()) as c:
            with pytest.raises(mimicry.InvalidError) as e:
                await c.do(Request("GET", "http://user:secret@[::1"))
            assert "secret" not in str(e.value) and isinstance(e.value, ValueError)
            with pytest.raises(mimicry.NetError) as ne:
                await c.do(Request("GET", "https://127.0.0.1:1/"))
            assert isinstance(ne.value, ConnectionError)
            with pytest.raises(mimicry.DeadlineError) as de:
                await c.do(Request("GET", server.url("/hang"), timeout=0.2))
            assert isinstance(de.value, TimeoutError)
        with pytest.raises(mimicry.ValidationError) as ve:
            await aio.parse_profile(b'{"name": "x"}')
        assert ve.value.problems
        with pytest.raises(mimicry.MimicryError, match="no-such-profile"):
            await aio.new_client(mimicry.Config(profile="no-such-profile"))

    run(main)


def test_profiles_and_machines() -> None:
    async def main() -> None:
        lst = await aio.list_profiles()
        assert set(lst.supported) <= set(lst.names)
        name = mimicry.profiles.chrome_macos().resolve(lst.supported)
        info, data = await aio.profile_spec(name)
        assert info.name == name and "Chrome" in info.user_agent
        p = await aio.parse_profile(data)
        assert (p.name, p.user_agent) == (info.name, info.user_agent)
        async with await aio.new_client(mimicry.Config(profile=p)) as c:
            assert c is not None
        assert await aio.machines("macos")

    run(main)


def test_many_concurrent_calls(server: Server) -> None:
    """More calls at once than the protocol's 256 streams: openers wait for
    a free stream."""

    async def main() -> None:
        async with await aio.new_client(config()) as c:

            async def one(i: int) -> int:
                n: int = (await echo(await c.do(Request("GET", server.url(f"/echo?{i}")))))["body_length"]
                return n

            assert await asyncio.gather(*(one(i) for i in range(400))) == [0] * 400

    run(main)


def test_large_call_is_continued(server: Server) -> None:
    """A call over 64 KiB (a large header) goes as DATA (Call.continued)."""

    async def main() -> None:
        async with await aio.new_client(config()) as c:
            v = "v" * 100_000
            got = await echo(await c.do(Request("GET", server.url("/echo"), headers={"X-Big": v})))
            assert got["headers"]["x-big"] == v

    run(main)


def test_objects_refuse_another_loop(server: Server) -> None:
    holder: list[aio.AsyncClient] = []

    async def make() -> None:
        holder.append(await aio.new_client(config()))

    run(make)

    async def use() -> None:
        with pytest.raises(mimicry.MimicryError, match="another event loop"):
            await holder[0].do(Request("GET", server.url("/echo")))

    run(use)


def test_dropped_responses_free_their_streams(server: Server) -> None:
    """A response collected unread resets its call (weakref.finalize), so
    more calls than the protocol's 256 streams never wait forever."""

    async def main() -> None:
        async with await aio.new_client(config()) as c:
            for _ in range(300):
                r = await c.do(Request("GET", server.url(f"/bytes/{1 << 20}")))
                del r
                gc.collect()
            await echo(await asyncio.wait_for(c.do(Request("GET", server.url("/echo"))), 10))

    run(main)


def test_core_exits_when_closed() -> None:
    """Closing the connection is the core's EOF: it frees everything and
    exits cleanly."""

    async def main() -> None:
        core = await aio.AsyncCore.spawn()
        await aio.new_client(config(), core=core)
        await core.close()
        assert core._proc is not None and core._proc.returncode == 0

    run(main)


@pytest.mark.skipif(not sys.platform.startswith("linux"), reason="reads /proc")
def test_only_the_childs_end_is_inherited() -> None:
    """The core holds one end of the socketpair, as its stdin and stdout, and
    not the parent's (both ends are close-on-exec)."""

    async def main() -> None:
        async with await aio.AsyncCore.spawn() as core:
            fds = Path(f"/proc/{core.pid}/fd")
            links = {int(p.name): os.readlink(p) for p in fds.iterdir()}
            sockets = {v for v in links.values() if v.startswith("socket:")}
            assert links[0] == links[1] and links[0].startswith("socket:")
            parent = os.readlink(f"/proc/self/fd/{core._fds[0]}")
            assert parent != links[0] and parent not in sockets

    run(main)
