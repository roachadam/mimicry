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
| 1 | One request on a warm connection: ns/op, B/op, allocs/op; GET /json and POST /echo (small JSON); HTTP/1.1, HTTP/2, HTTP/3 | `bench_test.go` `BenchmarkRequest`, `-count=10`, benchstat |
| 2 | One request on a new connection: full handshake (a new client each time) and resumed (connections closed, session cache kept) | `bench_test.go` `BenchmarkConnect` |
| 3 | Throughput and latency: requests/s, p50/p95/p99, MB/s, client CPU per request, allocations, GC, peak heap, at concurrency 1/16/64/256, bodies 1 KB/100 KB/10 MB, fixed window after a warm-up | `cmd/bench` |
| 4 | Memory: heap, goroutines and goroutine stacks (`StackInuse`) per client and per idle connection; GC under load is in layer 3 | `cmd/bench` |
| 5 | Realistic network: a page load (one HTML + 30 subresources over www, static and api) under `tc netem` (RTT 20 and 80 ms, loss 0 and 1%), cold and warm; over HTTP/3, mimicry also as a returning browser (new Transports sharing an Alt-Svc cache an earlier visit filled); the same through a CONNECT proxy (with and without Basic auth) and SOCKS5, the proxy with its own delay | `cmd/bench` |
| 6 | Many clients, one proxy identity each: N clients (200) in one process, each its own Transport or session with its own proxy credentials, each sending GET 1 KB or 100 KB to nginx one request at a time; HTTP/2 and HTTP/1.1 through CONNECT with Basic auth, HTTP/2 and HTTP/3 through SOCKS5 (socks5h, username and password). Requests/s, p50/p99, CPU and allocations per request, heap per client at steady state and peak, goroutines and stack per client, errors; a rotation variant (a new identity every K requests, 10) adds new-connection latency, handshakes/s and the cost per new identity | `cmd/bench` |

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
cmd/bench/          layers 3-6: `bench run` (orchestrator), `bench worker`, `bench wait`; `bench offloads` (the sidecar)
cmd/server/         the Go server (net/http + quic-go http3)
cmd/proxy/          CONNECT (with/without Basic auth) and SOCKS5 (CONNECT, UDP ASSOCIATE) proxy, counting per user
cmd/setup/          the CA (once), the server certificate, nginx's files
docker/             Dockerfile, compose.server.yml, compose.client.yml, nginx config
scripts/bench.sh    drives both stacks; the moon tasks call it
reports/            bench-<date>.md/.json, micro-<date>.txt and -benchstat.txt
```

Two compose stacks. **Server** (`docker/compose.server.yml`): nginx 1.31
(official image, built with `http_v3_module`), the Go server and the proxy,
each with fixed addresses on the `mimicry-bench` network and Docker aliases
`{www,static,api}.nginx.bench.test`, `{www,static,api}.go.bench.test` and
`proxy.bench.test`; the servers on one CPU set, the proxy on another. Both
servers serve the same endpoints with the same bytes and headers:

| Port | nginx | Go | |
|---|---|---|---|
| TLS h2 + h1, UDP h3, `Alt-Svc` | 8443 | 9443 | `/bytes/{n}`, `/json`, `POST /echo`, `/page/…` |
| TLS http/1.1 only, no `Alt-Svc` | 8444 | 9444 | the same |
| plain HTTP | 8080 | 9080 | `/_stats`: CPU time, CPUs, connections (Go) |

Proxy: CONNECT on 3128, CONNECT with Basic auth on 3129, SOCKS5 on 1080
(username and password when the client offers them, else none; CONNECT
and UDP ASSOCIATE), stats on 9081. It accepts the user `bench` and every
`bench-<anything>`, with the password `bench-secret`, and counts per user
what it relays (tunnels and UDP associations opened and still open,
datagrams, bytes each way: `/_proxy?prefix=…` on the stats port), which
layer 6 checks its clients
against. A UDP association gets a relay socket on the address the client
reached the proxy at, ephemeral and not published: HTTP/3 through SOCKS5
needs the client on the proxy's Docker network, so with `BENCH_SERVER` its
layer 6 rows are n/a. A `setup` step writes a CA (created
once, kept in the `mimicry-bench-ca` volume) and one certificate for every
name; every client verifies against that CA.

**Client** (`docker/compose.client.yml`): one container with every client,
its own CPU set, a fixed `GOMAXPROCS`, `NET_ADMIN` for netem, the CA volume
read-only, and `extra_hosts` mapping every name to the server stack's fixed
addresses, or to `BENCH_SERVER`. Beside it, the `offloads` sidecar on the
Docker host's own network (`NET_ADMIN`, port 9082).

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

## Many clients through proxies (layer 6)

The workload of a scraper that runs a client per proxy: `-multi-n` clients
(200; 100 with `BENCH_FAST`) of one kind in one worker process, each its
own Transport or session built with its own credentials
(`bench-<run>-<client>-<identity>`, so no two can share a tunnel), each
sending GET /bytes/1024 or /bytes/102400 to nginx one request at a time,
for the window after the warm-up. Routes: HTTP/2 and HTTP/1.1 through
CONNECT with Basic auth, HTTP/2 and HTTP/3 through SOCKS5 (`socks5h://`
with username and password; HTTP/3 for the clients that carry it over a
UDP association: mimicry with `Config.HTTP3OverSOCKS5`, tls-client,
httpcloak and azuretls; net/http's quic-go transport has no proxy, n/a).
Each route and body runs in two variants: one identity per client (`fixed`),
then a new one every `-multi-rotate` requests (10; `proxy`),
switched the way each library's users rotate (`clients.Client.SetProxy`: a
`Proxy` function's URL for mimicry and net/http, `SetProxy` for the
others), so every switch costs a new tunnel and handshake.

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

It runs 1 KB only, and measures over `-multi-client-window` (20 s; 10 s
with `BENCH_FAST`) instead of `-window`, so that what old clients leave
behind has time to pile up. Before the load it measures a new client alone
(20 of them one at a time, each closed after its first response: medians
of the constructor's time, the first request's, and what both allocate).
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
BENCH_FAST=1 internal/bench/scripts/bench.sh report -layers 6 -name rotation-before -micro "" \
  -multi-variants client -protos h2,h3 -multi-client-window 20s
# ... change mimicry, then the same with -name rotation-after ...
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
impersonating client is bolded, net/http is the reference. Layer 6 alone,
in a report of its own (`reports/multiclient-<date>.md` and `.json`;
`-micro ""` keeps the day's micro results out of it):

```sh
BENCH_FAST=1 internal/bench/scripts/bench.sh report -layers 6 -name multiclient -micro ""
```

## Running it locally

Needs Docker (OrbStack on the Mac) and moon. Both stacks run on this machine,
on separate CPU sets: client `0-3` (GOMAXPROCS 4), servers `4-11`
(GOMAXPROCS 8), proxy `10-11`.

```sh
moon run bench:micro   # layers 1-2: reports/micro-<date>.txt and -benchstat.txt
moon run bench:run     # every layer, then reports/bench-<date>.md and .json
```

`bench:run` is `scripts/bench.sh run`: build the image, run `setup`, start the
server stack, wait for it, run the micro benchmarks, then `bench run`, which
embeds the benchstat summary in the report. On macOS the script keeps the
machine awake (`caffeinate -i`); run it on AC power. Neither task is part of
`:test` (the project excludes `test` and `test-full`); `:vet` and `:lint`
include the module, under the repo's `.golangci.yml` (a `depguard` rule
holds it to mimicry's public API).

The script's commands and settings (`scripts/bench.sh` with no argument
lists them):

```sh
internal/bench/scripts/bench.sh up       # build, CA, start the server stack
internal/bench/scripts/bench.sh micro    # layers 1-2
internal/bench/scripts/bench.sh report -window 5s -warmup 2s   # layers 3-6 only
internal/bench/scripts/bench.sh render   # rewrite bench-<date>.md from its JSON, with the newest micro results
internal/bench/scripts/bench.sh down
```

Arguments after `run` or `report` go to `bench run`: `-window` and `-warmup`
per load point (10 s and 3 s; layer 6 uses them too), `-layers 3,4,5,6`, `-clients`, `-servers
nginx,go`, `-reduced go` (servers that get bodies 1KB/100KB and concurrency
1/64 only), `-protos h1,h2,h3` (layers 3, 5 and 6), `-conc 1,16,64,256`, `-idle-n 50`,
`-reps 5`, `-netem=false`, `-multi-n 200`, `-multi-variants fixed,proxy,client`,
`-multi-rotate 10` and `-multi-client-window 20s` (layer 6), `-name bench` (the report's file name
before the date), `-profile <dir>`. `-clients` also takes mimicry
variants that runs leave out otherwise, each with one `Config` option set
(`clients.Variants`): `mimicry-nocoalescing` (`DisableCoalescing`, a
connection per host where Chrome coalesces), `mimicry-conns16` and
`mimicry-conns64` (`MaxConnsPerHost`). Each HTTP/3 load point's (and
layer 6 point's) log
line also gives how much the client container's UDP receive errors grew
during it (`/proc/net/snmp`: `RcvbufErrors`, datagrams dropped on a full
socket buffer, and `InErrors`). Environment: `BENCH_COUNT` (10), `BENCH_BENCHTIME` (1s),
`BENCH_CONNECT_BENCHTIME` (200x), `BENCH_MICRO` (one `-bench` pattern),
`BENCH_SERVERS` (micro: `nginx,go`), the CPU sets and `GOMAXPROCS`
(`BENCH_CLIENT_CPUS`, `BENCH_SERVER_CPUS`, `BENCH_PROXY_CPUS`,
`BENCH_CLIENT_PROCS`, `BENCH_SERVER_PROCS`), `BENCH_PROXY_DELAY` (5ms).

`BENCH_FAST=1` runs the whole thing in about half the time: micro benchmarks
4 runs of 300 ms, load points a 3 s window over a 1 s warm-up, concurrency
1/64/256, 20 idle clients, 3 page loads per condition, 100 clients per
layer 6 point. Run-to-run spread at
3 s measured the same as at 5 s (2–3%). The report records the settings it
used, so a fast run can't be mistaken for a full one. `BENCH_SKIP_BUILD=1`
reuses the images a previous run built (the servers are unaffected by client
changes; the client image is not, so skip it only when that code is
unchanged too):

```sh
BENCH_FAST=1 internal/bench/scripts/bench.sh run
```

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
