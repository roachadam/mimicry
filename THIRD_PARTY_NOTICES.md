# Third-party notices

mimicry is licensed under the MIT licence ([`LICENSE`](LICENSE)); that covers
both modules in this repository, the library at the root and `internal/harness/`.
This file lists the code from other projects that mimicry bundles and the
Go modules it depends on.

## Bundled code

Code copied or ported into this repository. Each keeps its upstream notice
next to it.

| Path | Origin | Licence | Notice |
|---|---|---|---|
| `internal/quic/` (with `qlog/`, `quicwire/`, `testcert/`) | `golang.org/x/net` v0.61.0 (`quic/`, `quic/qlog/`, `internal/quic/quicwire/`, `internal/testcert/`), copied and modified; see [`UPSTREAM.md`](internal/quic/UPSTREAM.md) | BSD-3-Clause, Copyright 2009 The Go Authors, plus Google's patent grant | [`LICENSE`](internal/quic/LICENSE), [`PATENTS`](internal/quic/PATENTS); per-file headers kept |
| `internal/zdeflate/` | zlib 1.3.1 (`deflate.c`, `trees.c`), ported to Go and altered | zlib licence, (C) 1995-2026 Jean-loup Gailly and Mark Adler | [`LICENSE-zlib`](internal/zdeflate/LICENSE-zlib) |
| `internal/zdeflate/` | Chromium's zlib (`chromium/src/third_party/zlib`, zlib 1.3.2 with Chromium's changes: `insert_string.h`, `compare256.h`, `fill_window`, `longest_match`), ported to Go and altered | zlib licence for zlib; BSD-3-Clause, Copyright 2015 The Chromium Authors, for Chromium's changes | [`LICENSE-zlib`](internal/zdeflate/LICENSE-zlib), [`LICENSE-chromium`](internal/zdeflate/LICENSE-chromium) |
| `internal/zdeflate/` | zlib-rs 0.6.3 (`deflate_medium`, its hash, `longest_match`, `fill_window`, `compare256`), ported to Go and altered | zlib licence, (C) 2024 Trifecta Tech Foundation | [`LICENSE-zlib-rs`](internal/zdeflate/LICENSE-zlib-rs) |

`internal/zdeflate`'s package documentation marks it as an altered version
of all three, as the zlib licence requires.

Tables taken from specifications, not code: HPACK's static table
(`internal/hpack/static.go`, RFC 7541 appendix A) and QPACK's
(`internal/qpack/static.go`, RFC 9204 appendix A). These are facts of the
protocols, reproduced as data; the IETF Trust's licence for RFC text
permits it. Huffman coding for both comes from the
`golang.org/x/net/http2/hpack` dependency, not a copy.

## Go dependencies

Direct dependencies of the library (`go.mod`):

| Module | Version | Licence |
|---|---|---|
| `github.com/jonboulle/clockwork` | v0.5.0 | Apache-2.0 (tests only) |
| `github.com/klauspost/compress` | v1.20.1 | BSD-3-Clause (the `zstd` package used here; its `zstd/internal/xxhash` is MIT) |
| `github.com/molecule-man/go-brrr` | v1.2.0 | MIT |
| `github.com/roachadam/utls` | v0.0.0-20261025174743-24d1f83b2f8a | BSD-3-Clause (fork of `refraction-networking/utls`, itself from Go's `crypto/tls`) |
| `golang.org/x/crypto` | v0.58.0 | BSD-3-Clause |
| `golang.org/x/net` | v0.61.0 | BSD-3-Clause |
| `golang.org/x/sys` (indirect) | v0.49.0 | BSD-3-Clause |
| `golang.org/x/text` (indirect) | v0.43.0 | BSD-3-Clause |

The harness (`internal/harness/go.mod`) adds, for capture and interop only (never
imported by the library):

| Module | Version | Licence |
|---|---|---|
| `github.com/quic-go/quic-go` | v0.64.1 | MIT |
| `github.com/quic-go/qpack` (indirect) | v0.6.0 | MIT |

Other modules in either module graph (`go list -m all`), none of them
built into the library: `github.com/jordanlewis/gcassert` (MIT),
`github.com/quic-go/go-ossfuzz-seeds` (MIT), `github.com/stretchr/testify`
(MIT), `go.uber.org/mock` (Apache-2.0), `go.yaml.in/yaml/v3` (MIT and
Apache-2.0), `golang.org/x/mod`, `x/sync`, `x/term`, `x/tools`
(BSD-3-Clause). All are permissive.

## Tools not shipped

The harness's HTTP/3 version-negotiation server
(`internal/harness/internal/server/aioquic_h3.py`) runs on Python's `aioquic`
(BSD-3-Clause), installed separately at capture time. Nothing from it is in
this repository or in the library.
