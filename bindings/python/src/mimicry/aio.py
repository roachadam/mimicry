"""mimicry's async API: Go's names in Python casing, every call awaited.

    import mimicry
    from mimicry import aio

    client = await aio.new_client(mimicry.Config(profile=mimicry.profiles.chrome_macos()))
    resp = await client.do(mimicry.Request("GET", "https://www.shop.example/p/42"))
    page = await resp.read()

Objects belong to a core (:class:`AsyncCore`), by default the event loop's
own, started on first use. Close them (``await obj.close()`` or ``async
with``); one collected unclosed is freed in the background.
"""

from __future__ import annotations

import asyncio
import os
import struct
import weakref
from collections.abc import AsyncIterable, AsyncIterator, Callable, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import IO, Any, TypeVar, Union

from . import _types as t
from ._core import AsyncCore, Call, abandon_on_collect, default_core, is_awaitable
from ._errors import InvalidError, MimicryError, from_pb
from ._pb.mimicry.v1 import mimicry_pb2 as pb
from ._types import (
    CertPin,
    Config,
    ConnectionState,
    Cookie,
    CookieRequest,
    Headers,
    MessageType,
    Newest,
    Plan,
    ProfileInfo,
    Request,
    ResponseHead,
    SetCookieEvent,
    WebSocketOptions,
)

__all__ = [
    "AsyncAltSvcCache",
    "AsyncClient",
    "AsyncClientHintStore",
    "AsyncCore",
    "AsyncJar",
    "AsyncProfile",
    "AsyncResponse",
    "AsyncTicketCache",
    "AsyncTransport",
    "AsyncWebSocket",
    "ProfileList",
    "fetch_pins",
    "list_profiles",
    "machines",
    "new_alt_svc_cache",
    "new_client",
    "new_client_hint_store",
    "new_jar",
    "new_ticket_cache",
    "new_transport",
    "parse_profile",
    "profile_spec",
]

FileLike = Union[str, "os.PathLike[str]", IO[bytes]]
"""A path (written 0600, replaced atomically) or a binary file object."""

_MAX_MESSAGE = 64 << 20
_record = struct.Struct(">BI")
_T = TypeVar("_T", bound="_Object")


def _free(ref: weakref.ref[AsyncCore], oid: int) -> None:
    """A finalizer's: free an object on its core's loop."""
    core = ref()
    if core is None or core._forked or core._loop.is_closed():
        return
    try:
        core._loop.call_soon_threadsafe(core.free, oid)
    except RuntimeError:
        pass


class _Object:
    """An object of the core: its id, freed by :meth:`close` or, as a
    backstop, when collected."""

    def __init__(self, core: AsyncCore, oid: int) -> None:
        self._core = core
        self._id = oid
        self._closed = False
        self._finalizer = weakref.finalize(self, _free, weakref.ref(core), oid)

    @property
    def core(self) -> AsyncCore:
        return self._core

    def _oid(self) -> int:
        if self._closed:
            raise MimicryError(f"{type(self).__name__} is closed")
        self._core.check()
        return self._id

    async def close(self) -> None:
        """Free the object in the core."""
        if self._closed:
            return
        self._closed = True
        self._finalizer.detach()
        self._closed_hook()
        if self._core.usable:
            try:
                await self._core.call(pb.Call(close=pb.Close(id=self._id)))
            except MimicryError:
                pass

    def _closed_hook(self) -> None:
        pass

    async def __aenter__(self: _T) -> _T:
        return self

    async def __aexit__(self, *exc: object) -> None:
        await self.close()


async def _core_for(core: AsyncCore | None, *objects: Any) -> AsyncCore:
    """The core a call goes to: ``core``, else that of the objects it names,
    else the loop's default. Every object must be of that core."""
    owners = {o._core for o in objects if isinstance(o, _Object)}
    if core is None and owners:
        core = next(iter(owners))
    if core is None:
        core = await default_core()
    if owners - {core}:
        raise InvalidError("objects of different mimicry-core connections cannot be used together")
    core.check()
    return core


def _async_of(x: Any) -> Any:
    """The async object behind a sync API object (or ``x`` itself)."""
    return getattr(x, "_async", x)


async def _profile_ref(core: AsyncCore, profile: Any) -> pb.ProfileRef:
    profile = _async_of(profile)
    if isinstance(profile, AsyncProfile):
        return pb.ProfileRef(id=profile._oid())
    if isinstance(profile, Newest):
        names = getattr(core, "_profile_names", None)
        if names is None:
            names = (await list_profiles(core=core)).supported
            core._profile_names = names  # type: ignore[attr-defined]
        return pb.ProfileRef(name=profile.resolve(names))
    if isinstance(profile, str):
        return pb.ProfileRef(name=profile)
    raise TypeError(
        f"Config.profile must be a profile's name, a mimicry.profiles accessor's or a parsed profile, not {profile!r}"
    )


_CONFIG_OBJECTS = ("cookies", "client_hint_store", "alt_svc", "tickets")


async def _config(core: AsyncCore | None, config: Config) -> tuple[AsyncCore, pb.Config]:
    objs = {k: _async_of(getattr(config, k)) for k in _CONFIG_OBJECTS if getattr(config, k) is not None}
    want = {
        "cookies": AsyncJar,
        "client_hint_store": AsyncClientHintStore,
        "alt_svc": AsyncAltSvcCache,
        "tickets": AsyncTicketCache,
    }
    for k, v in objs.items():
        if not isinstance(v, want[k]):
            raise TypeError(f"Config.{k} must be a {want[k].__name__[5:]}, not {v!r}")
    prof = _async_of(config.profile)
    core = await _core_for(core, *objs.values(), prof)
    ids = {k: v._oid() for k, v in objs.items()}
    return core, config._to_pb(await _profile_ref(core, prof), ids)


def _verifier(fn: t.VerifyCallback) -> Callable[[pb.VerifyConnection], Any]:
    async def verify(v: pb.VerifyConnection) -> None:
        r = fn(ConnectionState._from_pb(v.state))
        if is_awaitable(r):
            await r  # type: ignore[misc]

    return verify


async def _body_chunks(body: Any) -> AsyncIterator[bytes]:
    """A streamed body's chunks: an async iterable as it is, a sync one
    pulled on a worker thread (its next may block)."""
    if isinstance(body, AsyncIterable):
        async for chunk in body:
            yield chunk
        return
    it = iter(body)
    done = object()
    while True:
        chunk = await asyncio.to_thread(next, it, done)
        if chunk is done:
            return
        yield chunk


class _Sender(_Object):
    """What clients and transports share: one browsing entity (a tab)."""

    def __init__(self, core: AsyncCore, oid: int, verify: t.VerifyCallback | None) -> None:
        super().__init__(core, oid)
        if verify is not None:
            core.on_verify(oid, _verifier(verify))

    def _closed_hook(self) -> None:
        self._core.on_verify(self._id, None)

    async def plan(self, req: Request) -> Plan:
        """What ``req`` would send, sending nothing (Go's ``Transport.Plan``)."""
        if req._streamed():
            raise TypeError("plan takes no streamed body")
        r = await self._core.call(pb.Call(plan=pb.Plan(transport=self._oid(), request=req._to_pb())))
        return Plan._from_pb(r.plan)

    async def dial_websocket(
        self,
        url: str,
        options: WebSocketOptions | None = None,
        *,
        timeout: t.Duration | None = None,
    ) -> AsyncWebSocket:
        """Open a WebSocket (Go's ``Transport.DialWebSocket``). A failed
        handshake raises its error, with ``response`` and ``body`` (the
        start of the response's body) set on it when the server answered."""
        d = pb.DialWebSocket(transport=self._oid(), url=url)
        if options is not None:
            d.options.CopyFrom(options._to_pb())
        t._duration(timeout, d.timeout)
        core = self._core
        c = await core.open(pb.Call(dial_websocket=d), body=True)
        try:
            r = await c.reply()
            if r.HasField("error"):
                err = from_pb(r.error)
                if r.HasField("response"):
                    err.response = ResponseHead._from_pb(r.response)  # type: ignore[attr-defined]
                    err.body = await c.read_all()  # type: ignore[attr-defined]
                c.close()
                raise err
        except BaseException:
            c.close()
            raise
        return AsyncWebSocket(c, r.websocket)

    async def close_idle_connections(self) -> None:
        await self._core.call(pb.Call(close_idle_connections=pb.CloseIdleConnections(transport=self._oid())))


class AsyncTransport(_Sender):
    """Go's ``mimicry.Transport``: one hop per request, redirects returned."""

    async def round_trip(self, req: Request) -> AsyncResponse:
        """Send one request (Go's ``Transport.RoundTrip``)."""
        return await _send(self, req)


class AsyncClient(_Sender):
    """Go's ``mimicry.NewClient``'s client: redirects followed, with a
    cookie jar (``cookies``)."""

    def __init__(self, core: AsyncCore, oid: int, verify: t.VerifyCallback | None, cookies: AsyncJar) -> None:
        super().__init__(core, oid, verify)
        self.cookies = cookies

    async def do(self, req: Request) -> AsyncResponse:
        """Send ``req``, following redirects (Go's ``Client.Do``)."""
        return await _send(self, req)


async def _send(tr: _Sender, req: Request) -> AsyncResponse:
    core = tr._core
    streamed = req._streamed()
    c = await core.open(pb.Call(do=pb.Do(client=tr._oid(), request=req._to_pb())), body=streamed)
    upload: asyncio.Task[None] | None = None
    try:
        if streamed:
            upload = asyncio.ensure_future(_upload(c, req.body))
        r = await c.reply()
        if r.HasField("error"):
            raise from_pb(r.error)
        return AsyncResponse(c, ResponseHead._from_pb(r.response), upload)
    except BaseException as e:
        c.close()
        if upload is not None:
            if upload.done() and not upload.cancelled() and upload.exception() is not None:
                raise upload.exception() from e  # type: ignore[misc]
            upload.cancel()
        raise


async def _upload(c: Call, body: Any) -> None:
    try:
        async for chunk in _body_chunks(body):
            if chunk:
                await c.write(chunk)
        await c.end()
    except MimicryError:
        pass  # the call failed: its reply or body says why
    except BaseException:
        c.close()
        raise


class AsyncResponse:
    """A response: its head (``status_code``, ``headers``, ``url`` ...), then
    its body, read with :meth:`read` or :meth:`aiter_bytes`. Close it (or
    read it to its end) to free the call."""

    def __init__(self, call: Call, head: ResponseHead, upload: asyncio.Task[None] | None) -> None:
        self.head = head
        self._call = call
        self._upload = upload
        self._done = False
        self._content: bytes | None = None
        self._finalizer = abandon_on_collect(self, call)

    status_code = property(lambda self: self.head.status_code)
    status = property(lambda self: self.head.status)
    proto = property(lambda self: self.head.proto)
    headers: Headers = property(lambda self: self.head.headers)  # type: ignore[assignment]
    content_length = property(lambda self: self.head.content_length)
    uncompressed = property(lambda self: self.head.uncompressed)
    url = property(lambda self: self.head.url)
    tls = property(lambda self: self.head.tls)

    def __repr__(self) -> str:
        return f"<AsyncResponse [{self.status_code}] {self.url}>"

    def _finish(self) -> None:
        self._done = True
        self._finalizer.detach()
        self._call.close()

    async def read(self) -> bytes:
        """The whole body (kept, so a second read returns it again)."""
        if self._content is None:
            parts = [chunk async for chunk in self.aiter_bytes()]
            self._content = b"".join(parts)
        return self._content

    async def aiter_bytes(self) -> AsyncIterator[bytes]:
        """The body's chunks as they arrive."""
        if self._done:
            if self._content is None:
                raise MimicryError("the response's body was already read or closed")
            if self._content:
                yield self._content
            return
        while chunk := await self._next_chunk():
            yield chunk

    async def _next_chunk(self) -> bytes:
        """The body's next chunk; ``b""`` at its end."""
        if self._done:
            return b""
        try:
            chunk = await self._call.read()
        except BaseException:
            await self.close()
            raise
        if not chunk:
            self._finish()
        return chunk

    async def close(self) -> None:
        """Stop reading: a body not read to its end is canceled (RESET)."""
        if not self._done:
            self._finish()
        if self._upload is not None and not self._upload.done():
            self._upload.cancel()

    async def __aenter__(self) -> AsyncResponse:
        return self

    async def __aexit__(self, *exc: object) -> None:
        await self.close()


class AsyncWebSocket:
    """An open WebSocket (Go's ``mimicry.WebSocket``)."""

    def __init__(self, call: Call, ws: pb.WebSocket) -> None:
        self._call = call
        self._core = call._core
        self._id = ws.id
        self.protocol = ws.protocol
        self.extensions = ws.extensions
        self.response = ResponseHead._from_pb(ws.response)
        self._seq = 0
        self._wlock = asyncio.Lock()
        self._rlock = asyncio.Lock()
        self._end: MimicryError | None = None
        self._finalizer = abandon_on_collect(self, call)

    async def read_message(self) -> tuple[MessageType, bytes]:
        """The next message; once the socket closed, raises why (a
        :class:`CloseError` after a close handshake)."""
        async with self._rlock:
            if self._end is not None:
                raise self._end
            typ, n = _record.unpack(await self._call.read_exactly(_record.size))
            payload = await self._call.read_exactly(n) if n else b""
            if typ == 0:
                self._end = from_pb(pb.Error.FromString(payload))
                raise self._end
            return MessageType(typ), payload

    async def write_message(self, typ: MessageType, data: bytes | str) -> None:
        """Send a message, returning once mimicry-core has written it."""
        b = data.encode() if isinstance(data, str) else bytes(data)
        if len(b) > _MAX_MESSAGE:
            raise ValueError(f"a {len(b)}-byte message: at most {_MAX_MESSAGE}")
        async with self._wlock:
            await self._call.write(_record.pack(int(typ), len(b)))
            if b:
                await self._call.write(b)
            self._seq += 1
            err = await self._core.written(self._id, self._seq)
        if err is not None:
            raise from_pb(err)

    async def close(self, code: int = 1000, reason: str = "") -> None:
        """Close the socket with ``code`` and ``reason``, returning once the
        close handshake is done (Go's ``WebSocket.Close``)."""
        await self._core.call(pb.Call(close_websocket=pb.CloseWebSocket(websocket=self._id, code=code, reason=reason)))
        async with self._wlock:
            if not self._call.ended:
                try:
                    await self._call.end()
                except MimicryError:
                    pass

    async def __aenter__(self) -> AsyncWebSocket:
        return self

    async def __aexit__(self, *exc: object) -> None:
        if not self._call.ended and self._end is None:
            try:
                await self.close()
            except MimicryError:
                pass
        self._finalizer.detach()
        self._call.close()


def _atomic_write(path: str | os.PathLike[str], data: bytes) -> None:
    """Write ``data`` to ``path`` 0600, replacing it atomically: saved state
    holds credentials (cookies, tickets)."""
    path = os.fspath(path)
    d = os.path.dirname(os.path.abspath(path))
    tmp = os.path.join(d, f".{os.path.basename(path)}.{os.getpid()}.{id(data):x}.tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_BINARY", 0), 0o600)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


async def _put(file: FileLike, data: bytes) -> None:
    if isinstance(file, (str, os.PathLike)):
        await asyncio.to_thread(_atomic_write, file, data)
    else:
        file.write(data)


async def _get(file: FileLike) -> bytes:
    if isinstance(file, (str, os.PathLike)):
        return await asyncio.to_thread(Path(file).read_bytes)
    data = file.read()
    return data.encode() if isinstance(data, str) else bytes(data)


class _Saved(_Object):
    """An object saved and loaded as JSON (``write_json``/``read_json``)."""

    async def write_json(self, file: FileLike) -> None:
        """Save as JSON (Go's ``WriteJSON``): to a path, created 0600, or a
        binary file object."""
        _, data = await self._core.call_for_body(pb.Call(write_json=pb.WriteJSON(id=self._oid())))
        await _put(file, data)

    async def read_json(self, file: FileLike) -> None:
        """Load what :meth:`write_json` saved (Go's ``ReadJSON``)."""
        data = await _get(file)
        await self._core.call_with_body(pb.Call(read_json=pb.ReadJSON(id=self._oid())), data)


class AsyncJar(_Saved):
    """A cookie jar with a profile's rules (Go's ``cookies.Jar``)."""

    def __init__(self, core: AsyncCore, oid: int, on_set_cookie: Callable[[SetCookieEvent], Any] | None = None) -> None:
        super().__init__(core, oid)
        if on_set_cookie is not None:
            core.on_set_cookie(oid, lambda e: on_set_cookie(SetCookieEvent._from_pb(e)))

    def _closed_hook(self) -> None:
        self._core.on_set_cookie(self._id, None)

    async def _cookies(self, call: pb.Call) -> list[Cookie]:
        r = await self._core.call(call)
        return [Cookie._from_pb(c) for c in r.cookies.cookies]

    async def all(self) -> list[Cookie]:
        return await self._cookies(pb.Call(jar_all=pb.JarAll(jar=self._oid())))

    async def cookies(self, request: CookieRequest) -> list[Cookie]:
        """The cookies ``request`` sends, as it would (Go's ``Jar.Cookies``)."""
        return await self._cookies(pb.Call(jar_cookies=pb.JarCookies(jar=self._oid(), request=request._to_pb())))

    async def peek(self, request: CookieRequest) -> list[Cookie]:
        """As :meth:`cookies`, touching nothing (Go's ``Jar.Peek``)."""
        return await self._cookies(pb.Call(jar_peek=pb.JarPeek(jar=self._oid(), request=request._to_pb())))

    async def set(self, cookie: Cookie) -> None:
        await self._core.call(pb.Call(jar_set=pb.JarSet(jar=self._oid(), cookie=cookie._to_pb())))

    async def set_string(self, domain: str, cookies: str | bytes) -> None:
        await self._core.call(
            pb.Call(jar_set_string=pb.JarSetString(jar=self._oid(), domain=domain, cookies=t._bytes(cookies)))
        )

    async def set_host_string(self, host: str, cookies: str | bytes) -> None:
        await self._core.call(
            pb.Call(jar_set_host_string=pb.JarSetHostString(jar=self._oid(), host=host, cookies=t._bytes(cookies)))
        )

    async def set_cookies(self, request: CookieRequest, lines: Sequence[str | bytes]) -> None:
        """Store a response's raw Set-Cookie lines (Go's ``Jar.SetCookies``)."""
        await self._core.call(
            pb.Call(
                jar_set_cookies=pb.JarSetCookies(
                    jar=self._oid(), request=request._to_pb(), lines=[t._bytes(x) for x in lines]
                )
            )
        )

    async def clear(self) -> None:
        await self._core.call(pb.Call(clear=pb.Clear(id=self._oid())))

    async def clear_expired(self) -> int:
        return (await self._core.call(pb.Call(jar_clear_expired=pb.JarClearExpired(jar=self._oid())))).count

    async def clear_session(self) -> int:
        return (await self._core.call(pb.Call(jar_clear_session=pb.JarClearSession(jar=self._oid())))).count

    async def string(self, name: str) -> str:
        """A Cookie header string of the cookies of domain ``name`` (Go's
        ``Jar.String``)."""
        r = await self._core.call(pb.Call(jar_string=pb.JarString(jar=self._oid(), name=name)))
        return t._text(r.value)

    async def clone(self) -> AsyncJar:
        r = await self._core.call(pb.Call(jar_clone=pb.JarClone(jar=self._oid())))
        return AsyncJar(self._core, r.object.id)

    async def write_netscape(self, file: FileLike) -> None:
        """Save as a Netscape cookies.txt (a path is created 0600)."""
        _, data = await self._core.call_for_body(pb.Call(write_netscape=pb.WriteNetscape(jar=self._oid())))
        await _put(file, data)

    async def read_netscape(self, file: FileLike) -> None:
        data = await _get(file)
        await self._core.call_with_body(pb.Call(read_netscape=pb.ReadNetscape(jar=self._oid())), data)


class AsyncTicketCache(_Saved):
    """TLS session tickets (Go's ``mimicry.TicketCache``): secrets; saved 0600."""

    async def clear(self) -> None:
        await self._core.call(pb.Call(clear=pb.Clear(id=self._oid())))


class AsyncAltSvcCache(_Saved):
    """Alt-Svc records (Go's ``mimicry.AltSvcCache``)."""

    async def clear(self) -> None:
        await self._core.call(pb.Call(clear=pb.Clear(id=self._oid())))


class AsyncClientHintStore(_Saved):
    """Each site's client hint opt-in (Go's ``mimicry.ClientHintStore``)."""

    async def client_hints(self, origin: str) -> list[str]:
        r = await self._core.call(pb.Call(client_hints=pb.ClientHints(store=self._oid(), origin=origin)))
        return list(r.strings.values)

    async def set_client_hints(self, origin: str, hints: Sequence[str]) -> None:
        await self._core.call(
            pb.Call(set_client_hints=pb.SetClientHints(store=self._oid(), origin=origin, hints=list(hints)))
        )


class AsyncProfile(_Object):
    """A profile parsed from JSON (:func:`parse_profile`), for ``Config.profile``."""

    def __init__(self, core: AsyncCore, info: pb.ProfileInfo) -> None:
        super().__init__(core, info.id)
        self.name = info.name
        self.user_agent = info.user_agent

    async def spec(self) -> bytes:
        """The profile as JSON (Go's ``profile.Encode``)."""
        _, data = await self._core.call_for_body(
            pb.Call(profile_spec=pb.ProfileSpec(profile=pb.ProfileRef(id=self._oid())))
        )
        return data


@dataclass(frozen=True)
class ProfileList:
    names: list[str]
    """Every built-in profile, deprecated ones included."""
    supported: list[str]
    """The built-in profiles not deprecated."""


async def new_client(
    config: Config,
    *,
    follow_redirects: bool = True,
    max_redirects: int = 0,
    core: AsyncCore | None = None,
) -> AsyncClient:
    """A client (Go's ``mimicry.NewClient``): redirects followed, up to
    ``max_redirects`` (0: the browser's limit); with ``config.cookies`` or a
    jar of its own (``client.cookies``)."""
    core, cfg = await _config(core, config)
    r = await core.call(
        pb.Call(
            new_client=pb.NewClient(
                config=cfg,
                redirects=pb.Redirects(follow=follow_redirects, max=max_redirects),
            )
        )
    )
    jar = _async_of(config.cookies)
    if jar is None:
        jar = AsyncJar(core, r.new_client.cookies)
    return AsyncClient(core, r.new_client.client, config.verify_connection, jar)


async def new_transport(config: Config, *, core: AsyncCore | None = None) -> AsyncTransport:
    """A transport (Go's ``mimicry.NewTransport``): one hop per request."""
    core, cfg = await _config(core, config)
    r = await core.call(pb.Call(new_transport=pb.NewTransport(config=cfg)))
    return AsyncTransport(core, r.object.id, config.verify_connection)


async def new_jar(
    *,
    rules: Any = None,
    on_set_cookie: Callable[[SetCookieEvent], Any] | None = None,
    core: AsyncCore | None = None,
) -> AsyncJar:
    """A cookie jar with ``rules``' (a profile's) cookie rules, RFC 6265bis
    when None. ``on_set_cookie`` is called (on the event loop) with each
    Set-Cookie line the jar receives."""
    rules = _async_of(rules)
    core = await _core_for(core, rules)
    j = pb.NewJar(on_set_cookie=on_set_cookie is not None)
    if rules is not None:
        j.rules.CopyFrom(await _profile_ref(core, rules))
    r = await core.call(pb.Call(new_jar=j))
    return AsyncJar(core, r.object.id, on_set_cookie)


async def new_ticket_cache(*, core: AsyncCore | None = None) -> AsyncTicketCache:
    core = await _core_for(core)
    r = await core.call(pb.Call(new_ticket_cache=pb.NewTicketCache()))
    return AsyncTicketCache(core, r.object.id)


async def new_alt_svc_cache(*, core: AsyncCore | None = None) -> AsyncAltSvcCache:
    core = await _core_for(core)
    r = await core.call(pb.Call(new_alt_svc_cache=pb.NewAltSvcCache()))
    return AsyncAltSvcCache(core, r.object.id)


async def new_client_hint_store(*, core: AsyncCore | None = None) -> AsyncClientHintStore:
    core = await _core_for(core)
    r = await core.call(pb.Call(new_client_hint_store=pb.NewClientHintStore()))
    return AsyncClientHintStore(core, r.object.id)


async def list_profiles(*, core: AsyncCore | None = None) -> ProfileList:
    """The built-in profiles (Go's ``profiles.Names`` and ``Supported``)."""
    core = await _core_for(core)
    r = await core.call(pb.Call(list_profiles=pb.ListProfiles()))
    return ProfileList(list(r.profiles.names), list(r.profiles.supported))


async def profile_spec(profile: Any, *, core: AsyncCore | None = None) -> tuple[ProfileInfo, bytes]:
    """A profile's name, user agent and data as JSON, the form
    :func:`parse_profile` reads: edit it and parse it to use an edited one."""
    profile = _async_of(profile)
    core = await _core_for(core, profile)
    r, data = await core.call_for_body(pb.Call(profile_spec=pb.ProfileSpec(profile=await _profile_ref(core, profile))))
    return ProfileInfo(r.profile.name, r.profile.user_agent), data


async def parse_profile(data: bytes | str, *, core: AsyncCore | None = None) -> AsyncProfile:
    """Read a profile's JSON (Go's ``profile.Parse``); an invalid one raises
    :class:`ValidationError` listing the problems."""
    core = await _core_for(core)
    r = await core.call_with_body(pb.Call(parse_profile=pb.ParseProfile()), t._bytes(data))
    return AsyncProfile(core, r.profile)


async def machines(platform: str, *, core: AsyncCore | None = None) -> list[bytes]:
    """The captured machines of ``platform`` ("macos", "windows", ...), each
    as JSON, for ``Config.client_hints``."""
    core = await _core_for(core)
    r = await core.call(pb.Call(machines=pb.Machines(platform=platform)))
    return list(r.machines.machines)


async def fetch_pins(
    config: Config, url: str, *, timeout: t.Duration | None = None, core: AsyncCore | None = None
) -> list[CertPin]:
    """The certificate chain ``url``'s server presents, for ``Config.pins``
    (Go's ``mimicry.FetchPins``)."""
    core, cfg = await _config(core, config)
    f = pb.FetchPins(config=cfg, url=url)
    t._duration(timeout, f.timeout)
    r = await core.call(pb.Call(fetch_pins=f))
    return [CertPin._from_pb(p) for p in r.pins.pins]
