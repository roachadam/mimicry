#!/usr/bin/env bash
# Builds the Python SDK's wheels into the directory given (default dist),
# one per platform, each carrying mimicry-core cross-compiled for it
# (CGO_ENABLED=0, static: one Linux build serves the manylinux and the
# musllinux wheel). Run from anywhere in the repository; needs Go and uv.
# Nothing is published here: the release workflow uploads the wheels
# (release.yml).
#
# MIMICRY_CORE_DIR: take the cores bindings/build-core.sh built there
# (mimicry-core-<goos>-<goarch>[.exe]) instead of building them.
# MIMICRY_VERSION: the wheels' version (PEP 440); default _version.py's.
#
# The macOS tags are 13.0: the minimum the pinned Go's linker writes into
# the binary (otool -l: LC_BUILD_VERSION minos).
set -euo pipefail

root=$(git rev-parse --show-toplevel)
mkdir -p "${1:-dist}"
out=$(cd "${1:-dist}" && pwd)
pkg="$root/bindings/python"
bin="$pkg/src/mimicry/_bin"

targets=(
	"linux amd64 manylinux_2_17_x86_64.manylinux2014_x86_64"
	"linux amd64 musllinux_1_1_x86_64"
	"linux arm64 manylinux_2_17_aarch64.manylinux2014_aarch64"
	"linux arm64 musllinux_1_1_aarch64"
	"darwin amd64 macosx_13_0_x86_64"
	"darwin arm64 macosx_13_0_arm64"
	"windows amd64 win_amd64"
	"windows arm64 win_arm64"
)

# The package's own build of the core (python:core) and its _version.py
# are put back after.
version_py="$pkg/src/mimicry/_version.py"
saved=$(mktemp -d)
cp "$version_py" "$saved/_version.py"
trap 'cp "$saved/_version.py" "$version_py"; rm -rf "$bin"; if [ -d "$saved/_bin" ]; then mv "$saved/_bin" "$bin"; fi; rm -rf "$saved"' EXIT
if [ -d "$bin" ]; then mv "$bin" "$saved/_bin"; fi
if [ -n "${MIMICRY_VERSION:-}" ]; then
	printf '__version__ = "%s"\n' "$MIMICRY_VERSION" >"$version_py"
fi

for t in "${targets[@]}"; do
	read -r goos goarch platform <<<"$t"
	exe=mimicry-core
	[ "$goos" = windows ] && exe=mimicry-core.exe
	rm -rf "$bin" && mkdir -p "$bin"
	if [ -n "${MIMICRY_CORE_DIR:-}" ]; then
		cp "$MIMICRY_CORE_DIR/mimicry-core-$goos-$goarch${exe#mimicry-core}" "$bin/$exe"
		chmod 755 "$bin/$exe"
	else
		(cd "$root" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -o "$bin/$exe" ./cmd/mimicry-core)
	fi
	MIMICRY_WHEEL_PLATFORM=$platform uv build --wheel --quiet --build-constraints "$pkg/build-constraints.txt" --require-hashes --out-dir "$out" "$pkg"
done

ls -l "$out"
