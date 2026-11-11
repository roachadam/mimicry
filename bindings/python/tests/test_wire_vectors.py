"""The wire protocol's language-neutral vectors (proto/testdata/wire/), replayed
against the sans-IO connection as their README says, for both roles."""

from __future__ import annotations

import json
import struct
from pathlib import Path
from typing import Any

import pytest

from mimicry import _wire as wire

VECTORS = Path(__file__).resolve().parents[3] / "proto" / "testdata" / "wire"


def _manifest() -> dict[str, Any]:
    with open(VECTORS / "manifest.json", encoding="utf-8") as f:
        m: dict[str, Any] = json.load(f)
        return m


MANIFEST = _manifest()


def err_name(err: BaseException | None) -> tuple[str, bool]:
    """The vector's name for an error, and whether the peer sent it."""
    if isinstance(err, wire.ConnectionClosed):
        cause = err.cause
        if cause is None:
            return "CONN_CLOSED", False
        if isinstance(cause, wire.Truncated):
            return "TRUNCATED", False
        return err_name(cause)
    if isinstance(err, wire.RemoteReset):
        return wire.code_name(err.code), True
    if isinstance(err, wire.ProtocolViolation):
        return err.code.name, False
    if isinstance(err, wire.StreamCanceled):
        return "CANCEL", False
    return "", False


class Replayer:
    def __init__(self, role: wire.Role) -> None:
        self.c = wire.Connection(role, MANIFEST["window"], MANIFEST["maxStreams"])
        self.streams: dict[int, wire.Stream] = {}
        self.notices: list[bytes] = []
        self.out = bytearray()

    def pump(self, events: list[wire.Event] | None = None) -> None:
        for e in events or []:
            if isinstance(e, wire.Notice):
                self.notices.append(e.payload)
        self.out += self.c.data_to_send()

    def take(self, n: int) -> bytes:
        self.pump()
        got = bytes(self.out[:n])
        del self.out[:n]
        return got

    def stream(self, f: dict[str, Any]) -> wire.Stream:
        s = self.streams.get(f["stream"])
        assert s is not None, f"no stream {f['stream']}"
        return s

    def act(self, f: dict[str, Any]) -> None:
        payload = bytes.fromhex(f.get("payload", ""))
        typ, sid, end = f["type"], f["stream"], f.get("end", False)
        if typ in ("CALL", "EVENT") and sid != 0:
            s = self.c.open_stream(payload, end)
            assert s.id == sid, f"opened stream {s.id}, want {sid}"
            self.streams[sid] = s
        elif typ == "EVENT":
            self.c.notify(payload)
        elif typ == "REPLY":
            self.c.send_reply(self.stream(f), payload, end)
        elif typ == "DATA":
            s = self.stream(f)
            if payload:
                assert self.c.send_data(s, payload) == len(payload)
            if end:
                self.c.end_stream(s)
        elif typ == "RESET":
            (code,) = struct.unpack(">I", payload)
            self.c.reset_stream(self.stream(f), code)
        else:
            raise AssertionError(f"no action sends {typ}")
        self.pump()

    def observe(self, f: dict[str, Any]) -> None:
        expect = f.get("expect")
        if expect == "ignore":
            return
        if expect == "stream-error":
            with pytest.raises(wire.WireError) as e:
                self.c.read(self.stream(f), 1)
            assert err_name(e.value) == (f["error"], False)
            return
        assert expect is None, expect
        payload = bytes.fromhex(f.get("payload", ""))
        typ, sid = f["type"], f["stream"]
        if typ in ("CALL", "EVENT"):
            if sid == 0:
                assert self.notices.pop(0) == payload
                return
            s = self.c.accept()
            assert s is not None, "no stream to accept"
            assert (s.id, s.payload) == (sid, payload)
            self.streams[sid] = s
        elif typ == "REPLY":
            assert self.c.reply(self.stream(f)) == payload
        elif typ == "DATA":
            s = self.stream(f)
            got = b""
            while len(got) < len(payload):
                chunk = self.c.read(s, len(payload) - len(got))
                assert chunk, f"read {got.hex()}, then {chunk!r}; want {payload.hex()}"
                got += chunk
                self.pump()
            assert got == payload
        elif typ == "RESET":
            with pytest.raises(wire.RemoteReset) as e:
                self.c.read(self.stream(f), 1)
            assert e.value.code == struct.unpack(">I", payload)[0]
            return
        elif typ == "WINDOW":
            return
        if f.get("end"):
            assert self.c.read(self.stream(f), 1) == b""


@pytest.mark.parametrize("v", MANIFEST["vectors"], ids=[v["name"] for v in MANIFEST["vectors"]])
def test_vector(v: dict[str, Any]) -> None:
    data = (VECTORS / v["file"]).read_bytes()
    r = Replayer(wire.Role.SDK if v["role"] == "sdk" else wire.Role.CORE)
    want = ("CONN_CLOSED", False)
    off = 0
    for i, f in enumerate(v["frames"]):
        b = data[off : off + f["size"]]
        off += f["size"]
        if f["dir"] == "out":
            if not f.get("auto"):
                r.act(f)
            got = r.take(len(b))
            assert got == b, f"frame {i}: the endpoint sent {got.hex()}, want {b.hex()}"
            continue
        r.pump(r.c.receive_data(b))
        if f.get("expect") == "conn-error":
            want = (f["error"], f.get("remote", False))
            r.pump(r.c.receive_eof())
            continue
        r.observe(f)
        r.pump()
    assert off == len(data)
    r.pump(r.c.receive_eof())
    assert err_name(r.c.error) == want
    assert not r.out, f"the endpoint sent {bytes(r.out).hex()} after the last frame"


def test_every_vector_file_is_listed() -> None:
    listed = {v["file"] for v in MANIFEST["vectors"]}
    on_disk = {p.name for p in VECTORS.glob("*.bin")}
    assert listed == on_disk


def test_split_reads() -> None:
    """Frames cut at every byte boundary parse as when whole."""
    stream = wire.frame(1, wire.FrameType.REPLY, False, b"head") + wire.frame(1, wire.FrameType.DATA, True, b"x" * 100)
    for cut in range(1, len(stream)):
        c = wire.Connection(wire.Role.SDK)
        s = c.open_stream(b"call", True)
        c.data_to_send()
        c.receive_data(stream[:cut])
        c.receive_data(stream[cut:])
        assert c.reply(s) == b"head"
        assert c.read(s) == b"x" * 100
        assert c.read(s) == b""
