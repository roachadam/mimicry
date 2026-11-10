# 0043: An opt-in lower HTTP/3 ACK rate (`Config.HTTP3ACKEvery`)

- Status: Accepted
- Date: 2026-10-29
- Commits: `f5772cb`

**Summary.** HTTP/3 keeps Chrome's ACK rate (every 10th packet) by
default, and `Config.HTTP3ACKEvery` (from the profile's rate up to 64)
lets a caller acknowledge less often. Slower bulk downloads against nginx
were traced to nginx being ACK-clocked on one saturated worker, not to a
mimicry bug, and the ACK rate is visible to the server. A library user
downloading large bodies from such a server can set it (20 measured to
match or beat tls-client) at the cost of an ACK cadence no browser sends.

## Problem

Against nginx, mimicry's HTTP/3 bulk downloads ran up to about 25% below
tls-client's (10 MB bodies, 64 at once, fast bench 2026-10-28: 97 against
120 requests/s). The cause was not obvious, and two readings pointed in
opposite directions. The Chrome profile acknowledges every 10th packet.
quic-go's tracker reads as acknowledging every 2nd (`packetsBeforeAck = 2`),
so quic-go should send five times as many ACKs and be the slower one, yet it
is faster. Before choosing anything the real cause was needed, and whether it
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

## Amendment 2026-11-02: Firefox's ACK after a quiet spell (`quic.ack.afterRtt`)

**Summary.** Firefox acknowledges a packet at once when it comes a while
after its last ACK, where `quic.ack` (every 2nd packet, batched) held it
for the 20 ms ACK delay. The profile field `quic.ack.afterRtt`, derived
by h3gen, says so, and a client with it sends a pending ACK as soon as a
smoothed RTT has passed since its last ACK (issue #40). Both Firefox
profiles have it; Chrome's and Safari's captures do not show it.

**The issue's hypothesis was a different rule.** #40 read Firefox's
immediate ACKs as "two packets of any kind since the last ACK", counting
the server's ACK-only packets. The captures cannot decrypt the server's
packets, so which ones are ACK-only is itself an inference from their
size and timing. Counting ACK-only packets explains the two-packet ACKs
#40 cites, but not the lone ones, and the two-packet ACKs fit the time
rule too.

**Evidence** (h3gen, `quictrace.LoneACKs`, over every edge capture). A
lone ACK is a client ACK-only packet whose only new packet is one server
1-RTT packet, so that packet elicited it. "Late": the server sent it 5 ms
or more after the client's previous ACK reached it. "At once": the ACK
reached the server within 3 ms of the packet.

| profile | late, at once | sooner, at once |
|---|---|---|
| Firefox 158, macOS | 70 of 70 | 72 of 215 |
| Firefox 158, Windows | 76 of 76 | 56 of 212 |
| Chrome 155 (macOS) | 27 of 44 | 249 of 323 |
| Chrome 156, Windows | 14 of 36 | 197 of 314 |
| Safari 27, macOS | 23 of 28 | 236 of 288 |
| Safari 26, macOS | 24 of 27 | 229 of 252 |

- Firefox's sooner ones that waited waited for the 20 ms ACK delay. The
  12 macOS ones the server sent before Firefox's previous ACK reached it
  and that were still acknowledged at once all had a missing packet in
  their ranges (RFC 9000's immediate ACK of a gap, already modelled).
- Over every h3 capture (at once read from the ACK Delay field, under
  3 ms), counting ACK-only packets leaves 144 of macOS's 262 lone ACKs
  at once unexplained (Windows 195 of 426). Its two-packet cases, one
  response with ACK-only packets around it, were at once 40 of 40 (Windows
  122 of 122); in 34 (99) the response came 3.7 ms or more after the last
  ACK, so the time rule sends them at once too. The other 6 came within
  0.4 ms of the last ACK (Windows 23 within 2.3 ms, its timestamps step
  by 0.5 ms), where at loopback RTTs neither rule is ruled out.
- **Source, not evidence:** neqo (Firefox's QUIC stack), `tracking.rs`:
  `RecvdPackets::ack_now` sends a pending ACK when its timer is due or
  when `last_ack_time + rtt <= now`, `rtt` being the path's smoothed RTT
  (`rtt().estimate()`); `unacknowledged_count` counts ack-eliciting
  packets only, against a tolerance of 1 (every 2nd). The check runs
  whenever neqo builds a packet, so after each batch of datagrams read;
  the ACK timer itself stays the ACK delay.
- **Measured vs inferred.** Measured: a lone packet arriving well after
  the last ACK is acknowledged at once, and one arriving right after it
  waits. Inferred, from the source: that the threshold is one smoothed
  RTT. The captures run over loopback (RTT 0.04–1 ms), so they cannot
  tell one smoothed RTT from a fixed gap below about 0.5 ms, or from any
  gap at all; mimicry follows the source there rather than fit a number
  to loopback.

**At real RTTs** (tens of ms), where it matters most: a response that
comes a round trip after the request finds the last ACK more than a
smoothed RTT old, so its first packet is acknowledged when it arrives
rather than up to 20 ms later. Within a bulk transfer, ACKs go every
2nd packet, far more often than once per RTT, so the rule never fires;
a lone packet arriving within an RTT of the last ACK still waits for the
ACK delay. On loopback almost any pause qualifies.

**Options.**

- Count ACK-only packets toward every 2 (#40's hypothesis): fits the
  two-packet ACKs only, and no capture favours it.
- A fixed gap (for example 1 ms) fitted to the loopback captures: no
  measured value to fit, and at real RTTs it would fire inside bulk
  transfers (packets 1 ms apart), where Firefox's rule does not.
- One smoothed RTT since the last ACK, as neqo's code says (chosen).

**Implementation.** `internal/quic`: `ackState.afterRTT` and `lastAck`
(set by `sentAck`, which now takes the time); `clientAckAfterRTT` makes
the send path treat a pending ACK as due once `lastAck` plus the smoothed
RTT has passed. The ACK timer is unchanged, so it only fires when the
connection sends anyway, a packet's arrival above all, as neqo's.
Profiles without the field (Chrome, Safari) behave as before.

**Replay.** `h3-streams-blocked`'s burst waits on macOS came 28–64 ms
after Firefox's (322 and 644 ms against 290–294 and 580–602); now 301–302
and 602–603, the rest the replay's own scheduling. The
STREAMS_BLOCKED pattern (mimicry's 7, 13 against Firefox's 6 7 8,
12 13 14) stays pinned: the replay sends a burst's first two requests in
one packet, Firefox 3.2–5.7 ms apart, so the server raises its limit by
two; with the requests 5 ms apart mimicry sends Firefox's pattern, and
without the rule it still does not; whether the replay's first two share
a packet is this process's scheduling, so the pin may not show.
`TestHTTP3EdgeMatchesBrowser` (`afterRTTRecord`) holds each Firefox
profile's late lone packets, over all its edge pages, to an ACK at once:
at most 5% (at least one) may wait, as a stall under -race can lift the
smoothed RTT past 5 ms, and a profile with none fails as unexercised.
In one run 38 of 38 (Windows) and 37 of 37 (macOS) went at once; with
the rule switched off, 0 of 28 did, and the test failed.

**Cost** (the Linux host of [performance](../performance.md), its
harness, nginx, HTTP/3, Firefox's profile with the field on and off in
one build, clients interleaved; median and range; 7 passes at 1 KB and
100 KB, 3 at 10 MB; datagrams are the client's UDP datagrams sent per
request in the window, nearly all ACK-only):

| point | off: req/s (MB/s) | on | off: CPU per request | on | off: datagrams per request | on |
|---|---|---|---|---|---|---|
| 1 KB, 64 at once | 89,291 (85,959–92,391) | 89,059 (72,622–93,741) | 34 µs | 34 µs | 0.65 | 0.65 |
| 100 KB, 64 at once | 7,670 (5,839–8,602) | 8,375 (6,243–10,039) | 200 µs | 186 µs | 4.58 | 4.01 |
| 100 KB, 64 at once, 7 MB buffer | 9,719 (8,559–10,038) | 9,652 (7,778–10,064) | 173 µs | 177 µs | 2.64 | 2.64 |
| 10 MB, one at a time | 791 MB/s (782–804) | 797 MB/s (793–800) | 14.1 ms | 14.0 ms | 237 | 237 |

No point moved beyond its range; at the 1 MB default the drops make
100 KB noisy both ways, as before. ACKs per download, against Firefox's
own: the h3-download replay (50 MiB at the capture's pace, about
10 MB/s) sends 9,857–10,600 ACKs with the rule and 10,467–10,586
without, 3.5–3.8 packets each; Firefox sent 6,520–7,363 (5.0–5.7 each),
on Windows 2,272–6,345. At the bench's 800 MB/s mimicry reads larger
batches and sends 237 per 10 MB (31 packets each) either way. The rule
adds no ACK to a steady download; the gap to Firefox's rate in the
replay is the batching's, and predates it.

**Revisit when** a capture over a real network (RTT of tens of ms)
shows where the threshold lies, or a Firefox version changes neqo's
`ack_now`.

## Amendment 2026-11-09: out-of-order ACKs are not counted (issue #122)

**Summary.** The bulk ACK check (`bulkRuleViolations`, behind
`TestHTTP3ACKEvery` and `TestHTTP3DownloadMatchesBrowser`) no longer
counts ACKs sent out of order: those whose new packets leave a gap above
the previous ACK's largest, or fill one below it. A client sends them at
once whatever its count (RFC 9000, section 13.2.1, which this decision
keeps), so `quic.ack.every` does not govern them. No library change.

**Problem.** `TestHTTP3ACKEvery` failed most runs on the maintainer's Mac
(75–89% of ACKs every 20, want 95%) and passed on CI. Each ACK's step
was its Largest Acknowledged less the previous ACK's, so an immediate ACK
at a gap counted as a broken cadence.

**Evidence** (2026-11-09, the Mac, chrome-157-linux at every 20; another
agent's browser captures were loading the machine):

- A failing run: 1,773 of 2,240 ACKs advanced by 20. Of the 467 others,
  447 left a gap in their new range, and 20 rode on a MAX_STREAM_DATA or
  MAX_DATA packet. None waited for the ACK timer (ACK Delay 0).
- The gaps were datagrams dropped on loopback: 759 server packets were
  never acknowledged, in 441 runs of 1–7. In a second run 483 server
  datagrams were never acknowledged, and `netstat -s -p udp` counted 482
  more "dropped due to full socket buffers" over it: the client's receive
  buffer, with the server sending at about 200,000 datagrams a second
  (3–6 µs apart). Each loss gives one short ACK, after which the count
  starts again at 20.
- Every browser sample (all profiles' h3-download captures) has 5–8
  such ACKs a download, at the packet numbers quic-go skips; Chromium's
  are not every 10 either. No sample lost a packet.

**Options.**

- Stop the loss, with a larger buffer or a paced server: the drops are
  the machine's (a loaded Mac's loopback; at every 20 the server sent
  30,000 packets in 150 ms, against 250–420 ms in chrome-157-linux's
  samples; mimicry's 1 MB buffer is Chrome's, decision 0045), not the
  ACK rule's, and a loss would still fail the test.
- Count packets newly acknowledged instead of the step in Largest
  Acknowledged: an ACK at a gap is still sent early, so it still fails.
- Leave out ACKs out of order (chosen): what the count governs, on
  mimicry and the browsers alike.

**Result.** The failing runs' captures pass at 98.9% (1,773 of 1,793).
20 runs each of chrome-157-linux and the default run (brave-157-linux):
all passed, 98.8–99.1% exact, 5–438 ACKs left out. Every profile's
samples (`MIMICRY_PROFILES=all`) are now 98.3–98.9% exact (Chromium) and
99.6–100% (Firefox, Safari). With the override at 10 or 30 against the
check at 20, the test fails (0 of 3,592 and 1 of 1,209 every 20).
