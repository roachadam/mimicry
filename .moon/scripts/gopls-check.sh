#!/usr/bin/env bash
# Runs gopls's analyzers (the diagnostics an editor shows, hints included)
# over the current module's Go files and fails if any are reported.
# internal/quic is upstream's copy (decision 0027) and is skipped, as in
# .golangci.yml. gopls is pinned and built with the pinned Go.
set -euo pipefail

files=$(go list -f '{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}} {{end}}{{range .TestGoFiles}}{{$d}}/{{.}} {{end}}{{range .XTestGoFiles}}{{$d}}/{{.}} {{end}}' ./... | tr ' ' '\n' | grep -v '/internal/quic/' | grep . || true)
[ -z "$files" ] && exit 0

gopls=golang.org/x/tools/gopls@v0.24.0
# Build (and, on a cold module cache, download) gopls first: go run's
# "go: downloading" lines go to stderr, which the check below captures, so
# they would otherwise be counted as diagnostics.
go run "$gopls" version >/dev/null

# shellcheck disable=SC2086 # one argument per file
out=$(go run "$gopls" check -severity=hint $files 2>&1)
if [ -n "$out" ]; then
	echo "$out"
	echo "gopls check: $(echo "$out" | wc -l | tr -d ' ') diagnostics" >&2
	exit 1
fi
