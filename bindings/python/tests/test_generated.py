"""The generated messages (src/mimicry/_pb) are the schema's: their descriptor
equals the one in the Go code generated from proto/ (internal/rpc/pb), which
TestGenerated there checks against the schema. A schema edited without
regenerating both fails one or the other; regenerate with
``moon run python:proto``."""

from __future__ import annotations

import re
from pathlib import Path

from google.protobuf import descriptor_pb2

from mimicry._pb.mimicry.v1 import mimicry_pb2

GO = Path(__file__).resolve().parents[3] / "internal" / "rpc" / "pb" / "mimicry.pb.go"

_ESCAPES = {"n": 10, "t": 9, "r": 13, '"': 34, "\\": 92, "'": 39, "a": 7, "b": 8, "f": 12, "v": 11}


def _go_string(literal: str) -> bytes:
    """The bytes of a Go interpreted string literal's body."""
    out = bytearray()
    i = 0
    while i < len(literal):
        c = literal[i]
        if c != "\\":
            out += c.encode()
            i += 1
            continue
        e = literal[i + 1]
        if e == "x":
            out.append(int(literal[i + 2 : i + 4], 16))
            i += 4
        elif e in "01234567":
            out.append(int(literal[i + 1 : i + 4], 8))
            i += 4
        elif e == "u":
            out += chr(int(literal[i + 2 : i + 6], 16)).encode()
            i += 6
        else:
            out.append(_ESCAPES[e])
            i += 2
    return bytes(out)


def _go_descriptor() -> bytes:
    src = GO.read_text(encoding="utf-8")
    m = re.search(r"const file_mimicry_v1_mimicry_proto_rawDesc = \"\" \+\n(.*?)\n\n", src, re.S)
    assert m, "no raw descriptor in mimicry.pb.go"
    parts = re.findall(r'^\s*"((?:[^"\\]|\\.)*)"(?: \+)?$', m.group(1), re.M)
    return b"".join(_go_string(p) for p in parts)


def _normalised(data: bytes) -> descriptor_pb2.FileDescriptorProto:
    fd = descriptor_pb2.FileDescriptorProto.FromString(data)
    # protoc-gen-go keeps the JSON names protoc computes; Python's code
    # leaves them out.
    for msg in fd.message_type:
        _clear_json(msg)
    return fd


def _clear_json(msg: descriptor_pb2.DescriptorProto) -> None:
    for f in msg.field:
        f.ClearField("json_name")
    for nested in msg.nested_type:
        _clear_json(nested)


def test_generated_matches_the_schema() -> None:
    py = _normalised(mimicry_pb2.DESCRIPTOR.serialized_pb)
    go = _normalised(_go_descriptor())
    assert py.name == "mimicry/v1/mimicry.proto"
    assert py == go, "src/mimicry/_pb is stale: run moon run python:proto"
