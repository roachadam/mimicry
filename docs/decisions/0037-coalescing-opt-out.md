# 0037: HTTP/2 coalescing stays the browser's; `DisableCoalescing` and `MaxConnsPerHost` for speed

- Status: Accepted
- Date: 2026-10-28
- Commits: `c1cd089`

**Summary.** mimicry keeps the browsers' HTTP/2 coalescing by default and
adds `Config.DisableCoalescing` to give every host its own connection,
plus `Config.MaxConnsPerHost` to change the HTTP/1.1 per-host limit of 6.
Measured, coalescing is faster on clean networks and only slower under
packet loss, so the browser-exact default is also the faster one there. A
library user on a lossy network can turn coalescing off, and one pushing
an HTTP/1.1-only server hard can raise the limit, each at a detection
risk the record spells out.

## Problem

Every browser shares one HTTP/2 connection between hosts whose
certificate covers them and whose address matches (decision
[0023](0023-connection-limits-and-coalescing.md)): Chrome and both Safaris
after a DNS lookup (`connections.coalesce` `lookup`), Firefox after opening
a connection of its own, which it then closes with a GOAWAY (`connect`).
mimicry does the same. A site whose `www`, `static` and `api` hosts sit on
one address under one certificate then loads over one TCP connection, so
every response shares one congestion window. In the benchmark's page loads
under packet loss mimicry was up to twice as slow as clients that open a
connection per host; the investigation traced it to that connection.

The maintainer's rule (2026-10-28): **when a browser behaviour costs significant
performance and is a low-ish detection risk, the default is the faster
setting, documented plainly, with a `Config` option for the browser-exact
behaviour** (decision [0036](0036-fast-quic-fallback.md) is the case it
was first applied to). The question was whether coalescing is such a
behaviour.

Separately, the per-host HTTP/1.1 limit (6) caps mimicry's throughput to
an HTTP/1.1-only server at high concurrency, and callers asked for a way
to raise it.

## Options

- **No coalescing by default, an option for the browser's.** Faster under
  loss. Rejected once measured with single-segment loss (below): without
  loss coalescing is the faster one, so the default would trade a cost on
  clean networks for a gain on lossy ones, and depart from all three
  browsers on every site whose hosts share a certificate and an address.
- **Keep coalescing, add an opt-out, `Config.DisableCoalescing`**
  (chosen; the maintainer's decision, 2026-10-28). The default stays browser-exact,
  and faster where the network is clean; a caller on a lossy network
  turns it off. Named like `DisableHTTP3`: a switch that departs from the
  browser on request.
- **Coalesce only some hosts, or leave a coalesced connection when it
  loses packets.** No browser does either, so each would be a fingerprint
  of its own, and loss shows only after the connection is shared.
  Rejected.
- **Fix it on the client's side of TCP.** Not possible: the server's
  congestion control decides how fast one connection carries a page, and
  nothing the client sets on its socket changes that.

## Decision

- Coalescing stays as decision 0023 has it, by default.
- `Config.DisableCoalescing` gives every host its own HTTP/2 connection:
  no DNS lookup for another host's connection (Chrome, Safari), no
  connection opened only to be declined (Firefox). Everything else about
  connections is unchanged: one HTTP/2 connection per host, the partition
  and proxy rules, HTTP/3 (which never coalesces).
- `Config.MaxConnsPerHost`: zero keeps the profile's per-host HTTP/1.1
  limit (`connections.http1PerHost`, 6 in every browser); a positive value
  raises or lowers it. HTTP/1.1 only: HTTP/2 and HTTP/3 use one connection
  per host. A Firefox profile's extra TCP connections when a QUIC
  connection dies with requests pending (`PendingTCP`) stay bounded by the
  profile's limit, as the browser's are.

Set `DisableCoalescing` when the network loses packets (mobile, congested
Wi-Fi, long paths) and many requests go to hosts that share a certificate
and an address; leave it off on clean networks, where coalescing is faster
and exact. Traffic through a proxy never coalesces (decision 0023), so the
option changes nothing there.

## Evidence

Page loads in the benchmark (`internal/bench`, layer 5: one HTML and 30
subresources over `www`, `static` and `api` on the Go server, one
certificate and one address;
[report](../../internal/bench/reports/coalescing/bench-2026-10-28.md)), 15
cold and 15 warm loads per client and condition, with segmentation
offloads off under netem so that "1% loss" drops single segments (the
benchmark's netem dropped groups of segments until then; see
[its README](../../internal/bench/README.md)). Medians in ms, mimicry by
default against `mimicry-nocoalescing` (`DisableCoalescing`), p from a
two-sided Mann-Whitney test; HTTP/2:

| Condition | Default (coalescing), cold / warm | `DisableCoalescing`, cold / warm | p, cold / warm |
|---|---|---|---|
| no netem | 11 / 6 | 14 / 5 | 0.27 / 0.03 |
| RTT 20 ms | 330 / 96 | 360 / 97 | 0.001 / 0.98 |
| RTT 20 ms, 1% loss | 857 / 1,731 | 750 / 881 | 0.40 / 0.00004 |
| RTT 80 ms | 955 / 350 | 1,312 / 436 | 0.00001 / 0.0001 |
| RTT 80 ms, 1% loss | 4,000 / 6,084 | 2,870 / 3,365 | 0.25 / 0.003 |

- **Clean networks favour coalescing.** Each extra host costs a TCP and a
  TLS handshake and a slow start of its own: 0.36 s of a cold load at
  80 ms (`DisableCoalescing` 37% slower), 30 ms at 20 ms; warm loads at
  80 ms are 86 ms slower with the window split three ways.
- **Lossy networks favour disabling it.** Warm loads take about half as
  long without coalescing (881 against 1,731 ms at 20 ms; 3,365 against
  6,084 ms at 80 ms); cold loads spread so widely (interquartile range
  2.1–5.8 s at 80 ms) that neither is significantly faster. The earlier
  investigation found why, with tcpdump and `ss` in the containers: the
  coalesced connection carried about 91% of the page, losses cut its
  window, a retransmission timeout before the warm load left it at one
  segment, and the server's slow start after idle made the warm load climb
  from there.
- **HTTP/3** loads move to QUIC after the first requests, which never
  coalesce; only the first HTTP/2 requests differ (cold at 80 ms 1,265
  against 1,376 ms with `DisableCoalescing`, p 0.002; nothing significant
  under loss; the returning client, all HTTP/3, identical).
- **The other clients do not coalesce.** tls-client and httpcloak open one
  connection per host (3 per page here); azuretls and net/http open more.
  Under 1% loss at 80 ms their warm loads (3,571 and 3,387 ms) match
  `DisableCoalescing`'s, so with it mimicry behaves like them there.

The cost of coalescing depends on the network, and on a clean one the
browser's behaviour is the faster: it does not meet the rule, so
the default stays the browser's.

**Detection risk of `DisableCoalescing`: low to medium.** Only a server
that hosts several of a site's hosts at one address under one
certificate can see it: where Chrome, Safari and Firefox end with one
connection (Firefox after its declined one), it sees one TCP connection
and one TLS handshake, with its SNI, per host, from one client within a
page load. Each connection on its own is the browser's: the same
ClientHello, SETTINGS and headers. All three browsers coalesce; Safari
too (`lookup` in both Safari profiles, measured in the `connections`
capture: no connection to `static` or `y`;
[EVIDENCE.md](../../profiles/EVIDENCE.md#connections)), so the option
matches none of them. Sites whose hosts have their own certificates or
addresses get separate connections from browsers too, and nothing differs.

`TestConnectionsMatchBrowser` replays the `connections` page in both
modes against every sample: by default as before; with `DisableCoalescing`
against the same samples with every coalesced host on a connection of its
own and the declined connections (Firefox) left out. Leaving coalescing on
in the second mode fails it for all three browsers.

`Config.MaxConnsPerHost`, HTTP/1.1 at concurrency 64 (`internal/bench`
layer 3, 10 s windows;
[report](../../internal/bench/reports/conns-per-host/bench-2026-10-28.md)),
requests/s:

| Server, body | 6 (default) | 16 | 64 | net/http |
|---|---|---|---|---|
| nginx, 1 KB | 76,849 | 127,840 (1.66×) | 197,113 (2.56×) | 231,222 |
| nginx, 100 KB | 30,028 | 46,653 (1.55×) | 74,313 (2.47×) | 78,620 |
| nginx, 10 MB | 440 | 936 (2.13×) | 1,050 (2.39×) | 1,060 |
| Go, 1 KB | 67,866 | 114,701 (1.69×) | 157,516 (2.32×) | 191,159 |
| Go, 100 KB | 41,686 | 63,331 (1.52×) | 72,182 (1.73×) | 77,545 |

`TestMaxConnsPerHost` (`internal/engine/native`) checks that the limit
holds at the profile's 6, below it and above it.

## Consequences

- Nothing changes by default. Callers on lossy networks have a switch,
  and the README lists it with the other options that trade fidelity for
  speed.
- With `DisableCoalescing`, each extra host costs a handshake and keeps
  its own TLS session and ticket, as hosts with separate certificates do,
  and `Plan` no longer leaves the protocol open for a lookup.
- `MaxConnsPerHost` above 6 is visible: no browser has more than 6
  HTTP/1.1 connections open to one host, so a server that counts a
  client's concurrent connections sees it.
- The benchmark has `mimicry-nocoalescing`, `mimicry-conns16` and
  `mimicry-conns64` variants for measuring the options.

## Revisit when

- A vendor is seen counting connections or TLS handshakes per site (the
  option's risk goes up), or a browser stops coalescing or starts leaving
  a coalesced connection under loss.
- Measurements on real networks, not netem, show the loss case dominating
  for typical users: then the rule would favour turning coalescing off by
  default.
