# 0027: QUIC from a copy of x/net/quic; HTTP/3 and QPACK written for mimicry

- Status: Accepted; amended 2026-10-27 (fix the copy, don't wait for upstream)
- Date: 2026-10-25
- Commits: `f98f5e9` (adds `internal/quic`), `acfc4f6`

**Summary.** mimicry's QUIC is a copy of `golang.org/x/net/quic` in
`internal/quic`, using the uTLS fork and changed to build packets per profile,
while HTTP/3 and QPACK are mimicry's own, as the maintainer's decision. No Go
QUIC library exposes the packet-building controls, and x/net/quic is smaller
and changes far less than quic-go. It is internal with no public API; since
the 2026-10-27 amendment the copy is fixed locally rather than waiting for
upstream, and every local change is recorded in `internal/quic/UPSTREAM.md`.

## Problem

HTTP/3 runs on QUIC. The HTTP/3 captures
([report](../../internal/harness/reports/http3-2026-10-25.md)) show the QUIC
fingerprint is mostly in how packets are built:
- datagram sizes and where padding goes;
- how the ClientHello is split across CRYPTO frames and packets (Chrome
  shuffles the pieces and interleaves PING and PADDING; Firefox cuts
  inside the SNI);
- packet numbers and connection IDs;
- ACK delays;
- transport parameters (order, GREASE);
- HTTP/3 SETTINGS and QPACK use.

No Go QUIC library lets a caller control these, so mimicry has to change
a stack's internals whichever one it uses.

## Options

The numbers are from the module cache, 2026-10-25.

- **Copy `golang.org/x/net/quic` into `internal/quic`.**
  - Official, from the Go team; about 13,000 lines plus 17,000 of tests.
  - Low churn: about 1,900 changed lines from April to October 2026.
  - Same authors and style as the x/net HTTP/2 that mimicry's design follows.
  - Its doc says it is a work in progress, not ready for production, and
    it has NewReno congestion control only.
  - x/net's HTTP/3 was a 61-line stub (v0.59) and is gone in v0.61.
- **A true fork of `quic-go`** (a separate repo, like the uTLS fork).
  - Mature and widely deployed, with Cubic and HTTP/3 included.
  - About 37,000 lines plus 6,000 of HTTP/3, and high churn: about 17,800
    changed lines from March 2025 to October 2026. Every sync would be a
    real merge against mimicry's packet-building changes.
- **mimicry's own QUIC.** Total control, but loss recovery and congestion
  control (RFC 9002) would be mimicry's to get right from nothing.
- Using either library unmodified, as a dependency, is not possible:
  neither exposes the controls.

## Decision

Copy `x/net/quic` at x/net v0.61.0 (commit `c1d31df`) into
`internal/quic`:
- keep its LICENSE, and record the upstream commit in the package;
- swap its `crypto/tls` QUIC API for the uTLS fork's `UQUICConn`, which
  mirrors it;
- then change packet construction per profile.

Upstream fixes are taken by diffing upstream between commits and applying
what is relevant. HTTP/3 and QPACK are written in mimicry, as HTTP/2 and
HPACK are. (The maintainer's decision, 2026-10-25.)

## Consequences

- mimicry owns about 13,000 lines that must be understood, and their maturity is
  mimicry's to prove. That means interop tests against real QUIC servers
  (Google, Cloudflare, nginx, quic-go) on top of the browser captures;
  upstream ships an interop program (`internal/quic/cmd/interop`).
- Congestion control starts as NewReno. What each browser uses (Cubic,
  BBR) is timing, to be measured before it is modelled.
- No public API is involved: `internal/`.

## Revisit when

- Interop shows the copy cannot be made robust at reasonable cost; then
  reconsider a quic-go fork.
- x/net/quic becomes production-ready, or HTTP/3 reappears upstream.

## Amendment 2026-10-27: fix the copy, don't wait for upstream

Diverging from x/net/quic is accepted (the maintainer's decision). A problem
found in the copy (a bug, or a cost that shows in the benchmarks) is
fixed here now rather than waiting for an upstream fix; upstream's
changes are still reviewed periodically and taken where relevant. Every
local change keeps its entry in `internal/quic/UPSTREAM.md` and its
`mimicry:` marker, so a sync can see what is mimicry's. First under this
rule: the streams table under a mutex, so opening a stream no longer
waits for the connection's loop (UPSTREAM.md change 12), which
serialized HTTP/3 request starts on a busy connection.
