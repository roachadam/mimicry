"""Exceptions: one class per code, every match caught (decision 0069)."""

from __future__ import annotations

import itertools
import pickle
from typing import Any

import mimicry
from mimicry import Code
from mimicry._errors import error_class, from_pb
from mimicry._pb.mimicry.v1 import mimicry_pb2 as pb


def test_each_code_has_its_class() -> None:
    for code in Code:
        c: Any = int(code)
        e = from_pb(pb.Error(code=c, message="m", matches=[c]))
        assert e.code is code and e.matches == {code} and str(e) == "m"
        assert type(e).__module__ == "mimicry"
        assert getattr(mimicry, type(e).__name__) is type(e)


def test_matches_are_caught_as_each() -> None:
    """A proxy failure at its deadline is a ProxyError, a DeadlineError and a
    TimeoutError; every pair of codes makes a consistent class."""
    e = from_pb(
        pb.Error(
            code=pb.CODE_PROXY,
            message="proxy timed out",
            matches=[pb.CODE_PROXY, pb.CODE_DEADLINE, pb.CODE_NET],
            proxy=pb.ProxyError(proxy="http://p:8080", status_code=0),
        )
    )
    assert isinstance(e, mimicry.ProxyError)
    assert isinstance(e, mimicry.DeadlineError) and isinstance(e, TimeoutError)
    assert isinstance(e, mimicry.NetError) and isinstance(e, ConnectionError)
    assert type(e).__name__ == "ProxyError" and e.proxy == "http://p:8080"
    assert e.matches == {Code.PROXY, Code.DEADLINE, Code.NET}
    for a, b in itertools.permutations(Code, 2):
        assert issubclass(error_class(a, [b]), (mimicry.MimicryError,))
    assert error_class(Code.PROXY, [Code.DEADLINE]) is error_class(Code.PROXY, [Code.DEADLINE])


def test_pickle() -> None:
    e = from_pb(
        pb.Error(
            code=pb.CODE_CLOSE,
            message="closed",
            matches=[pb.CODE_CLOSE, pb.CODE_NET],
            close=pb.CloseError(code=1001, reason="away"),
        )
    )
    back = pickle.loads(pickle.dumps(e))
    assert isinstance(back, mimicry.CloseError) and isinstance(back, mimicry.NetError)
    assert (back.close_code, back.reason, str(back)) == (1001, "away", "closed")
    x = pickle.loads(pickle.dumps(mimicry.CoreExited(-9, "boom")))
    assert (x.returncode, x.stderr) == (-9, "boom")
