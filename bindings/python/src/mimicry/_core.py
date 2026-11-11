"""The process's connection to mimicry-core (decision 0069): spawning it,
the asyncio driver of the wire protocol, calls, notices and reverse calls.

On Linux and macOS the core's stdin and stdout are one end of a socketpair
(both ends close-on-exec, 4 MiB buffers both ways), read through an asyncio
``BufferedProtocol`` into one preallocated buffer. On Windows they are two
anonymous pipes (``subprocess.PIPE``: ``CreatePipe``), never
``socket.socketpair()`` (a loopback TCP pair, CVE-2024-3219), driven by a
reader and a writer thread that hand bytes to the event loop.

A forked child never touches its parent's core: :func:`_after_fork` points
the inherited descriptors at ``/dev/null`` and every object made before the
fork refuses to be used; the child's first call starts its own core.
"""

from __future__ import annotations

import asyncio
import inspect
import io
import os
import socket
import subprocess
import sys
import threading
import weakref
from collections import deque
from collections.abc import Awaitable, Callable
from pathlib import Path
from typing import Any, BinaryIO

from . import _wire as wire
from ._errors import CanceledError, CoreExited, MimicryError, ProtocolError, from_pb
from ._pb.mimicry.v1 import mimicry_pb2 as pb
from ._version import __version__

PROTOCOL_MAJOR = 1
PROTOCOL_MINOR = 0

_SOCKET_BUFFER = 4 << 20
"""Both ends' SO_SNDBUF/SO_RCVBUF: macOS's 8 KiB default caps bodies near
1 GB/s (decision 0069, Evidence)."""

_READ_BUFFER = 1 << 20
_STDERR_TAIL = 4 << 10
_WINDOWS = sys.platform == "win32"


_PIPE_BUFFER = 1 << 20


if sys.platform == "win32":
    import _winapi
    import msvcrt

    def _pipe() -> tuple[int, int]:
        """An anonymous pipe, as file descriptors: read end, write end."""
        r, w = _winapi.CreatePipe(None, _PIPE_BUFFER)
        return msvcrt.open_osfhandle(r, 0), msvcrt.open_osfhandle(w, 0)


def core_path() -> str:
    """The core to start: ``$MIMICRY_CORE`` (an absolute path, for development
    and tests), else the binary inside the installed package. Never a lookup
    in ``PATH``."""
    env = os.environ.get("MIMICRY_CORE")
    if env:
        if not os.path.isabs(env):
            raise MimicryError(f"MIMICRY_CORE must be an absolute path, not {env!r}")
        return env
    path = Path(__file__).resolve().parent / "_bin" / ("mimicry-core.exe" if _WINDOWS else "mimicry-core")
    if not path.is_file():
        raise MimicryError(
            f"mimicry-core is not installed at {path}: install a wheel for this platform, "
            f"or build it from the repository: go build -o {path} ./cmd/mimicry-core"
        )
    return str(path)


class _Stderr:
    """Keeps the last bytes the core wrote to stderr, read on a thread of its
    own so a core writing a lot there (a crash) never blocks on a full pipe;
    then reaps the process."""

    def __init__(self, proc: subprocess.Popen[bytes]) -> None:
        self.proc = proc
        self.tail = bytearray()
        self.done = threading.Event()
        threading.Thread(target=self._run, name="mimicry-core stderr", daemon=True).start()

    def _run(self) -> None:
        f = self.proc.stderr
        try:
            while f is not None:
                b = f.read1(_STDERR_TAIL) if hasattr(f, "read1") else f.read(_STDERR_TAIL)
                if not b:
                    break
                self.tail += b
                del self.tail[: max(0, len(self.tail) - _STDERR_TAIL)]
        except (OSError, ValueError):
            pass
        try:
            self.proc.wait()
        finally:
            self.done.set()

    def text(self) -> str:
        return bytes(self.tail).decode(errors="replace")


class _Protocol(asyncio.BufferedProtocol):
    """Reads the core's bytes into one preallocated buffer: asyncio's
    StreamReader caps bodies at 0.5 to 1 GB/s (decision 0069, Evidence)."""

    def __init__(self, core: AsyncCore) -> None:
        self._core = core
        self._mv = memoryview(bytearray(_READ_BUFFER))

    def connection_made(self, transport: asyncio.BaseTransport) -> None:
        self._core._transport = transport  # type: ignore[assignment]

    def get_buffer(self, sizehint: int) -> memoryview:
        return self._mv

    def buffer_updated(self, nbytes: int) -> None:
        self._core._received(self._mv[:nbytes])

    def data_received(self, data: bytes) -> None:
        self._core._received(data)

    def eof_received(self) -> bool:
        self._core._events(self._core._wc.receive_eof())
        return False

    def connection_lost(self, exc: Exception | None) -> None:
        core = self._core
        if exc is None:
            core._events(core._wc.receive_eof())
        else:
            core._events(core._wc.receive_error(exc))
        core._resume()

    def pause_writing(self) -> None:
        self._core._paused = True

    def resume_writing(self) -> None:
        self._core._resume()


class _PipeTransport(asyncio.Transport):
    """The core's stdout and stdin pipes as a transport (Windows): a thread
    reads, another writes, both handing over through the loop, the reader
    waking it once for whatever piled up meanwhile."""

    _HIGH = 4 << 20
    _LOW = 1 << 20

    def __init__(
        self, loop: asyncio.AbstractEventLoop, reader: BinaryIO, writer: BinaryIO, protocol: _Protocol
    ) -> None:
        super().__init__()
        self._loop = loop
        self._reader = reader
        self._writer = writer
        self._protocol = protocol
        self._cv = threading.Condition()
        self._q: deque[bytes] = deque()
        self._queued = 0
        self._paused = False
        self._closing = False
        self._lost = False
        self._inbox: deque[bytes] = deque()
        self._inbox_lock = threading.Lock()
        self._delivering = False
        protocol.connection_made(self)
        threading.Thread(target=self._read, name="mimicry-core reader", daemon=True).start()
        threading.Thread(target=self._write, name="mimicry-core writer", daemon=True).start()

    def _read(self) -> None:
        f = self._reader
        mv = memoryview(bytearray(_READ_BUFFER))
        exc: Exception | None = None
        try:
            while True:
                n = f.readinto(mv)  # type: ignore[attr-defined]
                if not n:
                    break
                with self._inbox_lock:
                    self._inbox.append(bytes(mv[:n]))
                    wake = not self._delivering
                    self._delivering = True
                if wake:
                    self._call(self._deliver)
        except (OSError, ValueError) as e:
            exc = e
        try:
            f.close()
        except OSError:
            pass
        self._call(self._connection_lost, exc)

    def _deliver(self) -> None:
        while True:
            with self._inbox_lock:
                if not self._inbox:
                    self._delivering = False
                    return
                data = self._inbox.popleft()
            self._protocol.data_received(data)

    def _write(self) -> None:
        f = self._writer
        exc: Exception | None = None
        try:
            while True:
                with self._cv:
                    while not self._q and not self._closing:
                        self._cv.wait()
                    if not self._q:
                        break
                    b = self._q.popleft() if len(self._q) == 1 else b"".join(self._q)
                    self._q.clear()
                view = memoryview(b)
                while view:
                    n = f.write(view)
                    view = view[n or 0 :]
                with self._cv:
                    self._queued -= len(b)
                    resume = self._paused and self._queued <= self._LOW
                    if resume:
                        self._paused = False
                if resume:
                    self._call(self._protocol.resume_writing)
        except (OSError, ValueError) as e:
            exc = e
        try:
            f.close()
        except OSError:
            pass
        if exc is not None:
            self._call(self._connection_lost, exc)

    def _call(self, fn: Callable[..., Any], *args: Any) -> None:
        try:
            self._loop.call_soon_threadsafe(fn, *args)
        except RuntimeError:
            pass  # the loop is closed

    def _connection_lost(self, exc: Exception | None) -> None:
        if self._lost:
            return
        self._lost = True
        self.close()
        self._protocol.connection_lost(exc)

    def write(self, data: bytes | bytearray | memoryview) -> None:
        if self._closing or not data:
            return
        b = bytes(data)
        with self._cv:
            self._q.append(b)
            self._queued += len(b)
            pause = not self._paused and self._queued > self._HIGH
            if pause:
                self._paused = True
            self._cv.notify()
        if pause:
            self._protocol.pause_writing()

    def close(self) -> None:
        with self._cv:
            self._closing = True
            self._cv.notify()

    def is_closing(self) -> bool:
        return self._closing

    def abort(self) -> None:
        self.close()


class Call:
    """One call's stream: its reply, then DATA either way."""

    __slots__ = ("__weakref__", "_core", "_ev", "_s")

    def __init__(self, core: AsyncCore, s: wire.Stream) -> None:
        self._core = core
        self._s = s
        self._ev = asyncio.Event()
        core._waits[s.id] = self._ev

    @property
    def id(self) -> int:
        return self._s.id

    @property
    def ended(self) -> bool:
        return self._s.ended

    async def _raise(self, e: wire.WireError) -> None:
        raise await self._core._error(e, self._s.id) from None

    async def reply(self) -> pb.Reply:
        """The reply, read from DATA when it was too large for a frame."""
        core = self._core
        while True:
            self._ev.clear()
            try:
                b = core._wc.reply(self._s)
            except wire.WireError as e:
                await self._raise(e)
            if b is not None:
                break
            await self._ev.wait()
        r = pb.Reply.FromString(b)
        if r.continued:
            r = pb.Reply.FromString(await self.read_exactly(r.continued))
        return r

    async def read(self, max_bytes: int = -1) -> bytes:
        """Up to ``max_bytes`` of the core's DATA (the next chunk, if
        negative); ``b""`` after its END."""
        core = self._core
        while True:
            self._ev.clear()
            try:
                b = core._wc.read(self._s, max_bytes)
            except wire.WireError as e:
                await self._raise(e)
            if b is not None:
                if b:
                    core._flush()
                return b
            await self._ev.wait()

    async def read_exactly(self, n: int) -> bytes:
        parts = []
        while n > 0:
            b = await self.read(n)
            if not b:
                raise ProtocolError("mimicry-core ended a message early")
            parts.append(b)
            n -= len(b)
        return b"".join(parts)

    async def read_all(self) -> bytes:
        parts = []
        while b := await self.read():
            parts.append(b)
        return b"".join(parts)

    async def write(self, data: bytes | bytearray | memoryview) -> None:
        """Send DATA, waiting for credit and for the carrier to drain."""
        core = self._core
        mv = memoryview(data)
        while mv:
            self._ev.clear()
            try:
                n = core._wc.send_data(self._s, mv)
            except wire.WireError as e:
                await self._raise(e)
            if n:
                mv = mv[n:]
                core._flush()
                if core._paused:
                    await core._drain()
                continue
            await self._ev.wait()

    async def end(self) -> None:
        """End this side's DATA."""
        try:
            self._core._wc.end_stream(self._s)
        except wire.WireError as e:
            await self._raise(e)
        self._core._flush()

    async def reply_to(self, payload: bytes) -> None:
        """Answer a reverse call, ending the stream."""
        try:
            self._core._wc.send_reply(self._s, payload, True)
        except wire.WireError as e:
            await self._raise(e)
        self._core._flush()

    def close(self) -> None:
        """Done with the call: reset it unless both sides ended it."""
        core = self._core
        core._waits.pop(self._s.id, None)
        core._stream_errors.pop(self._s.id, None)
        if not self._s.ended and core._wc.error is None:
            core._wc.reset_stream(self._s)
            core._flush()


def _abandon(ref: weakref.ref[AsyncCore], call: Call) -> None:
    """A finalizer's: close ``call`` on its loop (finalizers run anywhere)."""
    core = ref()
    if core is None or core._forked or core._loop.is_closed():
        return
    try:
        core._loop.call_soon_threadsafe(call.close)
    except RuntimeError:
        pass


def abandon_on_collect(owner: object, call: Call) -> weakref.finalize:  # type: ignore[type-arg]
    """Reset ``call`` if ``owner`` is collected with it still open."""
    return weakref.finalize(owner, _abandon, weakref.ref(call._core), call)


_cores: weakref.WeakSet[AsyncCore] = weakref.WeakSet()


class AsyncCore:
    """One connection to a mimicry-core: usually a child this process started
    (:meth:`spawn`), or any connected socket speaking the protocol
    (:meth:`connect`). Objects belong to the core that made them, and the
    core frees them all when the connection ends."""

    def __init__(self, loop: asyncio.AbstractEventLoop) -> None:
        self._loop = loop
        self._wc = wire.Connection(wire.Role.SDK)
        self._transport: asyncio.Transport | None = None
        self._proc: subprocess.Popen[bytes] | None = None
        self._stderr: _Stderr | None = None
        self._fds: list[int] = []
        self._waits: dict[int, asyncio.Event] = {}
        self._openers: deque[asyncio.Future[None]] = deque()
        self._drainers: list[asyncio.Future[None]] = []
        self._paused = False
        self._closing = False
        self._forked = False
        self._failure: asyncio.Future[MimicryError] = loop.create_future()
        self._stream_errors: dict[int, pb.Error] = {}
        self._written: dict[tuple[int, int], asyncio.Future[pb.Error | None]] = {}
        self._written_early: dict[tuple[int, int], pb.Error | None] = {}
        self._set_cookie: dict[int, Callable[[pb.SetCookie], None]] = {}
        self._verify: dict[int, Callable[[pb.VerifyConnection], Awaitable[None]]] = {}
        self._tasks: set[asyncio.Task[Any]] = set()
        self.core_version = ""
        self.pid: int | None = None
        _cores.add(self)

    # -- starting -------------------------------------------------------

    @classmethod
    async def spawn(cls) -> AsyncCore:
        """Start a core as a child of this process and say Hello."""
        loop = asyncio.get_running_loop()
        core = cls(loop)
        path = core_path()
        # The core, by absolute path; Popen's fork-exec is brief enough to
        # run on the loop.
        if sys.platform == "win32":
            # Anonymous pipes (CreatePipe), as subprocess.PIPE makes them but
            # with 1 MiB buffers: the default few KiB cap bodies far below
            # the carrier's speed. Neither parent end is inheritable; Popen
            # passes the child's ends alone (close_fds: a handle list).
            stdin_r, stdin_w = _pipe()
            stdout_r, stdout_w = _pipe()
            try:
                proc = subprocess.Popen(  # noqa: ASYNC220
                    [path],
                    stdin=stdin_r,
                    stdout=stdout_w,
                    stderr=subprocess.PIPE,
                    close_fds=True,
                )
            except BaseException:
                os.close(stdin_w)
                os.close(stdout_r)
                raise
            finally:
                os.close(stdin_r)
                os.close(stdout_w)
            core._attach_process(proc)
            _PipeTransport(loop, io.FileIO(stdout_r, "rb"), io.FileIO(stdin_w, "wb"), _Protocol(core))
        else:
            parent, child = socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM)
            try:
                # Python makes both ends non-inheritable (PEP 446); only the
                # child's end is passed, as its stdin and stdout.
                for s in (parent, child):
                    s.setsockopt(socket.SOL_SOCKET, socket.SO_SNDBUF, _SOCKET_BUFFER)
                    s.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, _SOCKET_BUFFER)
                proc = subprocess.Popen(  # noqa: ASYNC220
                    [path],
                    stdin=child.fileno(),
                    stdout=child.fileno(),
                    stderr=subprocess.PIPE,
                    close_fds=True,
                )
            except BaseException:
                parent.close()
                raise
            finally:
                child.close()
            core._attach_process(proc)
            core._fds.append(parent.fileno())
            await loop.create_unix_connection(lambda: _Protocol(core), sock=parent)
        await core._hello()
        return core

    @classmethod
    async def connect(cls, sock: socket.socket) -> AsyncCore:
        """Speak to a core over ``sock``, a connected stream socket this
        process did not start the core for, and say Hello."""
        loop = asyncio.get_running_loop()
        core = cls(loop)
        core._fds.append(sock.fileno())
        if sock.family == getattr(socket, "AF_UNIX", None):
            await loop.create_unix_connection(lambda: _Protocol(core), sock=sock)
        else:
            await loop.create_connection(lambda: _Protocol(core), sock=sock)
        await core._hello()
        return core

    def _attach_process(self, proc: subprocess.Popen[bytes]) -> None:
        self._proc = proc
        self.pid = proc.pid
        self._stderr = _Stderr(proc)
        if proc.stderr is not None:
            self._fds.append(proc.stderr.fileno())

    async def _hello(self) -> None:
        r = await self.call(
            pb.Call(
                hello=pb.Hello(
                    protocol_major=PROTOCOL_MAJOR,
                    protocol_minor=PROTOCOL_MINOR,
                    sdk_version=f"python {__version__}",
                )
            )
        )
        self.core_version = r.hello.core_version
        if r.hello.protocol_major != PROTOCOL_MAJOR:
            raise ProtocolError(
                f"mimicry-core {self.core_version} speaks protocol "
                f"{r.hello.protocol_major}.{r.hello.protocol_minor}, this SDK {PROTOCOL_MAJOR}.{PROTOCOL_MINOR}"
            )

    # -- the connection's state -------------------------------------------

    @property
    def usable(self) -> bool:
        """Neither failed, closed nor inherited across a fork."""
        return not self._forked and not self._closing and self._wc.error is None

    def check(self) -> None:
        """Refuse to work on a core this process cannot use."""
        if self._forked:
            raise MimicryError(
                "this object belongs to mimicry-core of the parent process, which a forked "
                "child cannot use: make the child's own objects after the fork"
            )
        if asyncio.get_running_loop() is not self._loop:
            raise MimicryError("this object belongs to another event loop's mimicry-core")

    async def close(self) -> None:
        """Close the connection: the core frees every object and exits."""
        if self._closing or self._forked:
            return
        self._closing = True
        self._events(self._wc.close())
        if self._transport is not None:
            self._transport.close()
        if self._stderr is not None:
            await asyncio.to_thread(self._stderr.done.wait, 5)

    async def __aenter__(self) -> AsyncCore:
        return self

    async def __aexit__(self, *exc: object) -> None:
        await self.close()

    def kill(self) -> None:
        """Kill the core process (tests: a core that dies mid-request)."""
        if self._proc is not None:
            self._proc.kill()

    # -- calls ----------------------------------------------------------

    async def open(self, call: pb.Call, body: bool = False) -> Call:
        """Open a call; ``body`` says DATA follows it (end it with
        :meth:`Call.end`). A call too large for a frame goes as DATA."""
        self.check()
        payload = call.SerializeToString()
        big = len(payload) > wire.MAX_PAYLOAD
        head = pb.Call(continued=len(payload)).SerializeToString() if big else payload
        while not self._wc.can_open():
            f = self._loop.create_future()
            self._openers.append(f)
            try:
                await f
            finally:
                if not f.done():
                    f.cancel()
        try:
            s = self._wc.open_stream(head, end=not body and not big)
        except wire.WireError as e:
            raise await self._error(e, 0) from None
        c = Call(self, s)
        self._flush()
        if big:
            try:
                await c.write(payload)
                if not body:
                    await c.end()
            except BaseException:
                c.close()
                raise
        return c

    async def call(self, call: pb.Call) -> pb.Reply:
        """A call with no body: its reply, an error raised."""
        c = await self.open(call)
        try:
            r = await c.reply()
            if not c.ended:
                await c.read_all()
        finally:
            c.close()
        if r.HasField("error"):
            raise from_pb(r.error)
        return r

    async def call_with_body(self, call: pb.Call, body: bytes) -> pb.Reply:
        """A call whose DATA is ``body`` (ReadJSON, ParseProfile)."""
        c = await self.open(call, body=True)
        try:
            await c.write(body)
            await c.end()
            r = await c.reply()
            if not c.ended:
                await c.read_all()
        finally:
            c.close()
        if r.HasField("error"):
            raise from_pb(r.error)
        return r

    async def call_for_body(self, call: pb.Call) -> tuple[pb.Reply, bytes]:
        """A call whose reply is followed by DATA (WriteJSON, ProfileSpec)."""
        c = await self.open(call)
        try:
            r = await c.reply()
            if r.HasField("error"):
                raise from_pb(r.error)
            return r, await c.read_all()
        finally:
            c.close()

    async def written(self, websocket: int, seq: int) -> pb.Error | None:
        """Wait for the WebSocketWritten notice of a socket's seq-th message."""
        key = (websocket, seq)
        if key in self._written_early:
            return self._written_early.pop(key)
        f = self._written.get(key)
        if f is None:
            f = self._written[key] = self._loop.create_future()
        try:
            err: pb.Error | None = await self._first(f)
            return err
        finally:
            self._written.pop(key, None)

    async def _first(self, f: asyncio.Future[Any]) -> Any:
        """``f``'s result, or the connection's failure, whichever is first."""
        if not self._failure.done():
            await asyncio.wait({f, self._failure}, return_when=asyncio.FIRST_COMPLETED)
        if f.done():
            return f.result()
        raise self._failure.result()

    def on_set_cookie(self, jar: int, fn: Callable[[pb.SetCookie], None] | None) -> None:
        if fn is None:
            self._set_cookie.pop(jar, None)
        else:
            self._set_cookie[jar] = fn

    def on_verify(self, transport: int, fn: Callable[[pb.VerifyConnection], Awaitable[None]] | None) -> None:
        if fn is None:
            self._verify.pop(transport, None)
        else:
            self._verify[transport] = fn

    def free(self, oid: int) -> None:
        """Close an object without waiting (a finalizer's, on the loop)."""
        if not self.usable or self._loop.is_closed():
            return
        self._spawn_task(self._free(oid))

    async def _free(self, oid: int) -> None:
        try:
            await self.call(pb.Call(close=pb.Close(id=oid)))
        except MimicryError:
            pass

    def _spawn_task(self, coro: Any) -> None:
        t = self._loop.create_task(coro)
        self._tasks.add(t)
        t.add_done_callback(self._tasks.discard)

    # -- errors ---------------------------------------------------------

    async def _error(self, e: wire.WireError, sid: int) -> MimicryError:
        """The SDK's exception for a stream's wire error."""
        if isinstance(e, wire.ConnectionClosed):
            return await asyncio.shield(self._failure)
        if isinstance(e, wire.RemoteReset):
            pe = self._stream_errors.pop(sid, None)
            if pe is not None:
                return from_pb(pe)
            return CanceledError(f"mimicry-core reset the call ({wire.code_name(e.code)})")
        if isinstance(e, wire.StreamCanceled):
            return CanceledError("the call was canceled")
        return ProtocolError(str(e))

    async def _failed(self, err: wire.ConnectionClosed) -> None:
        """Decide what the connection's failure is, once."""
        cause = err.cause
        if self._closing:
            exc: MimicryError = CanceledError("the connection to mimicry-core was closed")
        elif isinstance(cause, (wire.ProtocolViolation, wire.RemoteReset)) or self._proc is None:
            if self._proc is not None and self._proc.poll() is None:
                self._proc.kill()
            exc = ProtocolError(f"lost the connection to mimicry-core: {err}")
        else:
            assert self._stderr is not None
            done = await asyncio.to_thread(self._stderr.done.wait, 2.0)
            if not done:
                self._proc.kill()
                await asyncio.to_thread(self._stderr.done.wait, 5.0)
                exc = ProtocolError(f"lost the connection to mimicry-core: {err}")
            else:
                exc = CoreExited(self._proc.returncode, self._stderr.text())
        if not self._failure.done():
            self._failure.set_result(exc)

    # -- the driver -----------------------------------------------------

    def _received(self, data: bytes | memoryview) -> None:
        self._events(self._wc.receive_data(data))
        self._flush()

    def _events(self, events: list[wire.Event]) -> None:
        waits = self._waits
        for e in events:
            if isinstance(e, wire.StreamChanged):
                ev = waits.get(e.stream.id)
                if ev is not None:
                    ev.set()
            elif isinstance(e, wire.Notice):
                self._notice(e.payload)
            elif isinstance(e, wire.StreamAccepted):
                while (s := self._wc.accept()) is not None:
                    self._spawn_task(self._answer(Call(self, s)))
            else:
                self._spawn_task(self._failed(e.error))
                for ev in waits.values():
                    ev.set()
                self._wake_openers()
        if self._openers and self._wc.can_open():
            self._wake_openers()

    def _wake_openers(self) -> None:
        while self._openers and self._wc.can_open():
            f = self._openers.popleft()
            if not f.done():
                f.set_result(None)
                if self._wc.error is None:
                    return

    def _flush(self) -> None:
        t = self._transport
        if t is None:
            return
        data = self._wc.data_to_send()
        if data:
            t.write(data)
        if self._wc.error is not None and not t.is_closing():
            t.close()
        if self._openers and self._wc.can_open():
            self._wake_openers()

    async def _drain(self) -> None:
        f = self._loop.create_future()
        self._drainers.append(f)
        await f

    def _resume(self) -> None:
        self._paused = False
        for f in self._drainers:
            if not f.done():
                f.set_result(None)
        self._drainers.clear()

    def _notice(self, payload: bytes) -> None:
        """A notice: recorded or handed off, never waited on."""
        try:
            ev = pb.Event.FromString(payload)
        except Exception:  # noqa: BLE001 - a notice this SDK cannot read is dropped
            return
        kind = ev.WhichOneof("event")
        if kind == "stream_error":
            self._stream_errors[ev.stream_error.stream] = ev.stream_error.error
        elif kind == "websocket_written":
            w = ev.websocket_written
            err = w.error if w.HasField("error") else None
            key = (w.websocket, w.seq)
            f = self._written.get(key)
            if f is not None and not f.done():
                f.set_result(err)
            else:
                self._written_early[key] = err
        elif kind == "set_cookie":
            fn = self._set_cookie.get(ev.set_cookie.jar)
            if fn is not None:
                self._loop.call_soon(fn, ev.set_cookie)

    async def _answer(self, c: Call) -> None:
        """Answer a reverse call (VerifyConnection)."""
        try:
            ev = pb.Event.FromString(c._s.payload)
            if ev.continued:
                ev = pb.Event.FromString(await c.read_exactly(ev.continued))
            reply = pb.EventReply()
            if ev.WhichOneof("event") == "verify_connection":
                fn = self._verify.get(ev.verify_connection.transport)
                if fn is not None:
                    try:
                        await fn(ev.verify_connection)
                    except Exception as e:  # noqa: BLE001 - the callback's rejection
                        reply.error.code = pb.CODE_VERIFY
                        reply.error.message = str(e) or type(e).__name__
            await c.reply_to(reply.SerializeToString())
        except MimicryError:
            pass
        finally:
            c.close()

    # -- fork -----------------------------------------------------------

    def _forked_in_child(self) -> None:
        """In a forked child: drop the parent's connection without a byte
        sent or a descriptor closed under anything else."""
        self._forked = True
        if not self._fds:
            return
        null = os.open(os.devnull, os.O_RDWR)
        try:
            for fd in self._fds:
                try:
                    os.dup2(null, fd, inheritable=False)
                except OSError:
                    pass
        finally:
            os.close(null)


_defaults: weakref.WeakKeyDictionary[asyncio.AbstractEventLoop, AsyncCore] = weakref.WeakKeyDictionary()
_default_locks: weakref.WeakKeyDictionary[asyncio.AbstractEventLoop, asyncio.Lock] = weakref.WeakKeyDictionary()
# What a forked child inherited: kept so that nothing of it is ever collected
# (a finalizer closing a descriptor or a loop's selector of the parent's).
_inherited: list[object] = []


async def default_core() -> AsyncCore:
    """This event loop's core, started on first use (and again after it
    exited or the process forked)."""
    loop = asyncio.get_running_loop()
    core = _defaults.get(loop)
    if core is not None and core.usable:
        return core
    lock = _default_locks.get(loop)
    if lock is None:
        lock = _default_locks[loop] = asyncio.Lock()
    async with lock:
        core = _defaults.get(loop)
        if core is None or not core.usable:
            core = await AsyncCore.spawn()
            _defaults[loop] = core
        return core


def _after_fork() -> None:
    _inherited.extend(_cores)
    _inherited.extend(_defaults.values())
    for core in list(_cores):
        core._forked_in_child()
    _defaults.clear()
    _default_locks.clear()


if hasattr(os, "register_at_fork"):
    os.register_at_fork(after_in_child=_after_fork)


def is_awaitable(x: object) -> bool:
    return inspect.isawaitable(x)
