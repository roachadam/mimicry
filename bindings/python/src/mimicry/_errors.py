"""The exceptions: one class per error code of the core (decision 0069).

An error from the core names its most specific code and every code its Go
error chain matches (``errors.Is``/``errors.As``). The exception raised is an
instance of the class of each matched code: a proxy failure at its deadline
is caught by ``except ProxyError`` and by ``except DeadlineError`` (and so by
``except TimeoutError``). The classes for a set of matches are made once and
cached.
"""

from __future__ import annotations

import enum
from collections.abc import Iterable
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from ._pb.mimicry.v1 import mimicry_pb2 as pb


class Code(enum.IntEnum):
    """An error's kind: one per sentinel and error type of Go's API."""

    INTERNAL = 0
    NOT_MEASURED = 1
    CORS = 2
    PROXY = 3
    PIN = 4
    BLOCKED = 5
    PLACEMENT = 6
    CLOSE = 7
    VALIDATION = 8
    CANCELED = 9
    DEADLINE = 10
    NET = 11
    INVALID = 12
    VERIFY = 13


class MimicryError(Exception):
    """The base of every exception the SDK raises.

    ``code`` is the most specific :class:`Code` (None for an error of the SDK
    itself, such as :class:`CoreExited`), ``matches`` every code it matches,
    ``message`` the core's text.
    """

    code: Code | None = None

    def __init__(self, message: str, *, matches: Iterable[Code] = ()) -> None:
        super().__init__(message)
        self.message = message
        own = type(self).code
        self.matches: frozenset[Code] = frozenset(matches) | ({own} if own is not None else set())

    def __str__(self) -> str:
        return self.message

    def __reduce__(self) -> tuple[Any, ...]:
        return (_rebuild, (_base_of(type(self)), self.message, tuple(self.matches), self.__dict__))


class InternalError(MimicryError):
    """Any error without a code of its own; also a bug in the core."""

    code = Code.INTERNAL


class NotMeasuredError(MimicryError):
    """The profile never measured what the request needs (ErrNotMeasured):
    nothing was sent."""

    code = Code.NOT_MEASURED


class CORSError(MimicryError):
    """A request or redirect CORS refuses (*CORSError)."""

    code = Code.CORS
    url: str = ""
    redirect: bool = False
    reason: str = ""
    status: int = 0
    header: list[tuple[str, bytes]]


class ProxyError(MimicryError):
    """The proxy refused or failed (*ProxyError); its cause is in ``matches``."""

    code = Code.PROXY
    proxy: str = ""
    status_code: int = 0
    header: list[tuple[str, bytes]]


class PinError(MimicryError):
    """A server's certificate chain matched none of its pins (*PinError)."""

    code = Code.PIN
    host: str = ""
    presented: list[str]


class BlockedError(MimicryError):
    """The browser would block the request (ErrBlocked): nothing was sent."""

    code = Code.BLOCKED


class PlacementError(MimicryError):
    """A header placement that cannot apply (ErrPlacement): nothing was sent."""

    code = Code.PLACEMENT


class CloseError(MimicryError):
    """A WebSocket closed: its close code and reason (*CloseError)."""

    code = Code.CLOSE
    close_code: int = 0
    reason: str = ""


class ValidationError(MimicryError):
    """An invalid profile (*profile.ValidationError): ``problems`` are
    ``(path, message)`` pairs."""

    code = Code.VALIDATION
    problems: list[tuple[str, str]]


class CanceledError(MimicryError):
    """The call was canceled: its stream reset (context.Canceled)."""

    code = Code.CANCELED


class DeadlineError(MimicryError, TimeoutError):
    """A deadline passed: a timeout (context.DeadlineExceeded)."""

    code = Code.DEADLINE


class NetError(MimicryError, ConnectionError):
    """A network failure: a connection refused or reset, a failed lookup, a
    TLS failure."""

    code = Code.NET


class InvalidError(MimicryError, ValueError):
    """A call the core cannot carry out as given: a malformed argument, an
    object of another type."""

    code = Code.INVALID


class VerifyError(MimicryError):
    """The ``verify_connection`` callback rejected the connection."""

    code = Code.VERIFY


class CoreExited(MimicryError):
    """mimicry-core exited: ``returncode`` is its exit status (negative: the
    signal that ended it, as :mod:`subprocess` reports it), ``stderr`` the
    last of what it wrote there. Every object of that core is gone."""

    def __init__(self, returncode: int | None, stderr: str) -> None:
        status = "is gone" if returncode is None else f"exited with status {returncode}"
        msg = f"mimicry-core {status}"
        if stderr.strip():
            msg += f": {stderr.strip()}"
        super().__init__(msg)
        self.returncode = returncode
        self.stderr = stderr


class ProtocolError(MimicryError):
    """The connection to mimicry-core broke the protocol (or was lost while
    the core still ran); the core was stopped."""


_BY_CODE: dict[Code, type[MimicryError]] = {
    cls.code: cls
    for cls in (
        InternalError,
        NotMeasuredError,
        CORSError,
        ProxyError,
        PinError,
        BlockedError,
        PlacementError,
        CloseError,
        ValidationError,
        CanceledError,
        DeadlineError,
        NetError,
        InvalidError,
        VerifyError,
    )
    if cls.code is not None
}

for _cls in (MimicryError, CoreExited, ProtocolError, *_BY_CODE.values()):
    _cls.__module__ = "mimicry"

_combined: dict[tuple[Code, frozenset[Code]], type[MimicryError]] = {}


def error_class(code: Code, matches: Iterable[Code] = ()) -> type[MimicryError]:
    """The class for an error of ``code`` that also matches ``matches``: the
    code's own class, or a subclass of it and each other match's class."""
    others = frozenset(m for m in matches if m != code and m in _BY_CODE)
    base = _BY_CODE.get(code, InternalError)
    if not others:
        return base
    key = (code, others)
    cls = _combined.get(key)
    if cls is None:
        bases = (base, *(_BY_CODE[m] for m in sorted(others)))
        cls = type(base.__name__, bases, {"__module__": base.__module__, "__qualname__": base.__qualname__})
        _combined[key] = cls
    return cls


def _base_of(cls: type[MimicryError]) -> type[MimicryError]:
    """The public class a combined class stands for."""
    return cls if cls in _BY_CODE.values() or cls.code is None else _BY_CODE[cls.code]


def _rebuild(base: type[MimicryError], message: str, matches: tuple[Code, ...], state: dict[str, Any]) -> MimicryError:
    if base.code is None:
        e = base.__new__(base)
        Exception.__init__(e, message)
    else:
        e = error_class(base.code, matches)(message, matches=matches)
    e.__dict__.update(state)
    return e


def _headers(hs: Iterable[pb.HeaderValues]) -> list[tuple[str, bytes]]:
    return [(h.name, v) for h in hs for v in h.values]


def from_pb(e: pb.Error) -> MimicryError:
    """The exception for an error the core sent."""
    code = Code(e.code) if e.code in Code._value2member_map_ else Code.INTERNAL
    matches = [Code(m) for m in e.matches if m in Code._value2member_map_]
    out = error_class(code, matches)(e.message, matches=matches)
    err: Any = out
    if e.HasField("cors"):
        err.url = e.cors.url
        err.redirect = e.cors.redirect
        err.reason = e.cors.reason
        err.status = e.cors.status
        err.header = _headers(e.cors.header)
    if e.HasField("proxy"):
        err.proxy = e.proxy.proxy
        err.status_code = e.proxy.status_code
        err.header = _headers(e.proxy.header)
    if e.HasField("pin"):
        err.host = e.pin.host
        err.presented = list(e.pin.presented)
    if e.HasField("close"):
        err.close_code = e.close.code
        err.reason = e.close.reason
    if e.HasField("validation"):
        err.problems = [(p.path, p.message) for p in e.validation.problems]
    return out
