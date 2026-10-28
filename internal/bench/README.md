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
| 5 | Realistic network: a page load (one HTML + 30 subresources over www, static and api) under `tc netem` (RTT 20 and 80 ms, loss 0 and 1%), cold and warm; the same through a CONNECT proxy (with and without Basic auth) and SOCKS5, the proxy with its own delay | `cmd/bench` |

Every measured response is checked against the protocol under test as the
server saw it (`x-proto`); a measurement with one response on another
protocol is discarded with the reason. A client that lacks something is
n/a, never faked. Each layer 3-5 measurement runs in a process of its own
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
cmd/bench/          layers 3-5: `bench run` (orchestrator), `bench worker`, `bench wait`
cmd/server/         the Go server (net/http + quic-go http3)
cmd/proxy/          CONNECT (with/without Basic auth) and SOCKS5 proxy
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

Proxy: CONNECT on 3128, CONNECT with Basic auth (`bench:bench-secret`) on
3129, SOCKS5 on 1080, stats on 9081. A `setup` step writes a CA (created
once, kept in the `mimicry-bench-ca` volume) and one certificate for every
name; every client verifies against that CA.

**Client** (`docker/compose.client.yml`): one container with every client,
its own CPU set, a fixed `GOMAXPROCS`, `NET_ADMIN` for netem, the CA volume
read-only, and `extra_hosts` mapping every name to the server stack's fixed
addresses, or to `BENCH_SERVER`.

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
internal/bench/scripts/bench.sh report -window 5s -warmup 2s   # layers 3-5 only
internal/bench/scripts/bench.sh render   # rewrite bench-<date>.md from its JSON, with the newest micro results
internal/bench/scripts/bench.sh down
```

Arguments after `run` or `report` go to `bench run`: `-window` and `-warmup`
per load point (10 s and 3 s), `-layers 3,4,5`, `-clients`, `-servers
nginx,go`, `-reduced go` (servers that get bodies 1KB/100KB and concurrency
1/64 only), `-protos h1,h2,h3` (layer 3), `-conc 1,16,64,256`, `-idle-n 50`,
`-reps 5`, `-netem=false`, `-profile <dir>`. Each HTTP/3 load point's log
line also gives how much the client container's UDP receive errors grew
during it (`/proc/net/snmp`: `RcvbufErrors`, datagrams dropped on a full
socket buffer, and `InErrors`). Environment: `BENCH_COUNT` (10), `BENCH_BENCHTIME` (1s),
`BENCH_CONNECT_BENCHTIME` (200x), `BENCH_MICRO` (one `-bench` pattern),
`BENCH_SERVERS` (micro: `nginx,go`), the CPU sets and `GOMAXPROCS`
(`BENCH_CLIENT_CPUS`, `BENCH_SERVER_CPUS`, `BENCH_PROXY_CPUS`,
`BENCH_CLIENT_PROCS`, `BENCH_SERVER_PROCS`), `BENCH_PROXY_DELAY` (5ms).

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
