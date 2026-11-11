"""mimicry's sync API: the async API's protocol, driven from one background
thread running an event loop (decision 0069). A sync call hands its
coroutine to that loop and waits for the result; callbacks the core asks
for (``verify_connection``, ``on_set_cookie``) run on worker threads, never
on the loop's, so they may block or call the sync API.
"""

from __future__ import annotations

import asyncio
import atexit
import concurrent.futures
import dataclasses
import os
import socket
import threading
from collections.abc import Callable, Coroutine, Iterator, Sequence
from typing import Any, TypeVar

from . import aio
from ._core import AsyncCore
from ._errors import MimicryError
from ._types import (
    CertPin,
    Config,
    Cookie,
    CookieRequest,
    Duration,
    Headers,
    MessageType,
    Plan,
    ProfileInfo,
    Request,
    ResponseHead,
    SetCookieEvent,
    WebSocketOptions,
)

_T = TypeVar("_T")


class _Runtime:
    """The background thread and its event loop, started on first use."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self.loop: asyncio.AbstractEventLoop | None = None
        self.thread: threading.Thread | None = None
        self._callbacks: concurrent.futures.ThreadPoolExecutor | None = None

    def _loop(self) -> asyncio.AbstractEventLoop:
        loop = self.loop
        if loop is not None:
            return loop
        with self._lock:
            if self.loop is None:
                loop = asyncio.new_event_loop()
                th = threading.Thread(target=self._serve, args=(loop,), name="mimicry", daemon=True)
                th.start()
                self.thread = th
                self.loop = loop
            return self.loop

    @staticmethod
    def _serve(loop: asyncio.AbstractEventLoop) -> None:
        asyncio.set_event_loop(loop)
        loop.run_forever()

    def run(self, coro: Coroutine[Any, Any, _T]) -> _T:
        loop = self._loop()
        if threading.current_thread() is self.thread:
            coro.close()
            raise RuntimeError("the sync mimicry API was called on mimicry's own thread; use mimicry.aio there")
        fut = asyncio.run_coroutine_threadsafe(coro, loop)
        try:
            return fut.result()
        except BaseException:
            # KeyboardInterrupt and the like: cancel the call (its stream is
            # reset), then let the exception go on.
            if not fut.done():
                fut.cancel()
            raise

    def callbacks(self) -> concurrent.futures.ThreadPoolExecutor:
        """The worker thread on_set_cookie callbacks run on, in order."""
        with self._lock:
            if self._callbacks is None:
                self._callbacks = concurrent.futures.ThreadPoolExecutor(1, thread_name_prefix="mimicry-callback")
            return self._callbacks

    def shutdown(self) -> None:
        """At exit: close every core of the loop, so each exits now."""
        loop = self.loop
        if loop is None or loop.is_closed() or self.thread is None or not self.thread.is_alive():
            return

        async def close_all() -> None:
            from ._core import _cores

            for core in list(_cores):
                if core._loop is loop:
                    await core.close()

        try:
            asyncio.run_coroutine_threadsafe(close_all(), loop).result(2)
        except Exception:  # noqa: BLE001,S110 - best effort at exit
            pass


_rt = _Runtime()
_inherited: list[_Runtime] = []


def _run(coro: Coroutine[Any, Any, _T]) -> _T:
    return _rt.run(coro)


def _after_fork() -> None:
    """In a forked child: the parent's thread is gone; start afresh."""
    global _rt
    _inherited.append(_rt)
    _rt = _Runtime()


if hasattr(os, "register_at_fork"):
    os.register_at_fork(after_in_child=_after_fork)
atexit.register(lambda: _rt.shutdown())


def _sync_config(config: Config) -> Config:
    """``config`` with its callback run on a worker thread."""
    fn = config.verify_connection
    if fn is None:
        return config

    def verify(state: Any) -> Any:
        return asyncio.to_thread(fn, state)

    return dataclasses.replace(config, verify_connection=verify)


class Core:
    """A connection to a mimicry-core (the sync API's :class:`AsyncCore`).
    Without one, objects use the process's own, started on first use."""

    def __init__(self, a: AsyncCore) -> None:
        self._async = a

    @classmethod
    def spawn(cls) -> Core:
        """Start a core as a child of this process."""
        return cls(_run(AsyncCore.spawn()))

    @classmethod
    def connect(cls, sock: socket.socket) -> Core:
        """Speak to a core over a connected stream socket."""
        return cls(_run(AsyncCore.connect(sock)))

    @property
    def core_version(self) -> str:
        return self._async.core_version

    @property
    def pid(self) -> int | None:
        return self._async.pid

    def close(self) -> None:
        _run(self._async.close())

    def kill(self) -> None:
        self._async.kill()

    def __enter__(self) -> Core:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()


def _core(core: Core | None) -> AsyncCore | None:
    return None if core is None else core._async


class _Object:
    _async: Any

    def close(self) -> None:
        """Free the object in the core."""
        _run(self._async.close())

    def __enter__(self: _T) -> _T:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()


class _Sender(_Object):
    _async: aio._Sender

    def plan(self, req: Request) -> Plan:
        """What ``req`` would send, sending nothing (Go's ``Transport.Plan``)."""
        return _run(self._async.plan(req))

    def dial_websocket(
        self, url: str, options: WebSocketOptions | None = None, *, timeout: Duration | None = None
    ) -> WebSocket:
        """Open a WebSocket (Go's ``Transport.DialWebSocket``)."""
        return WebSocket(_run(self._async.dial_websocket(url, options, timeout=timeout)))

    def close_idle_connections(self) -> None:
        _run(self._async.close_idle_connections())


class Transport(_Sender):
    """Go's ``mimicry.Transport``: one hop per request, redirects returned."""

    _async: aio.AsyncTransport

    def __init__(self, a: aio.AsyncTransport) -> None:
        self._async = a

    def round_trip(self, req: Request) -> Response:
        """Send one request (Go's ``Transport.RoundTrip``)."""
        return Response(_run(self._async.round_trip(req)))


class Client(_Sender):
    """Go's ``mimicry.NewClient``'s client: redirects followed, with a cookie
    jar (``cookies``)."""

    _async: aio.AsyncClient

    def __init__(self, a: aio.AsyncClient, cookies: Jar) -> None:
        self._async = a
        self.cookies = cookies

    def do(self, req: Request) -> Response:
        """Send ``req``, following redirects (Go's ``Client.Do``)."""
        return Response(_run(self._async.do(req)))


class Response:
    """A response: its head (``status_code``, ``headers``, ``url`` ...), then
    its body (:meth:`read`, :meth:`iter_bytes`). Close it, or read it to its
    end, to free the call."""

    def __init__(self, a: aio.AsyncResponse) -> None:
        self._async = a
        self.head: ResponseHead = a.head

    status_code = property(lambda self: self.head.status_code)
    status = property(lambda self: self.head.status)
    proto = property(lambda self: self.head.proto)
    headers: Headers = property(lambda self: self.head.headers)  # type: ignore[assignment]
    content_length = property(lambda self: self.head.content_length)
    uncompressed = property(lambda self: self.head.uncompressed)
    url = property(lambda self: self.head.url)
    tls = property(lambda self: self.head.tls)

    def __repr__(self) -> str:
        return f"<Response [{self.status_code}] {self.url}>"

    def read(self) -> bytes:
        """The whole body."""
        return _run(self._async.read())

    def iter_bytes(self) -> Iterator[bytes]:
        """The body's chunks as they arrive."""
        a = self._async
        if a._done:
            if a._content is None:
                raise MimicryError("the response's body was already read or closed")
            if a._content:
                yield a._content
            return
        try:
            while chunk := _run(a._next_chunk()):
                yield chunk
        finally:
            if not a._done:
                _run(a.close())

    def close(self) -> None:
        """Stop reading: a body not read to its end is canceled."""
        _run(self._async.close())

    def __enter__(self) -> Response:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()


class WebSocket:
    """An open WebSocket (Go's ``mimicry.WebSocket``)."""

    def __init__(self, a: aio.AsyncWebSocket) -> None:
        self._async = a
        self.protocol = a.protocol
        self.extensions = a.extensions
        self.response = a.response

    def read_message(self) -> tuple[MessageType, bytes]:
        """The next message; once the socket closed, raises why."""
        return _run(self._async.read_message())

    def write_message(self, typ: MessageType, data: bytes | str) -> None:
        """Send a message, returning once mimicry-core has written it."""
        _run(self._async.write_message(typ, data))

    def close(self, code: int = 1000, reason: str = "") -> None:
        """Close with ``code`` and ``reason``, once the handshake is done."""
        _run(self._async.close(code, reason))

    def __enter__(self) -> WebSocket:
        return self

    def __exit__(self, *exc: object) -> None:
        _run(self._async.__aexit__())


class _Saved(_Object):
    _async: aio._Saved

    def write_json(self, file: aio.FileLike) -> None:
        """Save as JSON: to a path (created 0600) or a binary file object."""
        _run(self._async.write_json(file))

    def read_json(self, file: aio.FileLike) -> None:
        """Load what :meth:`write_json` saved."""
        _run(self._async.read_json(file))


class Jar(_Saved):
    """A cookie jar with a profile's rules (Go's ``cookies.Jar``)."""

    _async: aio.AsyncJar

    def __init__(self, a: aio.AsyncJar) -> None:
        self._async = a

    def all(self) -> list[Cookie]:
        return _run(self._async.all())

    def cookies(self, request: CookieRequest) -> list[Cookie]:
        return _run(self._async.cookies(request))

    def peek(self, request: CookieRequest) -> list[Cookie]:
        return _run(self._async.peek(request))

    def set(self, cookie: Cookie) -> None:
        _run(self._async.set(cookie))

    def set_string(self, domain: str, cookies: str | bytes) -> None:
        _run(self._async.set_string(domain, cookies))

    def set_host_string(self, host: str, cookies: str | bytes) -> None:
        _run(self._async.set_host_string(host, cookies))

    def set_cookies(self, request: CookieRequest, lines: Sequence[str | bytes]) -> None:
        _run(self._async.set_cookies(request, lines))

    def clear(self) -> None:
        _run(self._async.clear())

    def clear_expired(self) -> int:
        return _run(self._async.clear_expired())

    def clear_session(self) -> int:
        return _run(self._async.clear_session())

    def string(self, name: str) -> str:
        return _run(self._async.string(name))

    def clone(self) -> Jar:
        return Jar(_run(self._async.clone()))

    def write_netscape(self, file: aio.FileLike) -> None:
        _run(self._async.write_netscape(file))

    def read_netscape(self, file: aio.FileLike) -> None:
        _run(self._async.read_netscape(file))


class TicketCache(_Saved):
    """TLS session tickets (Go's ``mimicry.TicketCache``): secrets."""

    _async: aio.AsyncTicketCache

    def __init__(self, a: aio.AsyncTicketCache) -> None:
        self._async = a

    def clear(self) -> None:
        _run(self._async.clear())


class AltSvcCache(_Saved):
    """Alt-Svc records (Go's ``mimicry.AltSvcCache``)."""

    _async: aio.AsyncAltSvcCache

    def __init__(self, a: aio.AsyncAltSvcCache) -> None:
        self._async = a

    def clear(self) -> None:
        _run(self._async.clear())


class ClientHintStore(_Saved):
    """Each site's client hint opt-in (Go's ``mimicry.ClientHintStore``)."""

    _async: aio.AsyncClientHintStore

    def __init__(self, a: aio.AsyncClientHintStore) -> None:
        self._async = a

    def client_hints(self, origin: str) -> list[str]:
        return _run(self._async.client_hints(origin))

    def set_client_hints(self, origin: str, hints: Sequence[str]) -> None:
        _run(self._async.set_client_hints(origin, hints))


class Profile(_Object):
    """A profile parsed from JSON (:func:`parse_profile`)."""

    _async: aio.AsyncProfile

    def __init__(self, a: aio.AsyncProfile) -> None:
        self._async = a
        self.name = a.name
        self.user_agent = a.user_agent

    def spec(self) -> bytes:
        return _run(self._async.spec())


def new_client(
    config: Config, *, follow_redirects: bool = True, max_redirects: int = 0, core: Core | None = None
) -> Client:
    """A client (Go's ``mimicry.NewClient``): redirects followed, up to
    ``max_redirects`` (0: the browser's limit); with ``config.cookies`` or a
    jar of its own (``client.cookies``)."""
    a = _run(
        aio.new_client(
            _sync_config(config), follow_redirects=follow_redirects, max_redirects=max_redirects, core=_core(core)
        )
    )
    jar = config.cookies if isinstance(config.cookies, Jar) else Jar(a.cookies)
    return Client(a, jar)


def new_transport(config: Config, *, core: Core | None = None) -> Transport:
    """A transport (Go's ``mimicry.NewTransport``): one hop per request."""
    return Transport(_run(aio.new_transport(_sync_config(config), core=_core(core))))


def new_jar(
    *, rules: Any = None, on_set_cookie: Callable[[SetCookieEvent], Any] | None = None, core: Core | None = None
) -> Jar:
    """A cookie jar with ``rules``' (a profile's) cookie rules, RFC 6265bis
    when None. ``on_set_cookie`` is called, on a worker thread and in order,
    with each Set-Cookie line the jar receives."""
    hook = None
    if on_set_cookie is not None:
        cb = on_set_cookie

        def hook(e: SetCookieEvent) -> None:
            _rt.callbacks().submit(cb, e)

    return Jar(_run(aio.new_jar(rules=rules, on_set_cookie=hook, core=_core(core))))


def new_ticket_cache(*, core: Core | None = None) -> TicketCache:
    return TicketCache(_run(aio.new_ticket_cache(core=_core(core))))


def new_alt_svc_cache(*, core: Core | None = None) -> AltSvcCache:
    return AltSvcCache(_run(aio.new_alt_svc_cache(core=_core(core))))


def new_client_hint_store(*, core: Core | None = None) -> ClientHintStore:
    return ClientHintStore(_run(aio.new_client_hint_store(core=_core(core))))


def list_profiles(*, core: Core | None = None) -> aio.ProfileList:
    """The built-in profiles (Go's ``profiles.Names`` and ``Supported``)."""
    return _run(aio.list_profiles(core=_core(core)))


def profile_spec(profile: Any, *, core: Core | None = None) -> tuple[ProfileInfo, bytes]:
    """A profile's name, user agent and data as JSON."""
    return _run(aio.profile_spec(profile, core=_core(core)))


def parse_profile(data: bytes | str, *, core: Core | None = None) -> Profile:
    """Read a profile's JSON (Go's ``profile.Parse``)."""
    return Profile(_run(aio.parse_profile(data, core=_core(core))))


def machines(platform: str, *, core: Core | None = None) -> list[bytes]:
    """The captured machines of ``platform``, each as JSON."""
    return _run(aio.machines(platform, core=_core(core)))


def fetch_pins(config: Config, url: str, *, timeout: Duration | None = None, core: Core | None = None) -> list[CertPin]:
    """The certificate chain ``url``'s server presents (Go's ``mimicry.FetchPins``);
    ``config.pins`` and ``config.verify_connection`` are ignored, as in Go."""
    return _run(aio.fetch_pins(_sync_config(config), url, timeout=timeout, core=_core(core)))
