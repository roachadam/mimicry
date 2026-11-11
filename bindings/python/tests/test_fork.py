"""Decision 0069's fork test: a parent busy with its own core forks ten
children; each drops the parent's connection, starts a core of its own on
first use and makes 100 calls; the parent's core still answers afterwards.
(gunicorn --preload, Celery prefork and multiprocessing's fork all do this.)"""

from __future__ import annotations

import asyncio
import os
import sys
import threading
import time
import warnings

import pytest

import mimicry
from mimicry import Request, aio

from .conftest import config
from .server import Server

pytestmark = pytest.mark.skipif(sys.platform == "win32", reason="no fork on Windows")

CHILDREN = 10
CALLS = 100


def _child(parent: mimicry.Client, server: Server, use_async: bool) -> int:
    """A child's work; its exit status."""
    try:
        parent.do(Request("GET", server.url("/echo")))
    except mimicry.MimicryError as e:
        if "parent process" not in str(e):
            return 2
    else:
        return 3
    if use_async:

        async def main() -> None:
            async with await aio.new_client(config()) as c:
                for _ in range(CALLS):
                    await (await c.do(Request("GET", server.url("/echo")))).read()

        asyncio.run(main())
    else:
        with mimicry.new_client(config()) as c:
            for _ in range(CALLS):
                c.do(Request("GET", server.url("/echo"))).read()
    return 0


def _wait(pid: int, deadline: float) -> int | None:
    assert sys.platform != "win32"
    while time.monotonic() < deadline:
        done, status = os.waitpid(pid, os.WNOHANG)
        if done:
            return os.waitstatus_to_exitcode(status)
        time.sleep(0.05)
    os.kill(pid, 9)
    os.waitpid(pid, 0)
    return None


def test_fork_children_start_their_own_core(server: Server) -> None:
    assert sys.platform != "win32"
    parent = mimicry.new_client(config())
    parent.do(Request("GET", server.url("/echo"))).read()
    stop = threading.Event()
    errors: list[BaseException] = []

    def busy() -> None:
        while not stop.is_set():
            try:
                parent.do(Request("GET", server.url("/echo"))).read()
            except BaseException as e:  # noqa: BLE001 - recorded for the test
                errors.append(e)
                return

    workers = [threading.Thread(target=busy) for _ in range(4)]
    for w in workers:
        w.start()
    pids = []
    try:
        with warnings.catch_warnings():
            # Python 3.12+ warns that forking a threaded process may deadlock
            # the child: that is the case this test is about.
            warnings.simplefilter("ignore", DeprecationWarning)
            for i in range(CHILDREN):
                pid = os.fork()
                if pid == 0:
                    code = 1
                    try:
                        code = _child(parent, server, use_async=i % 2 == 1)
                    except BaseException as e:  # noqa: BLE001 - the child reports and exits
                        print(f"child {i}: {e!r}", file=sys.stderr)
                    finally:
                        os._exit(code)
                pids.append(pid)
        deadline = time.monotonic() + 120
        results = [_wait(pid, deadline) for pid in pids]
    finally:
        stop.set()
        for w in workers:
            w.join(30)
    assert results == [0] * CHILDREN, results
    assert not errors, errors
    assert parent.do(Request("GET", server.url("/echo"))).status_code == 200
    parent.close()
