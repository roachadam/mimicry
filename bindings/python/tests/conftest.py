from __future__ import annotations

from collections.abc import Iterator
from typing import Any

import pytest

import mimicry
from mimicry import Config, profiles

from .server import CA, Server


@pytest.fixture(scope="session")
def server() -> Iterator[Server]:
    s = Server()
    yield s
    s.close()


def config(**kw: Any) -> Config:
    """A Chrome (macOS) config trusting the test server's CA."""
    kw.setdefault("profile", profiles.chrome_macos())
    return Config(root_cas=CA, **kw)


@pytest.fixture
def client(server: Server) -> Iterator[mimicry.Client]:
    with mimicry.new_client(config()) as c:
        yield c
