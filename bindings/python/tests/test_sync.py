"""The sync API: the same protocol driven from one background thread."""

from __future__ import annotations

import concurrent.futures
import hashlib
import json
import os
import stat
import threading
import tracemalloc
from collections.abc import Iterator
from pathlib import Path

import pytest

import mimicry
from mimicry import Kind, MessageType, Request

from .conftest import config
from .server import Server


def echo(resp: mimicry.Response) -> dict[str, object]:
    assert resp.status_code == 200
    data: dict[str, object] = json.loads(resp.read())
    return data


def test_do_and_streamed_upload(client: mimicry.Client, server: Server) -> None:
    body = os.urandom(200_000)

    def gen() -> Iterator[bytes]:
        yield body[:100]
        yield body[100:]

    for b in (body, gen()):
        got = echo(client.do(mimicry.as_kind(Request("PUT", server.url("/echo"), body=b), Kind.FETCH)))
        assert got["body_sha256"] == hashlib.sha256(body).hexdigest()


def test_streamed_download_bounded_memory(client: mimicry.Client, server: Server) -> None:
    total = 100 << 20
    with client.do(Request("GET", server.url(f"/bytes/{total}"))) as r:
        tracemalloc.start()
        try:
            n = sum(len(c) for c in r.iter_bytes())
            peak = tracemalloc.get_traced_memory()[1]
        finally:
            tracemalloc.stop()
    assert n == total
    assert peak < 16 << 20, f"peak {peak} bytes traced while streaming"


def test_cancel_by_closing(client: mimicry.Client, server: Server) -> None:
    seen = server.reset("/stall")
    r = client.do(Request("GET", server.url("/stall")))
    it = r.iter_bytes()
    assert next(it)
    r.close()
    assert seen.closed.wait(10)


def test_threads_share_the_driver(client: mimicry.Client, server: Server) -> None:
    def one(i: int) -> int:
        return len(client.do(Request("GET", server.url(f"/echo?{i}"))).read())

    with concurrent.futures.ThreadPoolExecutor(64) as ex:
        assert all(n > 0 for n in ex.map(one, range(640)))


def test_websocket(client: mimicry.Client, server: Server) -> None:
    with client.dial_websocket(server.ws_url()) as ws:
        ws.write_message(MessageType.BINARY, b"\x00\x01")
        assert ws.read_message() == (MessageType.BINARY, b"\x00\x01")
        ws.close(1000, "")
        with pytest.raises(mimicry.CloseError) as e:
            ws.read_message()
        assert e.value.close_code == 1000


def test_callbacks_run_off_the_loop(server: Server) -> None:
    """verify_connection and on_set_cookie run on worker threads: they may
    block and call the sync API."""
    threads: list[str] = []

    def verify(state: mimicry.ConnectionState) -> None:
        threads.append(threading.current_thread().name)
        assert mimicry.list_profiles().names
        if state.server_name == "localhost":
            raise PermissionError("no")

    events = threading.Event()

    def on_set_cookie(e: mimicry.SetCookieEvent) -> None:
        threads.append(threading.current_thread().name)
        events.set()

    jar = mimicry.new_jar(on_set_cookie=on_set_cookie)
    with mimicry.new_client(config(verify_connection=verify, cookies=jar)) as c:
        c.do(Request("GET", server.url("/cookies"))).read()
        with pytest.raises(mimicry.VerifyError):
            c.do(Request("GET", server.url("/echo", host="localhost")))
    assert events.wait(5)
    assert threads and "mimicry" not in threads


def test_saved_state_0600(client: mimicry.Client, server: Server, tmp_path: Path) -> None:
    client.do(Request("GET", server.url("/cookies"))).read()
    path = tmp_path / "jar.json"
    client.cookies.write_json(path)
    if os.name == "posix":
        assert stat.S_IMODE(path.stat().st_mode) == 0o600
    jar = mimicry.new_jar()
    jar.read_json(str(path))
    assert sorted(c.name for c in jar.all()) == [b"a", b"b"]


def test_core_killed(server: Server) -> None:
    with mimicry.Core.spawn() as core:
        c = mimicry.new_client(config(), core=core)
        seen = server.reset("/hang")
        out: list[BaseException] = []

        def call() -> None:
            try:
                c.do(Request("GET", server.url("/hang")))
            except BaseException as e:  # noqa: BLE001 - recorded for the test
                out.append(e)

        th = threading.Thread(target=call)
        th.start()
        while seen.requests == 0:
            threading.Event().wait(0.01)
        core.kill()
        th.join(10)
        assert out and isinstance(out[0], mimicry.CoreExited)
        assert out[0].returncode not in (None, 0)


def test_profile_names_resolve() -> None:
    lst = mimicry.list_profiles()
    for fn in (mimicry.profiles.chrome_macos, mimicry.profiles.firefox_windows, mimicry.profiles.safari_ios):
        name = fn().resolve(lst.supported)
        assert name in lst.supported and name.startswith(fn().browser)
