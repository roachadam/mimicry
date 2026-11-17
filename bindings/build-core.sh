#!/usr/bin/env bash
# Builds mimicry-core for every platform the SDKs ship (decision 0069) into
# the directory given, as mimicry-core-<goos>-<goarch>[.exe], with
# SHA256SUMS beside them. The release builds the binaries once with this,
# and the Node packages (scripts/pack.ts --core) and the Python wheels
# (build-wheels.sh, MIMICRY_CORE_DIR) carry those same files.
#
#   bindings/build-core.sh <dir> [<version>]   version as v1.2.3 (default: none)
#
# The build is static (CGO_ENABLED=0) and reproducible: -trimpath, no
# cgo, the module cache's sources only, so the same commit and Go build the
# same bytes (the release builds twice and compares).
set -euo pipefail

root=$(git rev-parse --show-toplevel)
mkdir -p "${1:?usage: build-core.sh <dir> [<version>]}"
out=$(cd "$1" && pwd)
version=${2:-}

ldflags="-s -w"
[ -n "$version" ] && ldflags="$ldflags -X main.version=$version"

targets=(
	"darwin amd64"
	"darwin arm64"
	"linux amd64"
	"linux arm64"
	"windows amd64"
	"windows arm64"
)

for t in "${targets[@]}"; do
	read -r goos goarch <<<"$t"
	exe="mimicry-core-$goos-$goarch"
	[ "$goos" = windows ] && exe="$exe.exe"
	(cd "$root" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags "$ldflags" -o "$out/$exe" ./cmd/mimicry-core)
done

sum=(sha256sum)
command -v sha256sum >/dev/null || sum=(shasum -a 256)
(cd "$out" && "${sum[@]}" mimicry-core-* >SHA256SUMS)
cat "$out/SHA256SUMS"
