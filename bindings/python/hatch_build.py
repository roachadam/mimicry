"""Tags each wheel for the platform of the mimicry-core it carries.

The wheel holds no Python extension, only the core (a static Go binary), so
its tag is ``py3-none-<platform>``. ``MIMICRY_WHEEL_PLATFORM`` names the
platform (CI builds every platform's wheel on one runner, with the core
cross-compiled into ``src/mimicry/_bin``); unset, it is this machine's.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

from hatchling.builders.hooks.plugin.interface import BuildHookInterface


class CoreBuildHook(BuildHookInterface):  # type: ignore[type-arg]
    PLUGIN_NAME = "custom"

    def initialize(self, version: str, build_data: dict[str, Any]) -> None:
        if self.target_name != "wheel" or version == "editable":
            return
        platform = os.environ.get("MIMICRY_WHEEL_PLATFORM")
        if not platform:
            from packaging.tags import sys_tags

            platform = next(iter(sys_tags())).platform
        exe = "mimicry-core.exe" if platform.startswith("win") else "mimicry-core"
        bin_dir = Path(self.root) / "src" / "mimicry" / "_bin"
        found = sorted(p.name for p in bin_dir.glob("*")) if bin_dir.is_dir() else []
        if found != [exe]:
            raise RuntimeError(
                f"the wheel for {platform} needs exactly src/mimicry/_bin/{exe}, found {found}: "
                f"build it with GOOS/GOARCH for {platform}: "
                f"CGO_ENABLED=0 go build -trimpath -o bindings/python/src/mimicry/_bin/{exe} ./cmd/mimicry-core"
            )
        build_data["pure_python"] = False
        build_data["tag"] = f"py3-none-{platform}"
