"""The framed, multiplexed protocol between mimicry-core and the SDK, sans IO.

A port of ``internal/rpc/wire`` (decision 0069): frames, streams, per-stream
credit, RESET and EVENT, as ``proto/testdata/wire/README.md`` describes them,
with no I/O of its own. Bytes read from the core go in through
:meth:`Connection.receive_data`; bytes to write come out of
:meth:`Connection.data_to_send`. The asyncio driver (``_conn``) does the I/O
and the waiting.

Both roles are implemented, so the language-neutral test vectors, written for
either side, all replay against this code.

Nothing grows without bound: a stream buffers at most ``window`` bytes of the
peer's DATA (the credit it granted), each side has at most ``max_streams``
streams open, and a frame carries at most :data:`MAX_PAYLOAD` bytes.
"""

from __future__ import annotations

import enum
import struct
from collections import deque
from dataclasses import dataclass

MAX_PAYLOAD = 64 << 10
"""The most a frame carries."""

HEADER_SIZE = 10
DEFAULT_WINDOW = 256 << 10
DEFAULT_MAX_STREAMS = 256
_MAX_CREDIT = (1 << 31) - 1
_FLAG_END = 0x01

_header = struct.Struct(">IBBI")
_u32 = struct.Struct(">I")


class FrameType(enum.IntEnum):
    CALL = 1
    REPLY = 2
    DATA = 3
    WINDOW = 4
    RESET = 5
    EVENT = 6


class ResetCode(enum.IntEnum):
    """Why a stream or the connection was reset; a RESET carries one."""

    CANCEL = 0
    STREAM_LIMIT = 1
    CREDIT_OVERRUN = 2
    FRAME_TOO_LARGE = 3
    UNKNOWN_TYPE = 4
    UNKNOWN_FLAGS = 5
    STREAM_CLOSED = 6
    STREAM_NOT_OPEN = 7
    WRONG_PARITY = 8
    BAD_FRAME = 9


def code_name(code: int) -> str:
    """The name of a reset code, ``CODE_<n>`` for one this side does not know."""
    try:
        return ResetCode(code).name
    except ValueError:
        return f"CODE_{code}"


class Role(enum.Enum):
    SDK = 1
    """Opens odd streams with CALL; accepts the core's EVENT streams and notices."""
    CORE = 2
    """Opens even streams with EVENT; sends notices; accepts CALL streams."""


class WireError(Exception):
    """A failure of the protocol: of a stream, or of the whole connection."""


class ProtocolViolation(WireError):
    """A frame the protocol does not allow, found by this side."""

    def __init__(self, code: ResetCode, message: str) -> None:
        super().__init__(f"wire: {message}")
        self.code = code


class RemoteReset(WireError):
    """A RESET the peer sent: of a stream, or of the connection (stream 0)."""

    def __init__(self, stream: int, code: int) -> None:
        what = "the connection" if stream == 0 else f"stream {stream}"
        super().__init__(f"wire: the peer reset {what} ({code_name(code)})")
        self.stream = stream
        self.code = code


class Truncated(WireError):
    """The connection ended inside a frame."""


class StreamCanceled(WireError):
    """This side reset the stream (its caller gave up)."""


class ConnectionClosed(WireError):
    """The connection failed: every open stream fails with it.

    ``cause`` is why: a :class:`ProtocolViolation`, a :class:`RemoteReset` of
    stream 0, :class:`Truncated`, an I/O error, or None for a clean end.
    """

    def __init__(self, cause: BaseException | None = None) -> None:
        msg = "wire: connection closed"
        if cause is not None:
            msg = f"{msg}: {cause}"
        super().__init__(msg)
        self.cause = cause


@dataclass(frozen=True, slots=True)
class StreamChanged:
    """A stream's state changed: a reply, DATA, END, credit, a reset."""

    stream: Stream


@dataclass(frozen=True, slots=True)
class StreamAccepted:
    """The peer opened a stream: :meth:`Connection.accept` returns it."""


@dataclass(frozen=True, slots=True)
class Notice:
    """An EVENT on stream 0: a notice nothing answers."""

    payload: bytes


@dataclass(frozen=True, slots=True)
class ConnectionFailed:
    error: ConnectionClosed


Event = StreamChanged | StreamAccepted | Notice | ConnectionFailed


def frame(stream: int, typ: FrameType, end: bool, payload: bytes = b"") -> bytes:
    """One frame's bytes."""
    return _header.pack(stream, typ, _FLAG_END if end else 0, len(payload)) + payload


def _reset_frame(sid: int, code: int) -> bytes:
    return frame(sid, FrameType.RESET, False, _u32.pack(code))


class Stream:
    """One call: its opening frame (CALL or EVENT), one REPLY, then DATA both
    ways, each direction ended by END. Its state; the :class:`Connection`
    that made it acts on it."""

    __slots__ = (
        "buffered",
        "err",
        "freed",
        "got_reply",
        "id",
        "local",
        "payload",
        "pending_window",
        "recv",
        "recv_credit",
        "recv_ended",
        "released",
        "reply",
        "reply_sent",
        "reset_queued",
        "reset_sent",
        "send_credit",
        "send_ended",
        "unreturned",
        "window_queued",
    )

    def __init__(self, sid: int, local: bool, window: int) -> None:
        self.id = sid
        self.local = local
        # payload is the opening frame's, of a stream the peer opened.
        self.payload = b""
        self.err: WireError | None = None
        self.reply = b""
        self.got_reply = False
        self.reply_sent = False
        # recv is the peer's DATA not yet read, buffered bytes of it. The
        # credit granted the peer (recv_credit) plus buffered plus unreturned
        # (read, not yet granted back) is the window until END.
        self.recv: deque[bytes] = deque()
        self.buffered = 0
        self.recv_credit = window
        self.unreturned = 0
        self.pending_window = 0
        self.window_queued = False
        self.recv_ended = False
        self.send_credit = window
        self.send_ended = False
        self.reset_sent = False
        # reset_queued: a RESET of this side's waits to be written; freed: the
        # stream ended; released: forgotten, its max_streams place free.
        self.reset_queued = False
        self.freed = False
        self.released = False

    @property
    def ended(self) -> bool:
        """Both sides stopped sending on it (or it failed)."""
        return self.freed

    def __repr__(self) -> str:
        return f"<wire.Stream {self.id}>"


class _Out:
    """A frame waiting to be written; a WINDOW is built when written, from
    its stream's pending credit."""

    __slots__ = ("buf", "open", "s", "typ")

    def __init__(self, s: Stream | None, typ: FrameType, buf: bytes, open_: bool = False) -> None:
        self.s = s
        self.typ = typ
        self.buf = buf
        # open marks a stream's opening frame, written even if the stream was
        # reset meanwhile (its RESET follows it).
        self.open = open_


class Connection:
    """One end of a connection, its state only.

    Every method runs to completion without waiting. The caller waits (on
    the events :meth:`receive_data` returns and on :meth:`can_open`) and
    writes :meth:`data_to_send` after each call that may have queued frames.
    """

    def __init__(
        self,
        role: Role,
        window: int = DEFAULT_WINDOW,
        max_streams: int = DEFAULT_MAX_STREAMS,
    ) -> None:
        if not 1 <= window <= _MAX_CREDIT:
            raise ValueError(f"wire: window {window} out of range")
        if max_streams < 1:
            raise ValueError(f"wire: max_streams {max_streams} out of range")
        self.role = role
        self.window = window
        self.max_streams = max_streams
        self.error: ConnectionClosed | None = None
        self._streams: dict[int, Stream] = {}
        self._next_id = 1 if role is Role.SDK else 2
        self._last_peer = 0
        self._peer_open = 0
        self._local_open = 0
        self._accept_q: deque[Stream] = deque()
        self._wq: deque[_Out] = deque()
        self._abort: bytes | None = None
        self._done_writing = False
        # A frame cut across reads: its bytes so far.
        self._partial = bytearray()
        self._events: list[Event] = []

    # -- opening and answering ------------------------------------------

    def can_open(self) -> bool:
        """Whether :meth:`open_stream` has a free stream now (or would raise
        the connection's error)."""
        return self.error is not None or self._local_open < self.max_streams

    def open_stream(self, payload: bytes, end: bool) -> Stream:
        """Open a stream: CALL on the SDK's side, EVENT on the core's.

        ``end`` says nothing follows in this direction. Raises the
        connection's error once it failed, and RuntimeError while this side
        has ``max_streams`` open (wait for :meth:`can_open`).
        """
        _check_payload(payload)
        if self.error is not None:
            raise self.error
        if self._local_open >= self.max_streams:
            raise RuntimeError("wire: every stream is in use")
        if self._next_id > 0xFFFFFFFF:
            raise RuntimeError("wire: stream ids exhausted")
        sid = self._next_id
        self._next_id += 2
        s = Stream(sid, True, self.window)
        self._streams[sid] = s
        self._local_open += 1
        if end:
            s.send_ended = True
        typ = FrameType.CALL if self.role is Role.SDK else FrameType.EVENT
        self._wq.append(_Out(s, typ, frame(sid, typ, end, payload), True))
        return s

    def notify(self, payload: bytes) -> None:
        """Send a notice, an EVENT on stream 0 (the core's side only)."""
        if self.role is not Role.CORE:
            raise RuntimeError("wire: only the core sends notices")
        _check_payload(payload)
        if self.error is not None:
            raise self.error
        self._wq.append(_Out(None, FrameType.EVENT, frame(0, FrameType.EVENT, False, payload)))

    def accept(self) -> Stream | None:
        """The next stream the peer opened (its opening frame's payload in
        ``payload``), None if there is none. One the peer reset before it was
        accepted is never returned."""
        if self._accept_q:
            return self._accept_q.popleft()
        return None

    def send_reply(self, s: Stream, payload: bytes, end: bool) -> None:
        """Answer a stream the peer opened; ``end`` says no DATA follows."""
        _check_payload(payload)
        if s.local:
            raise RuntimeError("wire: a reply on a stream this side opened")
        if s.err is not None:
            raise s.err
        if s.reply_sent:
            raise RuntimeError("wire: a second REPLY")
        s.reply_sent = True
        if end:
            s.send_ended = True
        self._wq.append(_Out(s, FrameType.REPLY, frame(s.id, FrameType.REPLY, end, payload)))
        self._maybe_free(s)

    # -- sending DATA ---------------------------------------------------

    def send_window(self, s: Stream) -> int:
        """How many bytes of DATA ``s`` may send now. Raises its error, or
        RuntimeError after its END."""
        _send_check(s)
        return s.send_credit

    def send_data(self, s: Stream, data: bytes | bytearray | memoryview) -> int:
        """Queue as much of ``data`` as the credit allows, in frames of at
        most :data:`MAX_PAYLOAD`; returns how many bytes were queued."""
        _send_check(s)
        n = min(len(data), s.send_credit)
        mv = memoryview(data)
        off = 0
        while off < n:
            k = min(n - off, MAX_PAYLOAD)
            self._wq.append(
                _Out(
                    s,
                    FrameType.DATA,
                    _header.pack(s.id, FrameType.DATA, 0, k) + mv[off : off + k],
                )
            )
            off += k
        s.send_credit -= n
        return n

    def end_stream(self, s: Stream) -> None:
        """End this side's DATA (END)."""
        _send_check(s)
        s.send_ended = True
        self._wq.append(_Out(s, FrameType.DATA, frame(s.id, FrameType.DATA, True)))
        self._maybe_free(s)

    def reset_stream(self, s: Stream, code: int = ResetCode.CANCEL) -> None:
        """Reset ``s`` if either direction is still open: the peer's side
        fails with ``code``, this side's with :class:`StreamCanceled`."""
        self._reset(s, code, StreamCanceled(f"wire: stream {s.id} reset with {code_name(code)}"))

    # -- reading --------------------------------------------------------

    def reply(self, s: Stream) -> bytes | None:
        """The REPLY to a stream this side opened, None while it has none.
        Raises the stream's error."""
        if not s.local:
            raise RuntimeError("wire: the reply of a stream the peer opened")
        if s.got_reply:
            return s.reply
        if s.err is not None:
            raise s.err
        return None

    def read(self, s: Stream, max_bytes: int = -1) -> bytes | None:
        """Up to ``max_bytes`` of the peer's DATA; if negative, the oldest
        DATA frame's payload not yet read, as it came (no copy).

        None while nothing is buffered, ``b""`` after the peer's END. Raises
        the stream's error. Bytes read are granted back to the peer as credit
        once they reach half the window.
        """
        if s.err is not None:
            raise s.err
        if s.buffered > 0 and max_bytes != 0:
            return self._take(s, max_bytes)
        if s.recv_ended:
            return b""
        return None

    # -- the connection -------------------------------------------------

    def close(self) -> list[Event]:
        """Fail the connection from this side; the caller closes the carrier."""
        self._fail(ConnectionClosed(), None)
        return self._take_events()

    def receive_eof(self) -> list[Event]:
        """The carrier ended: cleanly between frames, or inside one."""
        if self._partial:
            self._fail(ConnectionClosed(Truncated("wire: connection ended inside a frame")), None)
        else:
            self._fail(ConnectionClosed(), None)
        return self._take_events()

    def receive_error(self, err: BaseException) -> list[Event]:
        """The carrier failed with an I/O error."""
        self._fail(ConnectionClosed(err), None)
        return self._take_events()

    def receive_data(self, data: bytes | bytearray | memoryview) -> list[Event]:
        """Apply bytes read from the peer; returns what changed."""
        if self.error is not None:
            return []
        mv = memoryview(data)
        try:
            if self._partial:
                mv = self._fill_partial(mv)
            while len(mv) >= HEADER_SIZE:
                sid, typ, flags, length = _header.unpack_from(mv)
                _check_header(sid, typ, flags, length)
                end = HEADER_SIZE + length
                if len(mv) < end:
                    break
                payload = bytes(mv[HEADER_SIZE:end])
                mv = mv[end:]
                self._handle(sid, FrameType(typ), flags, payload)
            if len(mv):
                self._partial += mv
        except ProtocolViolation as e:
            self._fail(ConnectionClosed(e), _reset_frame(0, e.code))
        except RemoteReset as e:
            self._fail(ConnectionClosed(e), None)
        return self._take_events()

    def data_to_send(self) -> bytes:
        """The bytes to write now, possibly none.

        Once the connection failed, it is the RESET of stream 0 naming a
        protocol error this side found, once, then nothing; the caller then
        closes the carrier.
        """
        if self.error is not None:
            abort, self._abort = self._abort, None
            return abort or b""
        out: list[bytes] = []
        wq = self._wq
        while wq:
            b = self._prepare(wq.popleft())
            if b:
                out.append(b)
        if len(out) == 1:
            return out[0]
        return b"".join(out)

    # -- internals ------------------------------------------------------

    def _take_events(self) -> list[Event]:
        ev, self._events = self._events, []
        return ev

    def _changed(self, s: Stream) -> None:
        self._events.append(StreamChanged(s))

    def _fill_partial(self, mv: memoryview) -> memoryview:
        """Complete the frame begun in an earlier read; returns the rest."""
        p = self._partial
        if len(p) < HEADER_SIZE:
            k = min(HEADER_SIZE - len(p), len(mv))
            p += mv[:k]
            mv = mv[k:]
            if len(p) < HEADER_SIZE:
                return mv
        sid, typ, flags, length = _header.unpack_from(p)
        _check_header(sid, typ, flags, length)
        need = HEADER_SIZE + length - len(p)
        k = min(need, len(mv))
        p += mv[:k]
        mv = mv[k:]
        if k < need:
            return mv
        payload = bytes(p[HEADER_SIZE:])
        self._partial = bytearray()
        self._handle(sid, FrameType(typ), flags, payload)
        return mv

    def _peer_open_type(self) -> tuple[FrameType, int]:
        if self.role is Role.CORE:
            return FrameType.CALL, 1
        return FrameType.EVENT, 0

    def _handle(self, sid: int, typ: FrameType, flags: int, p: bytes) -> None:
        self._check(sid, typ, flags, p)
        if sid == 0:
            if typ is FrameType.RESET:
                raise RemoteReset(0, _u32.unpack(p)[0])
            # A notice: _check lets EVENT through on the SDK's side only.
            self._events.append(Notice(p))
            return
        # A stream ended but still holding its place (its RESET queued) is
        # closed to the peer.
        s = self._streams.get(sid)
        if s is not None and not s.freed:
            self._handle_stream(s, typ, flags, p)
        else:
            self._handle_new(sid, typ, flags, p)

    def _check(self, sid: int, typ: FrameType, flags: int, p: bytes) -> None:
        bad = ResetCode.BAD_FRAME
        if typ is FrameType.CALL and self.role is Role.SDK:
            raise ProtocolViolation(bad, f"CALL {sid} from the core")
        if typ is FrameType.EVENT and self.role is Role.CORE:
            raise ProtocolViolation(bad, f"EVENT {sid} from the SDK")
        if sid == 0 and typ is not FrameType.EVENT and typ is not FrameType.RESET:
            raise ProtocolViolation(bad, f"{typ.name} on stream 0")
        if sid == 0 and flags:
            raise ProtocolViolation(bad, f"END on {typ.name} 0")
        if typ is not FrameType.WINDOW and typ is not FrameType.RESET:
            return
        if flags:
            raise ProtocolViolation(bad, f"END on {typ.name} {sid}")
        if len(p) != 4:
            raise ProtocolViolation(bad, f"{typ.name} {sid} with a {len(p)}-byte payload")
        inc = _u32.unpack(p)[0]
        if typ is FrameType.WINDOW and (inc == 0 or inc > _MAX_CREDIT):
            raise ProtocolViolation(bad, f"WINDOW {sid} of {inc}")

    def _handle_new(self, sid: int, typ: FrameType, flags: int, p: bytes) -> None:
        open_type, parity = self._peer_open_type()
        peers = sid % 2 == parity
        if typ is open_type:
            if not peers:
                raise ProtocolViolation(ResetCode.WRONG_PARITY, f"{typ.name} {sid}")
            if sid <= self._last_peer:
                raise ProtocolViolation(ResetCode.STREAM_CLOSED, f"{typ.name} {sid} reuses an id")
            if self._peer_open >= self.max_streams:
                raise ProtocolViolation(ResetCode.STREAM_LIMIT, f"{typ.name} {sid} with {self._peer_open} open")
            s = Stream(sid, False, self.window)
            s.payload = p
            s.recv_ended = bool(flags & _FLAG_END)
            self._last_peer = sid
            self._peer_open += 1
            self._streams[sid] = s
            self._accept_q.append(s)
            self._events.append(StreamAccepted())
            return
        opened = sid <= self._last_peer if peers else sid < self._next_id
        if not opened:
            raise ProtocolViolation(ResetCode.STREAM_NOT_OPEN, f"{typ.name} {sid}")
        if typ is FrameType.WINDOW or typ is FrameType.RESET:
            return
        raise ProtocolViolation(ResetCode.STREAM_CLOSED, f"{typ.name} {sid}")

    def _handle_stream(self, s: Stream, typ: FrameType, flags: int, p: bytes) -> None:
        end = bool(flags & _FLAG_END)
        opening = typ is FrameType.CALL or typ is FrameType.EVENT
        if s.reset_sent and not opening:
            # This side reset it: the peer's frames were in flight until its
            # END or RESET says it stopped.
            if typ is FrameType.RESET or end:
                s.recv_ended = True
                self._free(s)
            return
        if opening:
            # _check let only the peer's opening type through: it opens an
            # id already open, the peer's or this side's.
            _, parity = self._peer_open_type()
            if s.id % 2 != parity:
                raise ProtocolViolation(ResetCode.WRONG_PARITY, f"{typ.name} {s.id}")
            raise ProtocolViolation(ResetCode.STREAM_CLOSED, f"{typ.name} {s.id} reuses an id")
        if typ is FrameType.DATA:
            if s.recv_ended:
                raise ProtocolViolation(ResetCode.STREAM_CLOSED, f"DATA {s.id} after END")
            if s.local and not s.got_reply:
                raise ProtocolViolation(ResetCode.BAD_FRAME, f"DATA {s.id} before REPLY")
            s.recv_ended = end
            if len(p) > s.recv_credit:
                self._reset(
                    s,
                    ResetCode.CREDIT_OVERRUN,
                    ProtocolViolation(
                        ResetCode.CREDIT_OVERRUN,
                        f"{len(p)} bytes on stream {s.id} with {s.recv_credit} of credit",
                    ),
                )
                return
            s.recv_credit -= len(p)
            if p:
                s.recv.append(p)
                s.buffered += len(p)
            self._changed(s)
            self._maybe_free(s)
        elif typ is FrameType.REPLY:
            if s.recv_ended:
                raise ProtocolViolation(ResetCode.STREAM_CLOSED, f"REPLY {s.id} after END")
            if not s.local:
                raise ProtocolViolation(ResetCode.BAD_FRAME, f"REPLY {s.id} from the stream's opener")
            if s.got_reply:
                raise ProtocolViolation(ResetCode.BAD_FRAME, f"a second REPLY on {s.id}")
            s.got_reply = True
            s.reply = p
            s.recv_ended = end
            self._changed(s)
            self._maybe_free(s)
        elif typ is FrameType.WINDOW:
            if s.send_ended:
                return
            inc = _u32.unpack(p)[0]
            if s.send_credit > _MAX_CREDIT - inc:
                raise ProtocolViolation(ResetCode.BAD_FRAME, f"WINDOW {s.id} takes the credit past 2^31-1")
            s.send_credit += inc
            self._changed(s)
        else:  # RESET
            s.err = RemoteReset(s.id, _u32.unpack(p)[0])
            s.recv_ended = True
            # Answer, so the peer knows this side stopped too, unless an END
            # already said so.
            if not s.send_ended:
                s.send_ended = True
                s.reset_queued = True
                self._wq.append(_Out(s, FrameType.RESET, _reset_frame(s.id, ResetCode.CANCEL)))
            self._remove_accept(s)
            self._free(s)

    def _reset(self, s: Stream, code: int, err: WireError) -> None:
        """Reset ``s`` from this side, failing it with ``err``. It stays until
        the peer's END or RESET shows the peer stopped sending."""
        if s.freed or s.reset_sent:
            return
        s.err = err
        s.reset_sent = True
        s.send_ended = True
        s.reset_queued = True
        self._wq.append(_Out(s, FrameType.RESET, _reset_frame(s.id, code)))
        self._remove_accept(s)
        self._changed(s)
        if s.recv_ended:
            self._free(s)

    def _maybe_free(self, s: Stream) -> None:
        if s.send_ended and s.recv_ended and not s.reset_sent:
            self._free(s)

    def _free(self, s: Stream) -> None:
        """End ``s``: both sides stopped sending on it. Its caller may still
        read what it buffered, unless it failed. Its place in max_streams is
        released now, or once its RESET is written."""
        if s.freed:
            return
        s.freed = True
        if s.err is not None:
            s.recv.clear()
            s.buffered = 0
        self._changed(s)
        if not s.reset_queued:
            self._release(s)

    def _release(self, s: Stream) -> None:
        if s.released:
            return
        s.released = True
        del self._streams[s.id]
        if s.local:
            self._local_open -= 1
        else:
            self._peer_open -= 1

    def _remove_accept(self, s: Stream) -> None:
        """Drop a stream the peer opened that was never accepted."""
        try:
            self._accept_q.remove(s)
        except ValueError:
            return
        s.payload = b""

    def _take(self, s: Stream, max_bytes: int) -> bytes:
        recv = s.recv
        if max_bytes < 0:
            out = recv.popleft()
        elif max_bytes >= s.buffered:
            out = recv[0] if len(recv) == 1 else b"".join(recv)
            recv.clear()
        else:
            parts: list[bytes] = []
            n = 0
            while n < max_bytes:
                c = recv[0]
                k = max_bytes - n
                if len(c) <= k:
                    parts.append(recv.popleft())
                    n += len(c)
                else:
                    parts.append(c[:k])
                    recv[0] = c[k:]
                    n += k
            out = parts[0] if len(parts) == 1 else b"".join(parts)
        n = len(out)
        s.buffered -= n
        s.unreturned += n
        if s.recv_ended or s.unreturned < (self.window + 1) // 2:
            return out
        s.recv_credit += s.unreturned
        s.pending_window += s.unreturned
        s.unreturned = 0
        if not s.window_queued:
            s.window_queued = True
            self._wq.append(_Out(s, FrameType.WINDOW, b""))
        return out

    def _prepare(self, f: _Out) -> bytes | None:
        """The bytes to write for ``f``, None when it is not to be written:
        DATA and REPLY of a stream this side reset (its RESET follows), a
        WINDOW once its direction ended."""
        s = f.s
        if s is None or f.open:
            return f.buf
        if f.typ is FrameType.RESET:
            # A stream holds its place in max_streams until its RESET is
            # written, so a peer that stops reading cannot make RESETs pile
            # up: at most one per stream it may have open.
            s.reset_queued = False
            if s.freed:
                self._release(s)
            return f.buf
        if f.typ is FrameType.WINDOW:
            s.window_queued = False
            inc = s.pending_window
            s.pending_window = 0
            if inc == 0 or s.reset_sent or s.recv_ended or s.freed:
                return None
            return frame(s.id, FrameType.WINDOW, False, _u32.pack(inc))
        if s.reset_sent:
            return None
        return f.buf

    def _fail(self, err: ConnectionClosed, abort: bytes | None) -> None:
        """End the connection with ``err``: every stream fails, queued frames
        are dropped; ``abort`` (a RESET of stream 0) is all left to write.
        Only the first failure counts."""
        if self.error is not None:
            return
        self.error = err
        self._abort = abort
        for s in self._streams.values():
            if s.err is None:
                s.err = err
            s.freed = True
            s.released = True
            s.recv.clear()
            s.buffered = 0
            self._changed(s)
        self._streams.clear()
        self._accept_q.clear()
        self._wq.clear()
        self._local_open = 0
        self._peer_open = 0
        self._partial = bytearray()
        self._events.append(ConnectionFailed(err))


def _check_payload(payload: bytes) -> None:
    if len(payload) > MAX_PAYLOAD:
        raise ValueError(f"wire: a {len(payload)}-byte payload is larger than a frame")


def _send_check(s: Stream) -> None:
    if s.err is not None:
        raise s.err
    if s.send_ended:
        raise RuntimeError("wire: write after END")
    if not s.local and not s.reply_sent:
        raise RuntimeError("wire: DATA before the REPLY")


def _check_header(sid: int, typ: int, flags: int, length: int) -> None:
    """A header's checks, in order, before its payload is read."""
    if typ < FrameType.CALL or typ > FrameType.EVENT:
        raise ProtocolViolation(ResetCode.UNKNOWN_TYPE, f"unknown frame type {typ} on stream {sid}")
    if flags & ~_FLAG_END:
        raise ProtocolViolation(ResetCode.UNKNOWN_FLAGS, f"unknown flags {flags:#04x} on stream {sid}")
    if length > MAX_PAYLOAD:
        raise ProtocolViolation(ResetCode.FRAME_TOO_LARGE, f"a {length}-byte frame on stream {sid}")
