# 0001: Own HTTP/1.1 and HTTP/2 stack; uTLS as a thin fork

- Status: Accepted; amended 2026-10-25 by [0027](0027-quic-base.md) (`internal/quic` imports uTLS too)
- Date: 2026-10-23 (HTTP/2 re-decided after reviewing sardanioss/net)

## Problem

A browser's fingerprint lives in three layers: the TLS ClientHello, the
HTTP/2 connection (SETTINGS, window updates, priorities, HPACK encoding,
frame sizes, stream IDs) and the HTTP/1.1 head. We need full control of
each, and something we can keep up to date.

## Options

- **Fork everything** (tls-client's fhttp, httpcloak's forked x/net and
  net/http). Total control; sardanioss's x/net fork rewrote about a third of
  the client (`transport.go` +847/−111, 1,730 lines over 20 files) and needed
  a forked `net/http` just for header order. Every upstream release is a
  merge.
- **BoringSSL or curl-impersonate via cgo.** Native TLS for Chromium only;
  HTTP/2 and HTTP/3 are imitations in every approach anyway, and custom
  profiles are impossible.
- **Thin uTLS fork + our own HTTP layers on x/net's exported pieces.**

## Decision

- TLS: uTLS, forked thinly as `github.com/roachadam/utls` (a public library
  cannot use `replace`, so the fork needs its own module path). Only
  `internal/handshake` imports it (and, since 0027, `internal/quic`; see
  the amendment). The fork carries a short, tested patch
  list (`utls/PATCHES.md`), rebuilt weekly on upstream `master`.
- HTTP/2: ours (`internal/h2`) on `golang.org/x/net/http2`'s exported
  `Framer` and `hpack` decoder; our own HPACK encoder (`internal/hpack`),
  because upstream has no indexing-policy hook.
- HTTP/1.1: ours (`internal/h1`); responses parsed by `http.ReadResponse`.
- The fingerprint (profiles, extension order, GREASE, shuffle) is ours, as
  data; we never call uTLS's shuffle (not uniform: ~12.5% stay in place
  against ~6.7% expected over 20k runs).

## Evidence

The fingerprint runs through most of an HTTP/2 client, unlike TLS, where it
is a thin layer: sardanioss's commit log is a checklist of measured Chrome
behaviours (HPACK policy, END_STREAM linger, 16375-byte DATA cap, 5 s
window-update timer, preface PING, dependency chaining, table-size echo).
Each is a few lines in our own client and a deep patch in a fork.

## Consequences

We own correctness of HTTP/2 (flow control, GOAWAY, resets), which the
harness checks against browser traces. Upgrading x/net is cheap.

## Revisit when

x/net exposes hooks for header order and HPACK policy, or our HTTP/2 client
accumulates bugs a mature stack would not have.

## Amendment 2026-10-25: `internal/quic` imports uTLS

QUIC's handshake runs on the uTLS fork's QUIC API, so `internal/quic`, our
copy of `golang.org/x/net/quic`, imports the fork too
([0027](0027-quic-base.md)). No other non-test package does.
