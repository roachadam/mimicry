# Benchmarks: mimicry against other Go clients

mimicry against `net/http` (the baseline; quic-go's `http3.Transport` for
HTTP/3) and three browser-impersonating clients, each with its newest Chrome
profile: [`bogdanfinn/tls-client`](https://github.com/bogdanfinn/tls-client),
[`sardanioss/httpcloak`](https://github.com/sardanioss/httpcloak) and
[`Noooste/azuretls-client`](https://github.com/Noooste/azuretls-client). Their
versions are pinned in `go.mod`; this is a module of its own so none of them
ever reaches the library's or the harness's dependencies. Reports are in
[`reports/`](reports/).

## What it measures

| Layer | What | Where |
|---|---|---|
| 1 | One request on a warm connection: ns/op, B/op, allocs/op; GET /json and POST /echo (small JSON); HTTP/1.1, HTTP/2, HTTP/3 | `bench_test.go` `BenchmarkRequest`, 6 passes of `-count 1`, benchstat |
| 2 | One request on a new connection: full handshake (a new client each time) and resumed (connections closed, session cache kept; up to 1% + 1 misses tolerated and counted as `unresumed`); the constructor alone in a tight loop | `bench_test.go` `BenchmarkConnect`, `BenchmarkNewClient` |
| 3 | Throughput and latency: requests/s, p50/p95/p99, MB/s, client CPU per request, allocations, GC, peak heap, at concurrency 1/16/64/256, bodies 1 KB/100 KB/10 MB (the default run: 1 and 64, 1 KB and 100 KB), a window after a warm-up, both stretched for long requests; the server's CPU per nginx worker; UDP receive drops per HTTP/3 point | `cmd/bench` |
| 4 | Memory: heap, goroutines and goroutine stacks (`StackInuse`) per client and per idle connection, at two client counts (10 and 50) so the report separates per connection (slope) from fixed (intercept); GC under load is in layer 3 | `cmd/bench` |
| 5 | Realistic network: a page load (one HTML + 30 subresources over www, static and api) under `tc netem` (RTT 20 and 80 ms, loss 0 and 1%), cold and warm, 10 times (20 under loss); over HTTP/3, mimicry also as a returning browser (new Transports sharing an Alt-Svc cache an earlier visit filled); the same through a CONNECT proxy (with and without Basic auth) and SOCKS5, the proxy with its own delay, each setup checked against the proxy's per-user counts | `cmd/bench` |
| 6 | Many clients, one proxy identity each: N clients (100) in one process, each its own Transport or session with its own proxy credentials, each sending GET 1 KB or 100 KB to nginx one request at a time; HTTP/2 and HTTP/1.1 through CONNECT with Basic auth, HTTP/2 and HTTP/3 through SOCKS5 (socks5h, username and password). Requests/s, p50/p99, CPU and allocations per request, heap per client at steady state and peak, goroutines and stack per client, errors; a rotation variant (a new identity every K requests, 10, over a 30 s window; the default run: HTTP/2 through CONNECT, 1 KB) adds new-connection latency, handshakes/s, the cost per new identity and where memory levels off; client rotation (a new client every K requests, dropped or closed); 1 KB again at a second N (25) for memory per client as slope and intercept | `cmd/bench` |

The default run measures a part of this, picked for the workload it is
for ([Default run](#default-run)); [Deep checks](#deep-checks) lists the
rest and how to run each.

A load point's throughput is the response body bytes that arrive inside
its window, counted as the client reads them: MB/s, and requests/s as
responses' worth of those bytes (CPU and allocations per request divide by
the same), wherever the requests started and ended. Its latencies are the
requests in flight during the window. Counting the requests that ended
inside the window made bulk points depend on where the window fell: nginx
shares a connection's bandwidth evenly between streams, so with Chrome's
stream windows all 256 of mimicry's 10 MB requests end together, about
every 4 s, and the fast run's 3 s window held a whole round or a few
stragglers (4 to 72 requests/s between runs of the same throughput).

Counting bytes was not enough for long requests either: a fixed 3 s window
at 10 MB and 256 at once held one round or less, so the per-request
allocations of a client whose costs come at a request's start (mimicry
over HTTP/3: 96 allocations per request at c1 and c64, 10-20 at c256)
depended on how many requests started inside it, and the buffering
clients (httpcloak, azuretls), whose bodies count only once whole, got
0, 1 or 2 rounds' worth. So both phases stretch (`scenario.LoadSpec`): the
warm-up runs at least `-warmup` and until a request per concurrent one has
completed and it has lasted twice their median latency; the window runs
at least `-window` and five warm-up medians, and until 3 requests per
concurrent one have started and finished inside it (up to `-max-window`,
120 s; the point records it when capped). Each point's JSON has the
warm-up and window it ran, and how many requests started inside the
window and how many of them finished there; the table gives the window.
Small bodies are unaffected: they meet the rule within the minimum window.

A point is **server-bound** when the server reaches 90% of its CPU set or
one nginx worker 90% of a core (nginx's `/_stats` reports each worker's CPU
time from `/proc/<pid>/schedstat`; one connection is one worker's). It is
not discarded: it stays in the tables, marked §, for every client if any
client's run of it was server-bound, and every pair's summary leaves it
out (a separate summary covers those points). Before, a point above 90%
was discarded for the client that reached it only, which dropped the
fastest client's point from its own geomean.

Every measured response is checked against the protocol under test as the
server saw it (`x-proto`); a measurement with one response on another
protocol is discarded with the reason. A client that lacks something is
n/a, never faked. Each layer 3-6 measurement runs in a process of its own
so clients' heaps and GC never mix. The fairness notes (how each client is
held to each protocol, which bodies are buffered, which headers each sends,
where mimicry is slower because Chrome is) are in every report.

## Layout

```
bench_test.go       layers 1-2 (Go benchmarks; run in the client container)
clients/            one adapter per client behind clients.Client
scenario/           warm-up, load, idle memory and page-load measurements
site/               what the servers serve: payloads, JSON, the page
target/             host names, ports, protocols, the CA, server stats
stats/              /_stats: a container's CPU time from cgroup v2
cmd/bench/          layers 3-6: `bench run` (orchestrator), `bench worker`, `bench wait`, `bench merge` (builds side by side, or `-passes`: medians and ranges), `bench microstat` (benchstat without its flagged geomeans); `bench offloads` (the sidecar)
cmd/server/         the Go server (net/http + quic-go http3)
cmd/proxy/          CONNECT (with/without Basic auth) and SOCKS5 (CONNECT, UDP ASSOCIATE) proxy, counting per user
cmd/setup/          the CA (once), the server certificate, nginx's files
docker/             Dockerfile, compose.server.yml, compose.client.yml (+ compose.client.udpstock.yml), nginx config
scripts/bench.sh    drives both stacks; the moon tasks call it
reports/            bench-<date>.md/.json (and its -passN, -udpstock-passN parts), micro-<date>.txt and -benchstat.txt
```

Two compose stacks. **Server** (`docker/compose.server.yml`): nginx 1.31
(official image, built with `http_v3_module`), the Go server and the proxy,
each with fixed addresses on the `mimicry-bench` network and Docker aliases
`{www,static,api}.nginx.bench.test`, `{www,static,api}.go.bench.test` and
`proxy.bench.test`. The servers share one CPU set (`4-11`; only one is under
load at a time) and nginx runs a worker per CPU of it; the proxy's (`10-11`)
lies inside it, which is harmless in layers 5 and 6, where the servers carry
little load (layer 3 never uses the proxy). Both
servers serve the same endpoints with the same bytes and headers:

| Port | nginx | Go | |
|---|---|---|---|
| TLS h2 + h1, UDP h3, `Alt-Svc` | 8443 | 9443 | `/bytes/{n}`, `/json`, `POST /echo`, `/page/…` |
| TLS http/1.1 only, no `Alt-Svc` | 8444 | 9444 | the same |
| plain HTTP | 8080 | 9080 | `/_stats`: CPU time, CPUs, connections (Go), each worker's CPU time (nginx) |

Proxy: CONNECT on 3128, CONNECT with Basic auth on 3129, SOCKS5 on 1080
(username and password when the client offers them, else none; CONNECT
and UDP ASSOCIATE), stats on 9081. It accepts the user `bench` and every
`bench-<anything>`, with the password `bench-secret`, and counts per user
what it relays (tunnels and UDP associations opened and still open,
datagrams, bytes each way: `/_proxy?prefix=…` on the stats port), which
layers 5 and 6 check their clients against. It relays tunnels with plain
`io.Copy` between the two TCP connections, which Linux splices, and reads a
tunnel's bytes from its server-side socket's `TCP_INFO` (`tcpi_bytes_received`
down, `tcpi_bytes_acked` up), live while it is open and once more at close;
from 2026-10-28 to the audit fixes it copied through a counting reader
instead (32 KB copies through user space), so the proxy rows of those
reports are not comparable with others'. A UDP association gets a relay
socket on the address the client
reached the proxy at, ephemeral and not published: HTTP/3 through SOCKS5
needs the client on the proxy's Docker network, so with `BENCH_SERVER` its
layer 6 rows are n/a. A `setup` step writes a CA (created
once, kept in the `mimicry-bench-ca` volume) and one certificate for every
name; every client verifies against that CA.

**Proxy delay.** Everything the proxy sends waits `BENCH_PROXY_DELAY` (5
ms), so a round trip through it gains about 10 ms. `BENCH_PROXY_DELAY_MODE`
says how: `netem` on the proxy container's `eth0`
(`docker/proxy-entrypoint.sh`), or `proxy`, the proxy's own delay line
(`cmd/proxy/delay.go`) for Docker hosts whose kernel has no `sch_netem`
(that host's). The delay line holds each relayed chunk (up to 32 KB) and
datagram, both ways, for the delay after it was read, in a queue per
direction (64 chunks, then the reader waits), so throughput stays and only
latency grows; it also holds the dial to the server and the handshake
replies, as netem holds the SYN, SYN-ACK and replies. TCP's ACKs are not
held, so each leg's congestion control sees its bare RTT; tunnels go
through user space instead of splicing. `bench.sh` probes the Docker host
for netem and picks `netem` where it works, else `proxy`; the report's
Environment says which. Checked on the Mac (OrbStack, netem available,
2026-10-29): layer 6, fixed identities, 50 clients, 1 KB and 100 KB over
HTTP/1.1, HTTP/2 (CONNECT with auth, SOCKS5) and HTTP/3 (SOCKS5), net/http
and mimicry, 5 s windows, run netem, proxy, netem, proxy: over the 14
points the delay line's requests/s were 1.025× netem's and its p50 latency
0.974× (geometric means; p50 14-18 ms either way, p99 within each other's
range), while the two netem runs alone differed 5.8% per point (RMS of the
log ratio). On battery, both modes under the same conditions.

**Client** (`docker/compose.client.yml`): one container with every client,
its own CPU set, a fixed `GOMAXPROCS`, `NET_ADMIN` for netem, the CA volume
read-only, and `extra_hosts` mapping every name to the server stack's fixed
addresses, or to `BENCH_SERVER`. Beside it, the `offloads` sidecar on the
Docker host's own network (`NET_ADMIN`, port 9082), only where the host has
netem.

**Segmentation offloads are off under netem.** With TSO, GSO and GRO on,
packets reach the client container as super-packets of up to 64 KB (the
server's TSO, or GRO on the host's end of the server's veth, put segments
together and nothing on a veth path cuts them up again), so netem would
delay and drop several segments at a time: "1% loss" would be loss of
whole groups of segments. Turning the servers' own TSO and GSO off is not
enough, as the host's GRO joins their segments again. For every netem
condition layer 5 turns TSO, GSO and GRO off on the client's `eth0` and,
through the `offloads` sidecar, on the host's end of the client's veth,
which then cuts every packet to the client into MSS-sized segments; the
condition without netem, and layers 1-4, run with the offloads as Docker
set them. Checked with tcpdump in the client container, a mimicry HTTP/2
page-load set under RTT 20 ms and 1% loss: with the offloads on, the
client's `eth0` received packets of 4.8 KB on average, up to 49 KB; with
them off, 1.4 KB on average and none over 1500 bytes (MSS 1448 plus
headers). Reports from before 2026-10-28 ran netem with them on. The
report's environment says they are off, and names any interface it could
not change.

**Ingress shaping lasts only as long as its condition.** The incoming half
of a condition is netem on `ifb0`, with `eth0`'s incoming packets
redirected to it; the redirect is set with each netem condition and taken
off with it, and `ifb0` is deleted when layer 5 (or layer 3 under
`-load-netem`) ends. Until 2026-10-29 the redirect stayed after its
condition, and `ifb0` without its netem queues 32 packets: the condition
without netem, and every layer after layer 5 in the same run, went through
that queue. In `bench-2026-10-29`, the first full run with layer 6, every
100 KB point of layer 6 ran at 115-522 requests/s, against 1,891-7,206 in
`multiclient-2026-10-28` (layer 6 alone), and 2.4-3.0% of mimicry's HTTP/3
requests with rotation went on HTTP/2; replayed, the queue dropped 4.6-4.7%
of the packets to the client during those points, and 0.02-0.07% of the
requests went on HTTP/2 without it.

## Many clients through proxies (layer 6)

The workload of a scraper that runs a client per proxy: `-multi-n` clients
(100) of one kind in one worker process, each its
own Transport or session built with its own credentials
(`bench-<run>-<client>-<identity>`, so no two can share a tunnel), each
sending GET /bytes/1024 or /bytes/102400 to nginx one request at a time,
for the window after the warm-up. Routes: HTTP/2 and HTTP/1.1 through
CONNECT with Basic auth, HTTP/2 and HTTP/3 through SOCKS5 (`socks5h://`
with username and password; HTTP/3 for the clients that carry it over a
UDP association: mimicry with `Config.HTTP3OverSOCKS5`, tls-client,
httpcloak and azuretls; net/http's quic-go transport has no proxy, n/a).
Each route and body runs with one identity per client (`fixed`); the
`proxy` variant gives each client a new one every `-multi-rotate` requests
(10) on the routes and bodies `-multi-proxy-routes` and
`-multi-proxy-bodies` name (`h2-connect-auth` and `1KB`; `all` for every
route or body), switched the way each library's users rotate
(`clients.Client.SetProxy`: a
`Proxy` function's URL for mimicry and net/http, `SetProxy` for the
others), so every switch costs a new tunnel and handshake. The `proxy`
variant measures over `-multi-proxy-window` (30 s): mimicry drops an old
identity's idle connections once it is outside the 16 most recent and
unused for 10 s, so a 3 s window only ever saw it growing. It samples the
process and the proxy every second, as client rotation does, and the
report gives where memory levels off (the last third's mean live heap,
goroutines and open tunnels per client) and the live heap's slope over the
window's second half. With `-multi-n2` (25; 0 for none) the 1 KB points of
the variants `-multi-n2-variants` names (`fixed`; `fixed,proxy` adds the
proxy rotation's) run again at that many clients,
and the report fits the live heap and goroutines in all against the client
count: per client (slope) and fixed (intercept), instead of the fixed part
divided by N. mimicry's HTTP/3 rows are marked ‡ in the memory tables: each
client also keeps the HTTP/2 connection it learnt Alt-Svc on, as layer 4's.

### Client rotation

Variant `client` is the other common pattern: a client per proxy identity,
replaced rather than moved. Each of the `-multi-n` routines sends one
request at a time and, every `-multi-rotate` requests, builds a new client
with the library's ordinary constructor and the next identity's
credentials, then lets the old one go in one of two modes:

- `dropped`: the old client is abandoned as it is, as code that makes a
  client per proxy and never closes it does;
- `closed`: the old client is closed first, with the adapter's `Close`:
  `Transport.CloseIdleConnections` for net/http and mimicry (which has no
  `Close`), `HttpClient.CloseIdleConnections` for tls-client (none
  either), `Session.Close` for httpcloak and azuretls.

It runs 1 KB only, and measures over `-multi-client-window` (10 s)
instead of `-window`, so that what old clients leave
behind has time to pile up. Before the load it measures a new client alone
(20 of them one at a time, each closed after its first response: medians
of the constructor's time, the first request's, and what both allocate).
Those are a cold, idle CPU's times (3-8× the constructor's under load), so
the report labels them so; the constructor's own cost is layer 2's
`BenchmarkNewClient`, a tight loop of constructor and `Close`.
During the window it records each new client's latency from the switch to
its first response, and every second the process's live heap, goroutines
and resident memory and the proxy's open tunnels and UDP associations for
the point's users (the proxy counts them per user; `/_proxy` reports
`openTunnels` and `openAssociations`). When the window closes the routines
stop and, before anything is closed, it reads what is left: live heap and
goroutines per routine (after GC), RSS, and the tunnels still open at the
proxy, plus the least-squares slope of each over the samples. Over HTTP/3,
mimicry's routines share one `Config.AltSvc` cache (a new client would
otherwise start on HTTP/2), and up to 1% of the responses may come over
another protocol, as a racing client's new connection can lose to TCP;
the report gives the share.

`-multi-variants` picks the variants (`fixed,proxy,client`). To compare
two builds of mimicry, run each into a report of its own and merge their
layer 6 points, side by side in every table:

```sh
BENCH_FAST=1 BENCH_NAME=rotation-before internal/bench/scripts/bench.sh report -layers 6 -micro "" \
  -multi-variants client -protos h2,h3 -multi-client-window 20s
# ... change mimicry, then the same with BENCH_NAME=rotation-after ...
docker compose -f internal/bench/docker/compose.client.yml run --rm client bench merge -out /reports \
  -name multiclient-rotation before=rotation-before-<date>.json after=rotation-after-<date>.json
```

### Checks and report

Every response's protocol is checked (`x-proto`), and after the window the
proxy's per-user counts are checked against each client: every identity
that got a response opened a tunnel or association, the proxy relayed the
client at least the body bytes it received, and over HTTP/3 the client's
datagrams went through a relay. A point that fails is discarded with the
reason. The report's tables give requests/s, p50/p99, CPU and allocations
per request, heap per client (the live heap at steady state and the peak),
goroutines and stack per client, the new-connection latency, handshakes/s
and the allocation each new identity costs, and errors with the proxy's and
the server's CPU use; client rotation has a section of its own (the new
client under load and alone, then what is left per routine and its growth:
live heap, goroutines, RSS, tunnels open at the proxy). The best
impersonating client is bolded (in a report merged from passes, only where
its range lies outside every other's), net/http is the reference. Layer 6
alone, in a report of its own (`reports/multiclient-<date>.md` and `.json`;
`-micro ""` keeps the day's micro results out of it):

```sh
BENCH_FAST=1 BENCH_NAME=multiclient internal/bench/scripts/bench.sh report -layers 6 -micro ""
```

## Running it locally

Needs Docker (OrbStack on the Mac) and moon. Both stacks run on this machine,
on separate CPU sets: client `0-3` (GOMAXPROCS 4), servers `4-11`
(GOMAXPROCS 8, shared by nginx and the Go server), proxy `10-11` (inside the
servers' set).

```sh
moon run bench:micro   # layers 1-2: reports/micro-<date>.txt and -benchstat.txt
moon run bench:run     # the default run, then reports/bench-<date>.md and .json
```

`bench:run` is `scripts/bench.sh run`: build the image, print the plan and
its estimated duration, run `setup`, restart the server stack (nginx, the
Go server and the proxy start fresh), wait for it, run the micro
benchmarks, then layers 3, 4 and 6 in passes (below), merged into one
report that embeds the benchstat summary ([Default run](#default-run)).
Neither task is part of
`:test` (the project excludes `test` and `test-full`); `:vet` and `:lint`
include the module, under the repo's `.golangci.yml` (a `depguard` rule
holds it to mimicry's public API).

**Power.** On macOS the script keeps the machine awake (`caffeinate -i`) and
refuses to measure (`micro`, `run`, `report`) unless `pmset -g batt` reports
AC power and `pmset -g` reports `lowpowermode 0`: the two 2026-10-29 runs were
on battery, and identical code spread 4-11% per point (log-SD) between
them. `BENCH_ALLOW_BATTERY=1` overrides it for smoke runs; the report's
Environment records the power state either way (`Power`).

**Passes.** Layers 3 and 6 run as `BENCH_PASSES` separate invocations of
`bench run` (3 for `run`, 1 for `report`), each in a new container and
process, the clients in another order each pass: as given (`BENCH_CLIENTS`,
the five clients by default), reversed, then rotated by half (and by one more for each
pass after). Layers 4 and 5 run in the first pass only. Each pass writes
`<name>-passN-<date>.json` and `.md`; `bench merge -passes` then writes
`<name>-<date>`: every layer 3 and 6 point is the median of each of its
values over the passes it was valid in (the lower middle one with an even
count), with each value's range (lowest–highest) beside it in the tables;
the summary's geomeans are over the medians; a best is bold only where its
range lies outside every other impersonating client's. A pass that finds a
point server-bound makes it server-bound in the merge. Give the clients
and the report's name as `BENCH_CLIENTS` and `BENCH_NAME`, which the passes
vary, not as `-clients` and `-name`.

**Stock UDP buffers and NET_ADMIN.** The quic-go clients (net/http's
baseline, tls-client, httpcloak, azuretls) ask for a 7 MB UDP receive
buffer; when `net.core.rmem_max` cuts it, they retry with `SO_RCVBUFFORCE`,
which succeeds for a process with `CAP_NET_ADMIN`. The client container has
`NET_ADMIN` in every normal pass (netem needs it), so there they get their
7 MB whatever `rmem_max` says; mimicry asks for none
(`Config.UDPReadBufferSize` 0) and gets `rmem_default` (224 KB here). On
stock Linux (`rmem_max` 212992, an unprivileged process) every client gets
about 208 KB. `BENCH_UDP_STOCK_PASSES` (0; 1 with `BENCH_FULL`) adds passes of layers 3
and 6 over HTTP/3 only with the client container's `rmem_max` and
`rmem_default` at 212992 and `NET_ADMIN` dropped
(`docker/compose.client.udpstock.yml`, layered over the client compose
file; both sysctls are per network namespace): setting the sysctls alone
changes nothing for the quic-go clients. Without `NET_ADMIN` there is no
netem, so the stock passes cannot include layer 5 or `-load-netem`
(`bench run` refuses them). The merged report ends with a section
comparing the two: the summary at stock buffers, and each HTTP/3 point's
req/s, CPU, p99 and receive-buffer drops, granted / stock.
`BENCH_UDP_STOCK=1 bench.sh report …` runs a whole report at stock values
(layers 3 and 6 by default). Every report records both sysctls and whether
the client had `NET_ADMIN`, and each HTTP/3 point's growth of the
client container's `RcvbufErrors` and `InErrors` (`/proc/net/snmp`) in its
JSON (`udp`) and its table (UDP). `-clients mimicry-udp7m` adds mimicry
asking for quic-go's 7 MB.

**Micro passes.** `micro` runs `BENCH_COUNT` (6) passes of the test binary
with `-count 1`, each a container of its own, the clients' order rotated
as above (`BENCH_CLIENTS` read by `bench_test.go`), appending to one raw
file; benchstat then summarises the lot, and `bench microstat` drops the
geomean rows benchstat flags as over different benchmark sets. Layer 2's
`handshake=resumed` tolerates up to 1% + 1 requests that made a full
handshake instead (the ticket race `settleWait` narrows), timed with the
rest and reported as the `unresumed` metric, instead of dropping the row.
`BenchmarkNewClient` times each client's constructor and `Close` in a
tight loop, with no request (`proxy=none` and `proxy=socks5`, credentials
included).

The script's commands and settings (`scripts/bench.sh` with no argument
lists them):

```sh
internal/bench/scripts/bench.sh up       # build, CA, start the server stack
internal/bench/scripts/bench.sh micro    # layers 1-2
internal/bench/scripts/bench.sh plan     # what run would measure and how long, measuring nothing
internal/bench/scripts/bench.sh report -window 10s -warmup 3s   # layers 3, 4 and 6 only
internal/bench/scripts/bench.sh render   # rewrite bench-<date>.md from its JSON, with the newest micro results
internal/bench/scripts/bench.sh down
```

Arguments after `run`, `plan` or `report` go to `bench run` (defaults: the
default run's): `-window` and `-warmup` per load point (at least 5 s and
2 s, stretched for long requests; layer 6's fixed and proxy variants use
them too), `-max-window 120s`, `-layers 3,4,6`, `-servers nginx` (or
`nginx,go`), `-reduced go` (servers that get bodies 1KB/100KB and
concurrency 1/64 only), `-protos h1,h2,h3` (layers 3, 5 and 6), `-conc
1,64`, `-bodies 1KB,100KB` (layer 3's sizes; `all` for 1KB, 100KB and
10MB), `-idle-n 10,50`, `-reps 10`, `-reps-loss 20`, `-netem=false`,
`-load-netem rtt80-loss1` (layer 3 under these netem conditions instead of
the network as it is, offloads off as in layer 5, the condition in each
point's heading), `-multi-n 100`, `-multi-n2 25`, `-multi-n2-variants
fixed`, `-multi-variants fixed,proxy,client`, `-multi-rotate 10`,
`-multi-proxy-routes h2-connect-auth` and `-multi-proxy-bodies 1KB` (`all`
for every route or body), `-multi-proxy-window 30s` and
`-multi-client-window 10s` (layer 6), `-profile <dir>`, `-plan` (what
`bench.sh plan` prints per pass). `BENCH_CLIENTS` also takes variants that
runs leave out
otherwise (`clients.Variants`): `mimicry-nocoalescing`
(`DisableCoalescing`, a connection per host where Chrome coalesces),
`mimicry-conns16` and `mimicry-conns64` (`MaxConnsPerHost`),
`mimicry-ack20` (`HTTP3ACKEvery` 20), `mimicry-udp7m`
(`UDPReadBufferSize` 7 MB), and `azuretls-stream` (azuretls with
`Request.IgnoreBody`, its body read as it arrives: the buffering client
measured streaming). Environment: `BENCH_PASSES`, `BENCH_UDP_STOCK_PASSES`,
`BENCH_UDP_STOCK`, `BENCH_CLIENTS`, `BENCH_NAME`, `BENCH_COUNT` (6),
`BENCH_BENCHTIME` (1s), `BENCH_CONNECT_BENCHTIME` (200x), `BENCH_MICRO`
(one `-bench` pattern), `BENCH_SERVERS` (micro: `nginx`), `BENCH_FULL`,
`BENCH_FAST`, the CPU sets
and `GOMAXPROCS` (`BENCH_CLIENT_CPUS`, `BENCH_SERVER_CPUS`,
`BENCH_PROXY_CPUS`, `BENCH_CLIENT_PROCS`, `BENCH_SERVER_PROCS`),
`BENCH_PROXY_DELAY` (5ms), `BENCH_ALLOW_BATTERY`, `BENCH_SKIP_BUILD`.

`BENCH_FAST=1` stays as a quick smoke mode, not for comparison (the
default run took over its old settings: 100 layer 6 clients, 25 for the
second count, a 10 s client-rotation window, no concurrency 16): one pass,
no stock-buffer pass, 2 micro passes of 300 ms (Connect 50x), load points
a 3 s window over a 1 s warm-up (both still stretched for long requests),
10 and 20 idle clients, 3 page loads per condition (5 under loss); about
27 min by its plan. A flag given explicitly wins over its preset. The
report records the settings it used, so a smoke run can't be mistaken for
a full one. `BENCH_FULL` and `BENCH_FAST` exclude each other.
`BENCH_SKIP_BUILD=1` reuses the images a previous
run built (the servers are unaffected by client changes; the client image
is not, so skip it only when that code is unchanged too).

### Default run

On AC power, Low Power Mode off, other apps closed:

```sh
internal/bench/scripts/bench.sh run
```

It measures the workload mimicry is meant for: many clients, each through
a proxy identity of its own, sending small requests. So:

- **Micro (layers 1-2):** 6 passes, nginx only.
- **Layer 3:** nginx; bodies 1 KB and 100 KB (an API response, a page);
  concurrency 1 (one client's latency and CPU per request) and 64 (many
  requests at once); HTTP/1.1, HTTP/2, HTTP/3; the five clients (net/http,
  mimicry, tls-client, httpcloak, azuretls). 60 points a pass.
- **Layer 4:** idle memory at 10 and 50 clients (two counts, for memory per
  connection as a slope), first pass only.
- **Layer 6:** 100 clients per point. One fixed identity per client on
  every route at 1 KB and 100 KB, and client rotation (`dropped` and
  `closed`) on every route at 1 KB: the workload itself. Proxy rotation on
  one route only, HTTP/2 through CONNECT at 1 KB, over 30 s: long enough
  for mimicry's cleanup of old identities (idle for 10 s) to show. The fixed
  1 KB points again at 25 clients, for memory per client as a slope and an
  intercept.
- **3 passes** of layers 3 and 6, the clients' order rotated, merged
  (medians and ranges).

The windows are short on purpose: 5 s over a 2 s warm-up for load and
fixed-identity points, 10 s for client rotation (30 s for proxy rotation,
as above). At these body sizes even the slowest point (100 KB, one at a
time) holds over ten thousand requests in 5 s, and client rotation over
4,000 new clients in 10 s (the 2026-10-29 run's rates); the stretch rule
still lengthens a window for a point that needs it. `BENCH_FULL=1`, or
`-window 10s -warmup 3s -multi-client-window 20s`, restores the long ones.

With its defaults spelled out:

```sh
BENCH_PASSES=3 BENCH_UDP_STOCK_PASSES=0 BENCH_COUNT=6 BENCH_SERVERS=nginx internal/bench/scripts/bench.sh run \
  -layers 3,4,6 -servers nginx -bodies 1KB,100KB -conc 1,64 -protos h1,h2,h3 -window 5s -warmup 2s \
  -idle-n 10,50 -multi-n 100 -multi-n2 25 -multi-n2-variants fixed -multi-variants fixed,proxy,client \
  -multi-rotate 10 -multi-proxy-routes h2-connect-auth -multi-proxy-bodies 1KB -multi-proxy-window 30s \
  -multi-client-window 10s
```

`run` prints the plan before it measures (`bench.sh plan` prints it alone):
pass 1's jobs by layer and group, the other passes' totals, micro's, and an
estimated duration, here about 1 h 40 min: setup ~3 min, micro ~16 min (6
passes), pass 1 ~28 min (layer 3 ~7.5 min, layer 4 ~2 min, layer 6 ~19
min), passes 2 and 3 ~26 min each. The estimate is per-job arithmetic,
not measured end to end. `bench run -plan` (`cmd/bench/plan.go`) walks the
run's own loops, counting each job instead of running it: a load point is
its warm-up and window, stretched as `scenario.LoadSpec` would for an
assumed 400 MB/s; a layer 6 point its warm-up and window, plus 1.5 s for
client rotation's new clients alone; a page-load set its loads at the
condition's RTT and loss; each worker 0.5 s more, and a job the client
cannot run 0.2 s. Those constants come from the job timestamps in a
2026-10-29 run's log. Micro (`micro_seconds` in `scripts/bench.sh`) is its
rows times their `-benchtime` plus a fifth and 0.1 s, Connect rows 11 ms per
iteration plus 0.3 s, and 5 s per pass (the 2026-10-27 micro log took about
1.3 s per 1 s row). Setup counts as 3 min and each container 15 s. The
report is `reports/bench-<date>.md` and `.json`, with each pass's own
beside it.

### Deep checks

Left out of the default run, kept in the code, one switch away each.
`BENCH_FULL=1` restores them all at once, the full matrix of the runs
before the trim (about 8 h 20 min by its plan): every layer, both servers,
every body, concurrency 1/16/64/256, 10 s windows over 3 s warm-ups, 200
and 50 clients per layer 6 point, proxy rotation on every route and body
(its 1 KB points at the second count too), a 20 s client-rotation window, the stock-buffer pass, 10 micro
passes on both servers. A flag given with it wins over its preset. One at
a time, with `run` or `report` (`plan` with the same arguments shows the
cost):

| Check | What it shows | How |
|---|---|---|
| 10 MB bodies | bulk throughput, the stretched windows, buffering clients' allocations | `-bodies all` |
| Concurrency 16 and 256 | mimicry's 6-per-host HTTP/1.1 queue, contention at high concurrency | `-conc 1,16,64,256` |
| The Go server | net/http and quic-go's server instead of nginx (layer 3 gives it the reduced matrix, `-reduced`) | `-servers nginx,go`; `BENCH_SERVERS=nginx,go` for micro |
| Layer 5 page loads | page loads under netem and through the proxies (first pass only) | `-layers 3,4,5,6` |
| Stock UDP buffers | HTTP/3 at stock Linux's 208 KB receive buffer, without `NET_ADMIN` | `BENCH_UDP_STOCK_PASSES=1` (run), `BENCH_UDP_STOCK=1` (report) |
| Proxy rotation everywhere | the proxy variant on every route and at 100 KB | `-multi-proxy-routes all -multi-proxy-bodies all` |
| Proxy rotation's memory fit | its 1 KB points again at `-multi-n2` | `-multi-n2-variants fixed,proxy` |
| 200 clients per layer 6 point | twice the clients, the second count 50 | `-multi-n 200 -multi-n2 50` |
| Long windows | 10 s over 3 s, client rotation 20 s | `-window 10s -warmup 3s -multi-client-window 20s` |
| 10 micro passes | narrower micro ranges | `BENCH_COUNT=10` |
| The variants | `mimicry-ack20` (HTTP/3 ACK every 20th packet), `mimicry-udp7m` (7 MB UDP buffer; with the stock pass), `mimicry-nocoalescing` (layer 5's HTTP/2 page loads), `mimicry-conns16`/`-conns64` (HTTP/1.1 at concurrency above 6), `azuretls-stream` (azuretls reading as the body arrives) | `BENCH_CLIENTS=net/http,mimicry,mimicry-ack20,…` |

The variants were never in a run's default clients; they stay opt-in.

## Running on a Linux host

Both stacks can run on a Linux Docker host instead of the Mac (a Linux
server: Docker directly on the host, so the CPU sets are real cores). Copy
the repository there and run `bench.sh` on it; it needs bash, Docker with
Compose, and no Go (the images build Go themselves). Without `.git` give
the commit:

```sh
BENCH_MIMICRY_COMMIT=153cf05 BENCH_PUBLISH=0 \
  BENCH_CLIENT_CPUS=0,2,4,6 BENCH_SERVER_CPUS=8-11 BENCH_SERVER_PROCS=4 BENCH_PROXY_CPUS=12-15 \
  nohup internal/bench/scripts/bench.sh run > bench.log 2>&1 &
```

On the i5-13500 the client gets one thread of each of four P-cores (CPUs
0, 2, 4 and 6; their SMT siblings 1, 3, 5 and 7 left idle), nginx the next
two P-cores' four threads (8-11), the proxy four E-cores (12-15). Only the
benchmark's containers are pinned: the host's other VMs and containers
float over every CPU, which shows up as noise. `BENCH_PUBLISH=0` publishes
no port (the client reaches the servers on the `mimicry-bench` network), so
the host's own services keep theirs. `pmset` and `caffeinate` are macOS
only. Without netem in the host's kernel (that host's kernel has neither `sch_netem` nor
`ifb`) the proxy's delay is the in-process one (Proxy delay, above), the
offloads sidecar does not start, and a run asking for netem (layer 5,
`-load-netem`) stops at once; the default run (layers 3, 4, 6) needs none.
Before each pass and at the end the script records the load average and
every other container at 1% CPU or more (`docker stats`), in the pass's
Environment and in `reports/host-<date>.txt`.

## Running against a remote server

The server stack can run on another Docker host (a Linux server, the Mac
mini) with the client here, over real Ethernet. One setting points the client
at it, `BENCH_SERVER=<its IP>`, plus how to reach its Docker daemon:

```sh
docker context create bench-server --docker host=ssh://user@server   # once
BENCH_SERVER=<server address> BENCH_SERVER_CONTEXT=bench-server \
  BENCH_SERVER_CPUS=0-7 BENCH_SERVER_PROCS=8 BENCH_PROXY_CPUS=8-9 \
  internal/bench/scripts/bench.sh run -netem=false
```

With `BENCH_SERVER` set, `up` builds and starts the server stack through that
context (on the remote host's own architecture), publishes its ports on every
address (`BENCH_PUBLISH_ADDR=0.0.0.0`: 8443-8444 TCP and 8443 UDP,
9443-9444 TCP and 9443 UDP, 8080, 9080, 3128, 3129, 1080, 9081; open them in
the host's firewall), creates the `mimicry-bench` network and CA volume here,
and copies the CA's certificate across (`bench.sh ca-sync`: only `ca.pem`;
the keys stay on the server host). The client's `extra_hosts` then map every
name to `BENCH_SERVER`. Pick server CPU sets that exist there. netem is
optional on a real network: `-netem=false` runs layer 5 on the network as it
is (and the proxy scenario there); leave it on to add delay and loss on top.
`DOCKER_HOST=ssh://…` works in place of a context. `bench.sh down` with the
same settings stops the remote stack.

## Profiling mimicry

`bench run -profile /reports/profiles` writes a CPU profile of each of
mimicry's measurement windows, and mutex, block and allocation profiles at
its end (`load-nginx-h2-bytes-1KB-c64.cpu.pprof`, `.mutex.pprof`,
`.block.pprof`, `.mem.pprof`, …). Mutex and block profiling are sampled
(one contention in 10, blocking events from about 10 µs) and on only during
the window. The other clients' jobs are not profiled. For layers 1-2, the
test binary's own flags on one client:

```sh
docker compose -f internal/bench/docker/compose.client.yml run --rm client \
  bench.test -test.run '^$' -test.bench 'Request/server=nginx/proto=h2/.*/client=mimicry' \
  -test.cpuprofile cpu.pprof -test.memprofile mem.pprof
```

## Adding a client

Write an adapter in `clients/` (configure the newest Chrome profile, the CA
pool, the protocol switches it has; report the response's protocol and
whether its TLS session was resumed from the servers' `x-proto` and
`x-tls-resumed` headers), add it to `clients.All`, pin its newest version in
`go.mod`, add it to the `internal/bench` depguard rule and to
`THIRD_PARTY_NOTICES.md`, and describe anything unequal about it in the
report's fairness notes (`cmd/bench/report.go`).
