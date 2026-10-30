# 0043: An opt-in lower HTTP/3 ACK rate (`Config.HTTP3ACKEvery`)

- Status: Accepted
- Date: 2026-10-29
- Commits: `4cca5f9`

## Problem

Against nginx, mimicry's HTTP/3 bulk downloads ran up to about 25% below
tls-client's (10 MB bodies, 64 at once, fast bench 2026-10-28: 97 against
120 requests/s). The cause was not obvious, and two readings pointed in
opposite directions. The Chrome profile acknowledges every 10th packet.
quic-go's tracker reads as acknowledging every 2nd (`packetsBeforeAck = 2`),
so quic-go should send five times as many ACKs and be the slower one, yet it
is faster. Before choosing anything we needed the real cause, and whether it
is a mimicry bug or Chrome's own behaviour.

## Evidence

Everything below was measured on the M3 Pro laptop under OrbStack (Docker
in a Linux VM), before [0045](0045-udp-receive-buffer-default.md): mimicry's
QUIC socket had the OS's default receive buffer.

Measured with a throwaway harness in the bench's client container against
the bench's nginx (one connection, 10 MB bodies, 64 at once, 4 s windows).
The client container's UDP counters give the datagrams each way. A raw
socket in the same network namespace saw the packets on the wire. mimicry's
ACK frames were logged from inside the connection. nginx's CPU comes from
its cgroup.

| client | server datagrams per client datagram | MB/s | nginx cores |
|---|---|---|---|
| mimicry, Chrome's rule (every 10) | 9.95 | 996 | 0.98 |
| mimicry, every 20 (build overlay) | 19.8 | 1,329 | 0.95 |
| mimicry, every 2 (build overlay) | 2.0 | 498 | 0.99 |
| tls-client (quic-go), this run | 9.2 | 959 | 0.95 |
| tls-client, other runs | 11.2–22.4 | 1,004–1,342 | 0.91–0.92 |

- **nginx's worker is the limit.** nginx serves a connection from one
  worker on one core, and that worker is saturated in every row, so
  throughput is one core divided by nginx's CPU per byte.
- **nginx's CPU follows the client's datagrams.** A linear fit over the
  rows gives about 4 µs of nginx CPU per client datagram, on a base of
  about 0.57 core-seconds per GB. At equal ratios the two clients cost
  nginx the same per byte (0.98 and 0.99 core-seconds per GB).
- **nginx is ACK-clocked.** Each ACK frees window for the packets it
  acknowledged, and nginx sends those in one GSO write. The client's
  interface received coalesced bursts of 14,720 bytes (10 × 1,472) at
  every 10, 29,440 (20 × 1,472) at every 20, and 46,464 (32 × 1,452) from
  tls-client.
- **Too many ACKs overflow nginx.** At every 2, nginx's own socket
  overflowed (+25,091 `RcvbufErrors`) and the client entered loss recovery
  530 times.
- **100 KB bodies behave the same.** At 64 at once: 7,908 req/s with
  Chrome's rule, 9,616 at every 20 (ratios 9.3 and 17.6), tls-client
  8,697. tls-client's nginx was at 0.71 cores there, so not the limit.
- **quic-go is batched on the wire.** `packetsBeforeAck = 2` only queues
  an ACK. The connection's run loop first handles up to
  `maxPacketsToProcess = 32` queued packets (`connection.go`), then sends.
  So it sends one ACK per batch, of varying size, and its speed varies with
  that batch between runs.
- **mimicry matches Chrome.**
  - Chrome's every 10 is captured: 97% of its h3-download ACKs advance by
    exactly 10 ([EVIDENCE.md](../../profiles/EVIDENCE.md), `quic.ack`).
  - mimicry acknowledges on the 10th packet (10.0 server packets per
    client datagram; 99.3% exact in `TestHTTP3DownloadMatchesBrowser`).
  - Its ACK Delay field is 0–8 µs, encoded with exponent 3, the default,
    since Chrome's transport parameters send no ack_delay_exponent, so it
    cannot inflate nginx's RTT estimate.
  - MAX_DATA leaves as soon as its share is crossed.
- **At 256 at once the ACK rate does not matter.** Both clients get about
  420k datagrams/s even though tls-client sends twice mimicry's ACKs;
  nginx's per-stream work is the limit there.

Fast bench, HTTP/3, 2026-10-29 (an uncommitted run; no report holds these
numbers): req/s (MB/s for 10 MB), mimicry / `mimicry-ack20` / tls-client:

| point | mimicry | ack20 | tls-client |
|---|---|---|---|
| nginx 1 KB c64 | 66,175 | 67,524 | 50,829 |
| nginx 1 KB c256 | 74,589 | 77,362 | 56,630 |
| nginx 100 KB c64 | 7,274 | 9,455 | 8,305 |
| nginx 100 KB c256 | 6,048 | 7,863 | 8,021 |
| nginx 10 MB c1 | 1,157 | **843** | 579 |
| nginx 10 MB c64 | 1,001 | 1,320 | 1,262 |
| nginx 10 MB c256 | 614 | 736 | 623 |
| go 100 KB c64 | 7,422 | 7,730 | 7,943 |

One stream is the exception. At 10 MB with one request at a time,
every 20 ran 27% slower: 1,104–1,118 MB/s with Chrome's rule against
807–814 MB/s, two runs each. nginx's worker is not saturated there
(9–11% of its CPU set); presumably fewer ACKs only clock its window out later (not measured further).

On a lossy path (RTT 80 ms, 1% loss, `bench run -layers 3 -protos h3
-servers nginx -load-netem rtt80-loss1`, 2026-10-28/29; reports
`internal/bench/reports/h3ack-netem-*`, for example
[10 MB c64, run 1](../../internal/bench/reports/h3ack-netem-10m64-1-2026-10-28.md)), every 20 is as
fast or faster. MB/s, median (range) of three runs; 100 KB at 30 s
warm-up and 60 s windows (c1: 5 s and 10 s), 10 MB c1 at 60 s and 180 s,
10 MB c64 at 30 s and 60 s:

| point | mimicry | ack20 | tls-client |
|---|---|---|---|
| nginx 100 KB c1 | 0.194 (0.178–0.195) | 0.217 (0.196–0.237) | 0.178 (0.169–0.194) |
| nginx 100 KB c64 | 0.242 (0.226–0.276) | 0.318 (0.315–0.356) | 0.212 (0.200–0.223)\* |
| nginx 10 MB c1 | 0.282 (0.281–0.287) | 0.296 (0.279–0.299) | 0.260 (0.247–0.282) |
| nginx 10 MB c64 | 0.334 (0.281–0.334)† | 0.317 (0.316–0.345)† | 0.485 (0.385–0.987)† |

\* Two runs: the third stayed on HTTP/2 through the warm-up. † The bytes
that arrived in the window: 64 bodies of 10 MB share about 0.3 MB/s, so
no request ends inside the run and the point is otherwise discarded.
At 1% loss, throughput is loss-limited (about 0.2–0.3 MB/s per connection)
and the ACK rate does not slow recovery: a gap is acknowledged at once
whatever the rate, and at 10 MB c64 the ranges overlap.

## Options

- **Keep Chrome's rate, with no option.** Browser-exact, but about 25%
  slower (10 MB) and about 18% slower (100 KB) whenever one client
  saturates a single server worker.
- **Read every queued datagram before acknowledging, as quic-go
  effectively does** (the profile's `Batched` mode). It needs no new rule,
  but the cadence would follow the machine's timing. It would also be
  Firefox's and Safari's rule rather than Chrome's.
- **An option to acknowledge every Nth packet** (chosen). It keeps every
  other ACK rule: the warm-up, max_ack_delay, the immediate ACK of a gap.
  A test can hold its cadence on the wire, and every 20 was measured to
  match or beat tls-client.

## Decision

`Config.HTTP3ACKEvery`, zero by default (the profile's rate).

- **What it does:** a value of at least the profile's rate and at most 64
  replaces `quic.ack.every` after the warm-up. Anything else is refused by
  `New`.
- **Why 64 at most:** a sender whose window holds fewer packets than N
  would wait for max_ack_delay instead of the count.
- **How it is applied:** the engine dials with a shallow copy of the
  profile's QUIC section, because profiles are shared and read-only.
  Nothing in `internal/quic` changes.
- **Why the default stays Chrome's:**
  - It only pays when a single client saturates one server worker. At 1 KB
    and at 256 at once it changes little (see the bench rows below).
  - The ACK rate is visible to the server.
- **The bench variant:** `mimicry-ack20`.

## Consequences

- **Testing:** `TestHTTP3ACKEvery` downloads the h3-download page at every
  20 and holds the ACKs to that cadence (98.4% exact). Without the
  engine's plumbing it fails: 0 of 3,695 ACKs were every 20.
- **Fidelity:** a caller who sets the option sends an ACK rate no browser
  of the profile's sends.
- **Loss:** a sender's loss detection works from gaps (a packet threshold
  of 3 and a time threshold), not from how often ACKs come. With every 20,
  nginx's socket saw no extra receive errors in the measurements above.

## Revisit when

- A capture shows Chrome changing its ACK rate (another `quic.ack.every`,
  or ACK frequency negotiation), or another browser's rate costs the same.
- The comparison is repeated with the 1 MB receive buffer (0045) on the
  Linux test host and the gap to tls-client closes or grows.
- A vendor is seen reading a client's ACK cadence: the option's risk goes
  up.
