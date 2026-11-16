#!/usr/bin/env bash
# Builds the Python SDK's wheels into the directory given (default dist),
# one per platform, each carrying mimicry-core cross-compiled for it
# (CGO_ENABLED=0, static: one Linux build serves the manylinux and the
# musllinux wheel). Run from anywhere in the repository; needs Go and uv.
# The wheels are not published (decision 0069: a release pipeline, later).
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

# The package's own build of the core (python:core) is put back after.
saved=$(mktemp -d)
trap 'rm -rf "$bin"; [ -d "$saved/_bin" ] && mv "$saved/_bin" "$bin"; rm -rf "$saved"' EXIT
[ -d "$bin" ] && mv "$bin" "$saved/_bin"

for t in "${targets[@]}"; do
	read -r goos goarch platform <<<"$t"
	exe=mimicry-core
	[ "$goos" = windows ] && exe=mimicry-core.exe
	rm -rf "$bin" && mkdir -p "$bin"
	(cd "$root" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -o "$bin/$exe" ./cmd/mimicry-core)
	MIMICRY_WHEEL_PLATFORM=$platform uv build --wheel --quiet --out-dir "$out" "$pkg"
done

ls -l "$out"
