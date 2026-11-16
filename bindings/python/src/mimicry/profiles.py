"""The newest built-in profile of each browser on each platform, as Go's
``profiles`` accessors return them (``profiles.ChromeMacOS()`` is
``chrome_macos()``), for ``Config.profile``.

Each is resolved against the core's list of supported profiles when a client
is made, so it is the newest profile the installed core has. To pin a
version, name it instead: ``Config(profile="chrome-157-macos")``; the list is
:func:`mimicry.list_profiles`.
"""

from __future__ import annotations

from ._types import Newest

__all__ = [
    "Newest",
    "brave_linux",
    "brave_macos",
    "brave_windows",
    "chrome_ios",
    "chrome_linux",
    "chrome_macos",
    "chrome_windows",
    "edge_macos",
    "edge_windows",
    "firefox_ios",
    "firefox_linux",
    "firefox_macos",
    "firefox_windows",
    "safari_ios",
    "safari_macos",
]


def brave_macos() -> Newest:
    return Newest("brave", "macos")


def brave_linux() -> Newest:
    return Newest("brave", "linux")


def brave_windows() -> Newest:
    return Newest("brave", "windows")


def chrome_macos() -> Newest:
    return Newest("chrome", "macos")


def chrome_ios() -> Newest:
    return Newest("chrome", "ios")


def chrome_linux() -> Newest:
    return Newest("chrome", "linux")


def chrome_windows() -> Newest:
    return Newest("chrome", "windows")


def edge_macos() -> Newest:
    return Newest("edge", "macos")


def edge_windows() -> Newest:
    return Newest("edge", "windows")


def firefox_macos() -> Newest:
    return Newest("firefox", "macos")


def firefox_ios() -> Newest:
    return Newest("firefox", "ios")


def firefox_linux() -> Newest:
    return Newest("firefox", "linux")


def firefox_windows() -> Newest:
    return Newest("firefox", "windows")


def safari_macos() -> Newest:
    return Newest("safari", "macos")


def safari_ios() -> Newest:
    return Newest("safari", "ios")
