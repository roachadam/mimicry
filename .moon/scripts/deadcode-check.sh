#!/usr/bin/env bash
# Runs deadcode (functions no test or main reaches) over the current module
# and fails if it reports any in an internal/ package. Outside internal/,
# unreached functions are the root's public API or cmd entry points, which
# users call. internal/quic is upstream's copy (decision 0027): its unused
# test helpers are upstream's, skipped as in .golangci.yml. deadcode is
# pinned and built with the pinned Go. It loads the host's GOOS only, so
# code built for another OS (the harness's windows files) is never reported.
set -euo pipefail

out=$(go run golang.org/x/tools/cmd/deadcode@v0.52.0 -test ./...)
dead=$(echo "$out" | grep '^internal/' | grep -v '^internal/quic/' || true)
if [ -n "$dead" ]; then
	echo "$dead"
	echo "deadcode: $(echo "$dead" | wc -l | tr -d ' ') unreachable functions in internal/ packages" >&2
	exit 1
fi
