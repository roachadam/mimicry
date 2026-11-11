"""The SDK's data types, Go's names in Python casing (decision 0069), and
their conversion to and from the core's messages."""

from __future__ import annotations

import dataclasses
import datetime as _dt
import enum
import json
from collections.abc import AsyncIterable, Awaitable, Callable, Iterable, Iterator, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any, TypeAlias, Union

from ._pb.mimicry.v1 import mimicry_pb2 as pb


class Kind(enum.IntEnum):
    """How a page makes a request (Go's ``mimicry.Kind``)."""

    NAVIGATE = 0
    FRAME = 1
    SCRIPT = 2
    ASYNC_SCRIPT = 3
    DEFER_SCRIPT = 4
    INJECTED_SCRIPT = 5
    PRELOAD_SCRIPT = 6
    STYLE = 7
    IMAGE = 8
    INJECTED_IMAGE = 9
    FONT = 10
    PRELOAD_FONT = 11
    FETCH = 12
    XHR = 13
    BEACON = 14
    PREFLIGHT = -1
    """Appears only in plans."""


class Priority(enum.IntEnum):
    AUTO = 0
    HIGH = 1
    LOW = 2


class CrossOrigin(enum.IntEnum):
    NONE = 0
    ANONYMOUS = 1
    USE_CREDENTIALS = 2


class Credentials(enum.IntEnum):
    SAME_ORIGIN = 0
    INCLUDE = 1
    OMIT = 2


class HintDelegation(enum.IntEnum):
    DEFAULT = 0
    DELEGATED = 1
    WITHHELD = 2


class MessageType(enum.IntEnum):
    """A WebSocket message's type (Go's ``mimicry.TextMessage``, ``BinaryMessage``)."""

    TEXT = 1
    BINARY = 2


class SameSite(enum.IntEnum):
    UNSET = 0
    LAX = 1
    STRICT = 2
    NONE = 3


class CertRole(enum.IntEnum):
    LEAF = 0
    INTERMEDIATE = 1
    ROOT = 2


Duration = float | _dt.timedelta
"""Seconds, or a :class:`datetime.timedelta`."""


def _seconds(d: Duration | None) -> float | None:
    if d is None:
        return None
    if isinstance(d, _dt.timedelta):
        return d.total_seconds()
    return float(d)


def _duration(d: Duration | None, msg: Any) -> None:
    s = _seconds(d)
    if s is None:
        return
    if s < 0:
        raise ValueError(f"a negative duration: {d!r}")
    whole = int(s)
    msg.seconds = whole
    msg.nanos = round((s - whole) * 1e9)


def _bytes(v: str | bytes | bytearray) -> bytes:
    return v.encode() if isinstance(v, str) else bytes(v)


def _enum(v: int) -> Any:
    """An enum value as the generated messages take it."""
    return int(v)


def _text(v: bytes) -> str:
    """A header value as text: UTF-8, or Latin-1 (every byte kept) when not."""
    try:
        return v.decode()
    except UnicodeDecodeError:
        return v.decode("latin-1")


HeaderValue = str | bytes
HeadersLike: TypeAlias = Union[
    "Headers",
    Mapping[str, HeaderValue | Sequence[HeaderValue] | None],
    Iterable[tuple[str, HeaderValue | None]],
]


class Headers:
    """An HTTP header: names with their values, in order, values as bytes.

    Built from a mapping (a value may be a list, or None: no values, which
    removes a header the profile would send where Go's ``Header`` does) or
    from ``(name, value)`` pairs. Names match in any case. ``get`` and
    ``get_all`` return text (UTF-8, else Latin-1); ``get_raw`` the bytes.
    """

    __slots__ = ("_items",)

    def __init__(self, init: HeadersLike | None = None) -> None:
        self._items: list[tuple[str, bytes | None]] = []
        if init is None:
            return
        if isinstance(init, Headers):
            self._items = list(init._items)
            return
        pairs: Iterable[Any] = init.items() if isinstance(init, Mapping) else init
        for name, value in pairs:
            if value is None:
                self._items.append((name, None))
            elif isinstance(value, (str, bytes, bytearray)):
                self._items.append((name, _bytes(value)))
            else:
                vals = list(value)
                if not vals:
                    self._items.append((name, None))
                self._items.extend((name, _bytes(v)) for v in vals)

    def add(self, name: str, value: HeaderValue) -> None:
        self._items.append((name, _bytes(value)))

    def get(self, name: str, default: str | None = None) -> str | None:
        """The first value of ``name``, or ``default``."""
        raw = self.get_raw(name)
        return _text(raw[0]) if raw else default

    def get_all(self, name: str) -> list[str]:
        return [_text(v) for v in self.get_raw(name)]

    def get_raw(self, name: str) -> list[bytes]:
        n = name.lower()
        return [v for k, v in self._items if v is not None and k.lower() == n]

    def items(self) -> list[tuple[str, str]]:
        """Every (name, value) pair, values as text."""
        return [(k, _text(v)) for k, v in self._items if v is not None]

    def raw_items(self) -> list[tuple[str, bytes]]:
        return [(k, v) for k, v in self._items if v is not None]

    def keys(self) -> list[str]:
        seen: dict[str, None] = {}
        for k, _ in self._items:
            seen.setdefault(k, None)
        return list(seen)

    def __getitem__(self, name: str) -> str:
        v = self.get(name)
        if v is None:
            raise KeyError(name)
        return v

    def __contains__(self, name: object) -> bool:
        return isinstance(name, str) and any(k.lower() == name.lower() for k, _ in self._items)

    def __iter__(self) -> Iterator[str]:
        return iter(self.keys())

    def __len__(self) -> int:
        return len(self.keys())

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Headers) and self._items == other._items

    def __repr__(self) -> str:
        return f"Headers({self.items()!r})"

    def _to_pb(self) -> list[pb.HeaderValues]:
        out: dict[str, pb.HeaderValues] = {}
        for k, v in self._items:
            hv = out.get(k.lower())
            if hv is None:
                hv = out[k.lower()] = pb.HeaderValues(name=k)
            if v is not None:
                hv.values.append(v)
        return list(out.values())

    @classmethod
    def _from_pb(cls, hs: Iterable[pb.HeaderValues]) -> Headers:
        h = cls()
        h._items = [(x.name, v) for x in hs for v in x.values]
        return h


def _headers_pb(h: HeadersLike | None) -> list[pb.HeaderValues]:
    if h is None:
        return []
    return (h if isinstance(h, Headers) else Headers(h))._to_pb()


@dataclass(frozen=True)
class Place:
    """Moves one header directly after or before another (Go's ``mimicry.Place``)."""

    header: str
    after: str = ""
    before: str = ""

    def _to_pb(self) -> pb.Place:
        return pb.Place(header=self.header, after=self.after, before=self.before)


@dataclass(frozen=True)
class Pin:
    """A certificate pin (Go's ``mimicry.Pin``): base64 SPKI SHA-256 hashes."""

    host: str
    spki: Sequence[str]
    include_subdomains: bool = False


@dataclass(frozen=True)
class ClientCertificate:
    """A client certificate chain and its key, both PEM, for ``host`` ("" for
    every host without an entry of its own)."""

    certificate: bytes
    key: bytes
    host: str = ""


@dataclass(frozen=True)
class Options:
    """How a page makes the request: Go's ``mimicry.Request``, what
    :func:`with_options` takes. Go's ``From`` is ``page``."""

    kind: Kind = Kind.NAVIGATE
    page: str | None = None
    priority: Priority = Priority.AUTO
    cross_origin: CrossOrigin = CrossOrigin.NONE
    credentials: Credentials = Credentials.SAME_ORIGIN
    no_cors: bool = False
    stylesheet: str | None = None
    ignore_cors: bool = False
    header_placement: Sequence[Place] = ()
    client_hints: HintDelegation = HintDelegation.DEFAULT

    def _to_pb(self) -> pb.Options:
        return pb.Options(
            kind=_enum(self.kind),
            page=self.page or "",
            priority=_enum(self.priority),
            cross_origin=_enum(self.cross_origin),
            credentials=_enum(self.credentials),
            no_cors=self.no_cors,
            stylesheet=self.stylesheet or "",
            ignore_cors=self.ignore_cors,
            header_placement=[p._to_pb() for p in self.header_placement],
            client_hints=_enum(self.client_hints),
        )


Body = bytes | bytearray | memoryview | str | Iterable[bytes] | AsyncIterable[bytes] | None


@dataclass(frozen=True)
class Request:
    """An HTTP request and how it is made (Go's ``*http.Request``).

    ``body`` is bytes (or str, sent as UTF-8), sent whole with the call so a
    redirect that keeps the body can send it again; or an iterable of bytes
    (an async iterable with the async API), streamed, which cannot be sent
    again (``content_length`` then gives its length if known).
    ``options`` are :func:`with_options`' (None: a navigation), ``exact``
    :func:`exact`'s, ``header_order`` Go's ``HeaderOrderKey``. ``proxy``
    overrides the client's for this request ("" connects directly).
    ``timeout`` bounds the call, response body included.
    """

    method: str
    url: str
    headers: HeadersLike | None = None
    body: Body = None
    content_length: int | None = None
    options: Options | None = None
    exact: bool = False
    header_order: Sequence[str] | None = None
    proxy: str | None = None
    timeout: Duration | None = None

    def _streamed(self) -> bool:
        return self.body is not None and not isinstance(self.body, (bytes, bytearray, memoryview, str))

    def _to_pb(self) -> pb.Request:
        r = pb.Request(
            method=self.method,
            url=self.url,
            header=_headers_pb(self.headers),
            exact=self.exact,
            header_order=list(self.header_order or ()),
        )
        if self.options is not None:
            r.options.CopyFrom(self.options._to_pb())
        if self._streamed():
            r.stream_body = True
            if self.content_length is not None:
                r.content_length = self.content_length
        elif self.body is not None:
            r.body = _bytes(self.body) if isinstance(self.body, str) else bytes(self.body)  # type: ignore[arg-type]
        if self.proxy is not None:
            r.proxy = self.proxy
        _duration(self.timeout, r.timeout)
        return r


def as_kind(req: Request, kind: Kind) -> Request:
    """A copy of ``req`` made as ``kind`` from no page (Go's ``mimicry.As``):
    ``with_options(req, Options(kind=kind))``."""
    return with_options(req, Options(kind=kind))


def with_options(req: Request, options: Options) -> Request:
    """A copy of ``req`` made as ``options`` describe (Go's ``mimicry.With``)."""
    return dataclasses.replace(req, options=options)


def exact(req: Request) -> Request:
    """A copy of ``req`` that sends exactly its own headers, in its own order
    (``header_order``) or sorted by name, plus ``Config.header`` (Go's
    ``mimicry.Exact``)."""
    return dataclasses.replace(req, exact=True)


@dataclass(frozen=True)
class WebSocketOptions:
    """Go's ``mimicry.WebSocketOptions``; Go's ``From`` is ``page``."""

    page: str | None = None
    protocols: Sequence[str] = ()
    header: HeadersLike | None = None

    def _to_pb(self) -> pb.WebSocketOptions:
        return pb.WebSocketOptions(
            page=self.page or "",
            protocols=list(self.protocols),
            header=_headers_pb(self.header),
        )


@dataclass(frozen=True)
class ConnectionState:
    """A TLS connection's state (Go's ``tls.ConnectionState``); certificates
    are DER, leaf first."""

    version: int
    handshake_complete: bool
    did_resume: bool
    cipher_suite: int
    negotiated_protocol: str
    server_name: str
    peer_certificates: list[bytes]
    verified_chains: list[list[bytes]]
    signed_certificate_timestamps: list[bytes]
    ocsp_response: bytes
    ech_accepted: bool

    @classmethod
    def _from_pb(cls, c: pb.ConnectionState) -> ConnectionState:
        return cls(
            version=c.version,
            handshake_complete=c.handshake_complete,
            did_resume=c.did_resume,
            cipher_suite=c.cipher_suite,
            negotiated_protocol=c.negotiated_protocol,
            server_name=c.server_name,
            peer_certificates=list(c.peer_certificates),
            verified_chains=[list(ch.certificates) for ch in c.verified_chains],
            signed_certificate_timestamps=list(c.signed_certificate_timestamps),
            ocsp_response=c.ocsp_response,
            ech_accepted=c.ech_accepted,
        )


@dataclass(frozen=True)
class ResponseHead:
    """What a response is before its body: status, headers, the final URL."""

    status_code: int
    status: str
    proto: str
    headers: Headers
    content_length: int | None
    uncompressed: bool
    url: str
    tls: ConnectionState | None

    @classmethod
    def _from_pb(cls, r: pb.Response) -> ResponseHead:
        return cls(
            status_code=r.status_code,
            status=r.status,
            proto=r.proto,
            headers=Headers._from_pb(r.header),
            content_length=None if r.content_length < 0 else r.content_length,
            uncompressed=r.uncompressed,
            url=r.url,
            tls=ConnectionState._from_pb(r.tls) if r.HasField("tls") else None,
        )


@dataclass(frozen=True)
class Lines:
    """What one protocol would send (Go's ``mimicry.Lines``)."""

    request_line: str
    headers: list[tuple[str, bytes]]
    error: Exception | None

    @classmethod
    def _from_pb(cls, x: pb.Lines) -> Lines:
        from ._errors import from_pb

        return cls(
            request_line=x.request_line,
            headers=[(h.name, h.value) for h in x.headers],
            error=from_pb(x.error) if x.HasField("error") else None,
        )


@dataclass(frozen=True)
class Override:
    name: str
    mimicry: bytes
    caller: list[bytes]


@dataclass(frozen=True)
class Plan:
    """What a request would send, without sending it (Go's ``mimicry.Plan``);
    ``text`` is Go's ``Plan.String()``."""

    method: str
    url: str
    kind: Kind
    variant: str
    condition: list[str]
    protocol: str
    http2: Lines
    http1: Lines
    http3: Lines | None
    preflight: Plan | None
    overrides: list[Override]
    dropped: list[str]
    text: str

    def __str__(self) -> str:
        return self.text

    @classmethod
    def _from_pb(cls, p: pb.PlanReply) -> Plan:
        return cls(
            method=p.method,
            url=p.url,
            kind=Kind(p.kind),
            variant=p.variant,
            condition=list(p.condition),
            protocol=p.protocol,
            http2=Lines._from_pb(p.http2),
            http1=Lines._from_pb(p.http1),
            http3=Lines._from_pb(p.http3) if p.HasField("http3") else None,
            preflight=Plan._from_pb(p.preflight) if p.HasField("preflight") else None,
            overrides=[Override(o.name, o.mimicry, list(o.caller)) for o in p.overrides],
            dropped=list(p.dropped),
            text=p.text,
        )


@dataclass(frozen=True)
class CertPin:
    """One certificate of a chain, for pinning (Go's ``mimicry.CertPin``)."""

    role: CertRole
    subject: str
    issuer: str
    not_after: _dt.datetime | None
    spki: str
    spki_hex: str
    fingerprint: str

    @classmethod
    def _from_pb(cls, p: pb.CertPin) -> CertPin:
        return cls(
            role=CertRole(p.role),
            subject=p.subject,
            issuer=p.issuer,
            not_after=_time(p.not_after) if p.HasField("not_after") else None,
            spki=p.spki,
            spki_hex=p.spki_hex,
            fingerprint=p.fingerprint,
        )


def _time(ts: Any) -> _dt.datetime:
    return _dt.datetime.fromtimestamp(ts.seconds + ts.nanos / 1e9, tz=_dt.UTC)


def _set_time(t: _dt.datetime | None, msg: Any) -> None:
    if t is None:
        return
    if t.tzinfo is None:
        raise ValueError("a naive datetime: give its timezone")
    s = t.timestamp()
    whole = int(s // 1)
    msg.seconds = whole
    msg.nanos = round((s - whole) * 1e9)


@dataclass
class Cookie:
    """A cookie in a jar (Go's ``cookies.Cookie``). ``name``, ``value`` and
    ``raw`` are bytes, as on the wire; ``expires`` None is a session cookie."""

    name: bytes
    value: bytes
    domain: str
    path: str = "/"
    host_only: bool = False
    expires: _dt.datetime | None = None
    secure: bool = False
    http_only: bool = False
    same_site: SameSite = SameSite.UNSET
    partitioned: bool = False
    partition: str = ""
    created: _dt.datetime | None = None
    last_access: _dt.datetime | None = None
    raw: bytes = b""

    @classmethod
    def _from_pb(cls, c: pb.Cookie) -> Cookie:
        return cls(
            name=c.name,
            value=c.value,
            domain=c.domain,
            path=c.path,
            host_only=c.host_only,
            expires=_time(c.expires) if c.HasField("expires") else None,
            secure=c.secure,
            http_only=c.http_only,
            same_site=SameSite(c.same_site),
            partitioned=c.partitioned,
            partition=c.partition,
            created=_time(c.created) if c.HasField("created") else None,
            last_access=_time(c.last_access) if c.HasField("last_access") else None,
            raw=c.raw,
        )

    def _to_pb(self) -> pb.Cookie:
        c = pb.Cookie(
            name=_bytes(self.name),
            value=_bytes(self.value),
            domain=self.domain,
            host_only=self.host_only,
            path=self.path,
            secure=self.secure,
            http_only=self.http_only,
            same_site=_enum(self.same_site),
            partitioned=self.partitioned,
            partition=self.partition,
            raw=_bytes(self.raw),
        )
        _set_time(self.expires, c.expires)
        _set_time(self.created, c.created)
        _set_time(self.last_access, c.last_access)
        return c


@dataclass(frozen=True)
class CookieRequest:
    """How a request meets the jar (Go's ``cookies.Request``)."""

    url: str
    method: str = "GET"
    navigation: bool = False
    page: str | None = None
    redirects: Sequence[str] = ()
    websocket: bool = False

    def _to_pb(self) -> pb.CookieRequest:
        return pb.CookieRequest(
            url=self.url,
            method=self.method,
            navigation=self.navigation,
            page=self.page or "",
            redirects=list(self.redirects),
            websocket=self.websocket,
        )


@dataclass(frozen=True)
class SetCookieEvent:
    """One Set-Cookie line a jar made with ``on_set_cookie`` received
    (Go's ``cookies.Event``): the cookie stored, or why not."""

    url: str
    line: bytes
    cookie: Cookie | None
    rejected: str
    deleted: bool

    @classmethod
    def _from_pb(cls, e: pb.SetCookie) -> SetCookieEvent:
        return cls(
            url=e.url,
            line=e.line,
            cookie=Cookie._from_pb(e.cookie) if e.HasField("cookie") else None,
            rejected=e.rejected,
            deleted=e.deleted,
        )


@dataclass(frozen=True)
class ProfileInfo:
    """A profile's name and user agent."""

    name: str
    user_agent: str


@dataclass(frozen=True)
class Newest:
    """The newest built-in profile of a browser on a platform, as Go's
    ``profiles.ChromeMacOS()`` and the other accessors return it; resolved
    against the core's list when a client is made (:mod:`mimicry.profiles`)."""

    browser: str
    platform: str

    def resolve(self, names: Iterable[str]) -> str:
        best: tuple[int, str] | None = None
        for n in names:
            parts = n.split("-")
            if len(parts) != 3 or parts[0] != self.browser or parts[2] != self.platform:
                continue
            try:
                v = int(parts[1])
            except ValueError:
                continue
            if best is None or v > best[0]:
                best = (v, n)
        if best is None:
            raise LookupError(f"no built-in {self.browser} profile for {self.platform}")
        return best[1]


VerifyCallback = Callable[[ConnectionState], Awaitable[None] | None]
"""Called with each verified connection's state; raise to reject it."""


@dataclass
class Config:
    """Go's ``mimicry.Config``, callbacks as data (decision 0069).

    ``profile`` is a built-in profile's name (``"chrome-157-macos"``), a
    :class:`Newest` from :mod:`mimicry.profiles`, or a parsed profile.
    ``cookies``, ``tickets``, ``alt_svc`` and ``client_hint_store`` are
    objects made by the same API (async or sync) as the client.
    ``verify_connection`` is called with each verified connection's state
    and rejects it by raising. Durations are seconds or timedeltas.
    """

    profile: Any
    root_cas: bytes | None = None
    dial_timeout: Duration | None = None
    timeout: Duration | None = None
    local_addrs: Sequence[str] = ()
    dns_server: str = ""
    proxy: str = ""
    header: HeadersLike | None = None
    header_placement: Sequence[Place] = ()
    cookies: Any = None
    client_hint_store: Any = None
    client_hints: bytes | Mapping[str, Any] | None = None
    disable_decompression: bool = False
    disable_http3: bool = False
    alt_svc: Any = None
    tickets: Any = None
    browser_quic_fallback: bool = False
    browser_dial_delay: bool = False
    browser_proxy_auth: bool = False
    disable_coalescing: bool = False
    max_conns_per_host: int = 0
    idle_conn_timeout: Duration | None = None
    max_idle_proxies: int = 0
    http3_over_socks5: bool = False
    connect_udp_proxy: Mapping[str, str] = field(default_factory=dict)
    http3_read_buffer_size: int = 0
    http3_ack_every: int = 0
    pins: Sequence[Pin] = ()
    verify_connection: VerifyCallback | None = None
    client_certificates: Sequence[ClientCertificate] = ()
    key_log_file: str = ""

    def _to_pb(self, profile: pb.ProfileRef, objects: Mapping[str, int]) -> pb.Config:
        """As a message; ``profile`` resolved and the objects' ids given by
        the caller, which knows their core."""
        c = pb.Config(
            profile=profile,
            root_cas=self.root_cas or b"",
            local_addrs=list(self.local_addrs),
            dns_server=self.dns_server,
            proxy=self.proxy,
            header=_headers_pb(self.header),
            header_placement=[p._to_pb() for p in self.header_placement],
            cookies=objects.get("cookies", 0),
            client_hint_store=objects.get("client_hint_store", 0),
            disable_decompression=self.disable_decompression,
            disable_http3=self.disable_http3,
            alt_svc=objects.get("alt_svc", 0),
            tickets=objects.get("tickets", 0),
            browser_quic_fallback=self.browser_quic_fallback,
            browser_dial_delay=self.browser_dial_delay,
            browser_proxy_auth=self.browser_proxy_auth,
            disable_coalescing=self.disable_coalescing,
            max_conns_per_host=self.max_conns_per_host,
            max_idle_proxies=self.max_idle_proxies,
            http3_over_socks5=self.http3_over_socks5,
            connect_udp_proxy=dict(self.connect_udp_proxy),
            http3_read_buffer_size=self.http3_read_buffer_size,
            http3_ack_every=self.http3_ack_every,
            pins=[pb.Pin(host=p.host, include_subdomains=p.include_subdomains, spki=list(p.spki)) for p in self.pins],
            verify_connection=self.verify_connection is not None,
            client_certificates=[
                pb.ClientCertificate(host=c.host, certificate=c.certificate, key=c.key)
                for c in self.client_certificates
            ],
            key_log_file=self.key_log_file,
        )
        if self.client_hints is not None:
            c.client_hints = (
                self.client_hints
                if isinstance(self.client_hints, (bytes, bytearray))
                else json.dumps(self.client_hints).encode()
            )
        _duration(self.dial_timeout, c.dial_timeout)
        _duration(self.timeout, c.timeout)
        _duration(self.idle_conn_timeout, c.idle_conn_timeout)
        return c
