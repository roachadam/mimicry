# 0045: QUIC sockets ask for Chrome's 1 MB receive buffer by default

- Status: Accepted
- Date: 2026-10-29
- Commits: `a454c2a`

## Problem

The benchmark at `9696132` on the Linux test host (`internal/bench/reports/linux-2026-10-29/`)
had mimicry at 4,316 req/s for HTTP/3, 100 KB bodies, 64 at once, with 2,814
datagrams dropped on a full receive buffer (`RcvbufErrors`). The other
clients ran at 7,400-8,600 req/s and dropped none. They are quic-go and its
forks, which ask for a 7 MB buffer and force it past `net.core.rmem_max`
with `SO_RCVBUFFORCE` when the process has `CAP_NET_ADMIN` (the bench's
client container does). mimicry, before this change, asked for nothing
(`Config.UDPReadBufferSize` 0) and kept the OS's default, 208 KB on Linux.
An earlier Mac run had found
7 MB halving 100 KB at 256 at once, so a bigger default was not obviously
better. That needed checking on Linux, after the gate fix
(`internal/quic/UPSTREAM.md` change 15).

The buffer is local: nothing on the wire changes with it, so this is a
performance default, not a fingerprint question.

## Evidence

**What the kernel grants.** These are from a Go probe in the bench's client
container (`reports/focus-2026-10-29/udpbuf-granted.txt`). Linux grants
twice the request, up to twice `rmem_max`, and never errors. A socket that
asks for nothing gets `rmem_default`.

- The Linux test host (kernel 6.18) has a host `rmem_max` of 16 MB and
  `rmem_default` 212992. Asking for 1 MB got 2 MB, 2 MB got 4 MB, and 7 MB
  got 14 MB.
- On stock Linux (both at 212992, OrbStack, no `NET_ADMIN`), any request
  got 425984 (416 KB). `SO_RCVBUFFORCE` failed with EPERM.
- So on a stock host, every default above 208 KB gets the same 416 KB. Only
  a host that raised `rmem_max` gets more.
- The test host's kernel keeps both sysctls global. runc refuses to set them in a
  container (`open sysctl net.core.rmem_max file: reopen fd 8: permission
  denied`), so the stock-buffer passes can't run there. Instead,
  `mimicry-udp208k` (asking for 212992, so granted 416 KB) stands in for
  stock Linux on that host. The full stock case, all clients, ran on the
  M3 Pro laptop.

**What Chrome asks for.** Chromium sets
`SetReceiveBufferSize(kQuicSocketReceiveBufferSize)` on every QUIC socket.
The constant is `1024 * 1024` in `net/quic/quic_context.h`, used in
`quic_session_pool.cc` (chromium/src main at 7038dfb4ec29). On the M3 Pro
laptop, a Chromium-based desktop app's live QUIC socket (to port 443) showed
`rhiwat` 1048576 in `netstat -anvp udp`, against 786896 (macOS's default)
on Chrome's mDNS sockets. This is source and one observation, not a
capture. It decides nothing about the wire; it only says the browser's own
choice is 1 MB.

**Benchmarks.** All on the Linux test host: nginx, HTTP/3, 3 passes each, medians
(range). Client CPUs 0,2,4,6; nginx 8-11; `rmem_max` 16 MB, so the request
is doubled. The reports are in `internal/bench/reports/focus-2026-10-29/`:
`udpbuf-before` (the old default), `udpbuf-curve` (512 KB and 1 MB added)
and `udpbuf-after` (this change; `mimicry-udpos` is the old default).

| point | OS default (208 KB) | 1 MB (2 MB granted) | 2 MB | 7 MB | tls-client | net/http |
|---|---|---|---|---|---|---|
| 100 KB c64 req/s | 4,211 / 4,321 / 4,289 | 4,864 / 4,725 | 5,957 / 6,901 / 6,742 | 7,794 / 8,653 / 8,612 | 7,093 / 7,574 / 7,560 | 7,337 / 7,607 |
| 100 KB c64 drops | 3,325 / 2,702 / 2,676 | 513 / 167 | 877 / 110 / 55 | 0 | 0 | 0 |
| 100 KB c256 req/s | 3,487 / 3,523 / 3,553 | 3,580 / 3,703 | 2,209 / 3,583 / 2,001 | 1,901 / 2,682 / 1,736 | 3,273 / 4,126 / 4,282 | 3,991 / 3,984 |
| 100 KB c256 p99 ms | 86.5 / 87.6 / 82.3 | 133 / 132 | 169 / 97.0 / 160 | 209 / 211 / 194 | 97.0 / 84.4 / 85.5 | 83.4 / 83.4 |
| 1 KB c256 req/s | 97,392 / 99,573 / 92,304 | 110,213 / 109,061 | 105,928 / 111,886 / 110,744 | 106,322 / 112,905 / 110,948 | 62,493 / 66,480 / 66,709 | 96,912 / 101,494 |
| 1 KB c256 drops | 1,342 / 1,404 / 1,261 | 0 | 0 | 0 | 0 | 0 |

Each cell lists that setting's runs in order: before, curve, after. A run
that left a client out has fewer values. Each value is a 3-pass median.
Rows with 1 KB at 1 and 64 at once, and 100 KB at 1, are equal within
noise for every size. 512 KB tracked the OS default: 4,463 at 100 KB c64
and 3,522 at c256.

- **The c256 stall holds on Linux.** At 7 MB, nginx's worker fell to
  25-33% of a core, against 98-99% everywhere else. 2 MB fell the same way
  in two runs of three. The mechanism is the one the backlog records
  ("HTTP/3 bulk throughput against nginx"). The client answers its backlog
  in bursts that overflow nginx's socket. Its congestion window falls, and
  its MAX_DATA waits behind it while nginx idles on flow control. At 256
  bodies of 100 KB, 25 MB is in flight against Chrome's 15 MB connection
  window, so the connection's flow control is in play. At 64 at once (6.4
  MB) it is not.
- **1 MB held at c256.** Its medians were 2-4% above the OS
  default, but one pass of the curve run fell to 2,781. Its p99 rose from
  82-88 ms to 132-133 ms in both runs: the queue the buffer holds.
- **At equal buffers mimicry still drops more.** On the M3 Pro laptop at stock
  values (every client 416 KB), at 100 KB c64 mimicry dropped 14.7-17.2k
  datagrams and net/http 5.4-5.6k. So the gap left at 1 MB is the receive path
  (backlog: "HTTP/3 receive path runs on about one core"), not the buffer.

**Stock Linux, all clients** (M3 Pro laptop, OrbStack, `BENCH_UDP_STOCK=1`, 1 pass
each; `udpbuf-before-stock-mac`, `udpbuf-after-stock-mac`). Keep these
apart from the Linux test host's numbers. mimicry's 1 MB gets 416 KB there, like every
other client. 100 KB at 64 and 256 at once was server-bound (nginx's worker
96-99%) for every client, and mimicry at 1 MB equalled the OS default
there: 7,606 against 7,575 (c64), 5,941 against 5,982 (c256). 1 KB at 256
at once ran 85,284 against 76,917. Other rows were unchanged.

## Options

- **Keep the OS's default.** This leaves the 100 KB c64 gap and the drops
  at 1 KB c256. On macOS it is 768 KB anyway.
- **7 MB, quic-go's.** It closes the c64 gap (8,612 against tls-client's
  7,560), but halves 100 KB at 256 at once in two runs of three. A default
  that stalls high-concurrency downloads is worse than one that is slower
  at 64.
- **2 MB.** About 60% faster at c64, but stalls at c256 in two runs of
  three.
- **512 KB.** No measurable gain at 100 KB, and it would shrink macOS's
  768 KB default.
- **1 MB, best effort, no `SO_RCVBUFFORCE`** (chosen). It is Chrome's own
  request. It never failed a row in the benchmarks. In the runs above
  100 KB c64 ran 10-13% faster with 81-94% fewer drops, and 1 KB c256
  11-18% faster with no drops. The cost is the p99 at 100 KB c256. On
  stock Linux it gets 416 KB, so it changes little there.

The final benchmark at `a454c2a`
([linux-final-2026-10-29](../../internal/bench/reports/linux-final-2026-10-29/final-2026-10-29.md),
layer 3, HTTP/3, nginx, 100 KB c64) reproduced the drop and not the speed:
mimicry at 4,408 (4,405-4,492) req/s with 447 drops, against 4,316 and 2,814
drops at `9696132`: about 2% faster, 84% fewer drops.

`SO_RCVBUFFORCE` stays out at any size: it needs `CAP_NET_ADMIN`, and a
library should not take more than the host's administrator allows.

## Decision

`Config.UDPReadBufferSize` 0 asks for 1 MB (`dial.DefaultUDPReadBufferSize`)
on every QUIC socket, a SOCKS5 association's included. A negative value
keeps the OS's default; it used to be an error. A positive value asks for
that size. The request is best effort, through `SetReadBuffer`, and an
error is ignored.

## Consequences

- Hosts that raised `rmem_max` get a 2 MB buffer per QUIC connection;
  stock Linux hosts get 416 KB; macOS gets 1 MB. That is up to 2 MB of
  kernel memory per connection, and only while datagrams are queued.
- The 100 KB c64 gap to the quic-go clients stays: 4,408 req/s in the
  final benchmark (`a454c2a`) against 7,383-8,516. Closing it needs the receive path faster, or the
  c256 stall removed so that a larger buffer is safe. Both are in the
  backlog.
- The bench's `mimicry-udpos` measures the old default. `mimicry-udp208k`
  stands in for stock Linux on hosts that cannot set the sysctls per
  container.
- `TestUDPReadBufferSize` checks the default, a size, one above the cap and
  the OS default against a reference socket asking the same.

## Revisit when

- The HTTP/3 receive path or the c256 flow-control stall is fixed:
  re-measure 2 MB and 7 MB at 100 KB, 64 and 256 at once.
- Chrome changes `kQuicSocketReceiveBufferSize`.
