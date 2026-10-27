# mimicry benchmark after the performance fixes, 2026-10-27

A focused rerun after commit 9cc4fac (HTTP/3 stream leak fixed: `finish` closes the read side; HTTP/2 response chunks and request-body buffers pooled; about half the per-request allocations removed), against the first report, [`bench-2026-10-27.md`](bench-2026-10-27.md) (commit 3f2bb93 plus the benchmark, before the fixes).

Re-measured: mimicry and net/http only. Layer 1 (`BenchmarkRequest`, both servers, every protocol, GET and POST, `-count=10`, `-benchtime 1s`); layer 3 (the same load points as the first report: nginx full matrix, the Go server reduced to 1 KB/100 KB at concurrency 1 and 64; 2 s warm-up, 5 s window); layer 4 (idle memory, 50 clients, nginx). tls-client, httpcloak and azuretls are carried over from the first report: their code did not change. Same Mac, same CPU sets (client 0-3, GOMAXPROCS 4; servers 4-11), same image apart from the library. Raw data: `after-fixes/bench-2026-10-27-after-fixes.json`, `micro-2026-10-27-after-fixes.txt`.

net/http was measured again in both runs and is the drift check: if its numbers moved, the machine did, and the carried-over clients carry that much uncertainty. ‡ marks rows where mimicry is slower because it keeps Chrome's limit (6 HTTP/1.1 connections per host; see the first report's fairness notes).

## Drift check: net/http, first run → this run

| metric | layer 3 geomean (points) |
|---|---|
| requests/s | 0.99× (48) |
| p99 | 0.98× (48) |
| CPU/request | 1.03× (48) |
| allocs/request | 1.01× (48) |

Layer 1 net/http ns/op, new/old geomean: 0.96× over 12 benchmarks.

## Layer 1: one request on a warm connection, mimicry before → after

Medians of 10 runs. `nethttp` for scale.

| server | proto | method | ns/op before → after | Δ | allocs/op before → after | B/op before → after | net/http now: ns/op, allocs, B |
|---|---|---|---|---:|---|---|---|
| nginx | h1 | GET | 68.8µs → 62.4µs | -9% | 180 → 62 | 14.2K → 7.5K | 57.3µs, 59, 4.2K |
| nginx | h1 | POST | 66.3µs → 62.2µs | -6% | 205 → 76 | 16.3K → 8.9K | 62.7µs, 68, 5.3K |
| nginx | h2 | GET | 79.4µs → 69.0µs | -13% | 172 → 91 | 13.5K → 8.5K | 61.2µs, 56, 5.9K |
| nginx | h2 | POST | 91.0µs → 72.7µs | -20% | 196 → 106 | 79.5K → 10.1K | 68.7µs, 66, 7.1K |
| nginx | h3 | GET | 83.9µs → 82.6µs | -2% | 192 → 111 | 23.6K → 18.7K | 68.9µs, 122, 7.9K |
| nginx | h3 | POST | 96.5µs → 87.6µs | -9% | 212 → 124 | 25.7K → 20.4K | 85.6µs, 131, 16.9K |
| go | h1 | GET | 74.0µs → 59.1µs | -20% | 174 → 56 | 13.4K → 6.9K | 59.9µs, 53, 3.7K |
| go | h1 | POST | 77.2µs → 60.3µs | -22% | 201 → 72 | 15.5K → 8.4K | 60.4µs, 64, 4.9K |
| go | h2 | GET | 87.8µs → 74.7µs | -15% | 154 → 73 | 12.9K → 8.1K | 71.4µs, 39, 5.2K |
| go | h2 | POST | 111.5µs → 83.5µs | -25% | 182 → 92 | 79.0K → 9.8K | 78.1µs, 51, 6.5K |
| go | h3 | GET | 112.2µs → 102.2µs | -9% | 176 → 95 | 23.1K → 18.5K | 79.6µs, 104, 6.4K |
| go | h3 | POST | 121.9µs → 109.9µs | -10% | 201 → 113 | 25.4K → 20.3K | 83.0µs, 116, 15.4K |

benchstat, old (first report) against new, mimicry and nethttp rows:

```
goos: linux
goarch: arm64
pkg: github.com/roachadam/mimicry/internal/bench
                                                           │      old      │                 new                  │
                                                           │    sec/op     │    sec/op     vs base                │
nginx h1 GET nethttp-4     61.71µ ±  5%   57.28µ ±  4%   -7.18% (p=0.002 n=10)
nginx h1 GET mimicry-4     68.82µ ± 12%   62.36µ ± 12%        ~ (p=0.190 n=10)
nginx h1 POST nethttp-4    60.22µ ±  5%   62.65µ ±  8%        ~ (p=0.063 n=10)
nginx h1 POST mimicry-4    66.32µ ± 14%   62.21µ ±  5%        ~ (p=0.052 n=10)
nginx h2 GET nethttp-4     65.90µ ±  2%   61.17µ ±  8%   -7.18% (p=0.009 n=10)
nginx h2 GET mimicry-4     79.35µ ±  3%   68.96µ ±  6%  -13.10% (p=0.000 n=10)
nginx h2 POST nethttp-4    64.47µ ±  3%   68.71µ ±  3%   +6.57% (p=0.000 n=10)
nginx h2 POST mimicry-4    91.04µ ±  2%   72.71µ ±  6%  -20.13% (p=0.000 n=10)
nginx h3 GET nethttp-4     78.53µ ±  3%   68.94µ ±  5%  -12.22% (p=0.000 n=10)
nginx h3 GET mimicry-4     83.91µ ±  3%   82.58µ ±  4%        ~ (p=0.481 n=10)
nginx h3 POST nethttp-4    88.23µ ±  5%   85.57µ ±  6%        ~ (p=0.143 n=10)
nginx h3 POST mimicry-4    96.47µ ±  6%   87.63µ ± 11%        ~ (p=0.063 n=10)
go h1 GET nethttp-4        63.23µ ±  5%   59.92µ ±  3%   -5.24% (p=0.005 n=10)
go h1 GET mimicry-4        74.02µ ±  3%   59.14µ ±  3%  -20.10% (p=0.000 n=10)
go h1 POST nethttp-4       65.31µ ±  3%   60.36µ ±  4%   -7.58% (p=0.000 n=10)
go h1 POST mimicry-4       77.21µ ±  3%   60.33µ ±  5%  -21.86% (p=0.000 n=10)
go h2 GET nethttp-4        76.62µ ±  4%   71.43µ ±  1%   -6.78% (p=0.000 n=10)
go h2 GET mimicry-4        87.81µ ±  5%   74.65µ ±  1%  -14.98% (p=0.000 n=10)
go h2 POST nethttp-4       81.58µ ± 11%   78.06µ ± 14%   -4.31% (p=0.029 n=10)
go h2 POST mimicry-4      111.46µ ±  4%   83.45µ ±  6%  -25.13% (p=0.000 n=10)
go h3 GET nethttp-4        80.39µ ±  2%   79.65µ ±  4%        ~ (p=0.481 n=10)
go h3 GET mimicry-4        112.2µ ±  7%   102.2µ ±  6%   -8.95% (p=0.002 n=10)
go h3 POST nethttp-4       88.35µ ±  7%   83.02µ ±  2%   -6.03% (p=0.003 n=10)
go h3 POST mimicry-4       121.9µ ±  5%   109.9µ ± 15%   -9.90% (p=0.029 n=10)
geomean                                                       79.53µ         72.30µ         -9.08%

                                                           │      old      │                 new                  │
                                                           │     B/op      │     B/op      vs base                │
nginx h1 GET nethttp-4     4.168Ki ± 0%   4.168Ki ± 0%        ~ (p=0.786 n=10)
nginx h1 GET mimicry-4    14.231Ki ± 0%   7.469Ki ± 0%  -47.51% (p=0.000 n=10)
nginx h1 POST nethttp-4    5.300Ki ± 0%   5.300Ki ± 0%        ~ (p=0.752 n=10)
nginx h1 POST mimicry-4   16.263Ki ± 0%   8.916Ki ± 0%  -45.18% (p=0.000 n=10)
nginx h2 GET nethttp-4     5.848Ki ± 0%   5.850Ki ± 0%        ~ (p=0.089 n=10)
nginx h2 GET mimicry-4    13.459Ki ± 0%   8.456Ki ± 0%  -37.18% (p=0.000 n=10)
nginx h2 POST nethttp-4    7.084Ki ± 0%   7.084Ki ± 0%        ~ (p=0.378 n=10)
nginx h2 POST mimicry-4    79.54Ki ± 0%   10.13Ki ± 0%  -87.26% (p=0.000 n=10)
nginx h3 GET nethttp-4     7.924Ki ± 0%   7.934Ki ± 0%   +0.13% (p=0.014 n=10)
nginx h3 GET mimicry-4     23.62Ki ± 0%   18.72Ki ± 0%  -20.75% (p=0.000 n=10)
nginx h3 POST nethttp-4    16.81Ki ± 0%   16.86Ki ± 0%   +0.27% (p=0.000 n=10)
nginx h3 POST mimicry-4    25.74Ki ± 0%   20.38Ki ± 0%  -20.82% (p=0.000 n=10)
go h1 GET nethttp-4        3.655Ki ± 0%   3.651Ki ± 0%   -0.11% (p=0.000 n=10)
go h1 GET mimicry-4       13.420Ki ± 0%   6.921Ki ± 0%  -48.43% (p=0.000 n=10)
go h1 POST nethttp-4       4.857Ki ± 0%   4.855Ki ± 0%        ~ (p=0.923 n=10)
go h1 POST mimicry-4      15.516Ki ± 0%   8.415Ki ± 0%  -45.77% (p=0.000 n=10)
go h2 GET nethttp-4        5.181Ki ± 0%   5.177Ki ± 0%   -0.08% (p=0.000 n=10)
go h2 GET mimicry-4       12.905Ki ± 0%   8.132Ki ± 0%  -36.99% (p=0.000 n=10)
go h2 POST nethttp-4       6.480Ki ± 0%   6.476Ki ± 0%   -0.08% (p=0.003 n=10)
go h2 POST mimicry-4      79.031Ki ± 0%   9.849Ki ± 0%  -87.54% (p=0.000 n=10)
go h3 GET nethttp-4        6.399Ki ± 0%   6.400Ki ± 0%        ~ (p=0.250 n=10)
go h3 GET mimicry-4        23.14Ki ± 0%   18.46Ki ± 0%  -20.25% (p=0.000 n=10)
go h3 POST nethttp-4       15.36Ki ± 0%   15.37Ki ± 0%   +0.10% (p=0.000 n=10)
go h3 POST mimicry-4       25.39Ki ± 0%   20.26Ki ± 0%  -20.21% (p=0.000 n=10)
geomean                                                       12.26Ki        8.605Ki       -29.84%

                                                           │     old     │                 new                  │
                                                           │  allocs/op  │ allocs/op   vs base                  │
nginx h1 GET nethttp-4     59.00 ± 0%   59.00 ± 0%        ~ (p=1.000 n=10) ¹
nginx h1 GET mimicry-4    180.00 ± 0%   62.00 ± 0%  -65.56% (p=0.000 n=10)
nginx h1 POST nethttp-4    68.00 ± 0%   68.00 ± 0%        ~ (p=1.000 n=10) ¹
nginx h1 POST mimicry-4   205.00 ± 0%   76.00 ± 0%  -62.93% (p=0.000 n=10)
nginx h2 GET nethttp-4     56.00 ± 0%   56.00 ± 0%        ~ (p=1.000 n=10) ¹
nginx h2 GET mimicry-4    172.00 ± 0%   91.00 ± 0%  -47.09% (p=0.000 n=10)
nginx h2 POST nethttp-4    66.00 ± 0%   66.00 ± 0%        ~ (p=1.000 n=10) ¹
nginx h2 POST mimicry-4    196.0 ± 0%   106.0 ± 0%  -45.92% (p=0.000 n=10)
nginx h3 GET nethttp-4     122.0 ± 0%   122.0 ± 0%        ~ (p=1.000 n=10) ¹
nginx h3 GET mimicry-4     192.0 ± 0%   111.0 ± 0%  -42.19% (p=0.000 n=10)
nginx h3 POST nethttp-4    130.0 ± 0%   131.0 ± 0%   +0.77% (p=0.000 n=10)
nginx h3 POST mimicry-4    212.0 ± 0%   124.0 ± 0%  -41.51% (p=0.000 n=10)
go h1 GET nethttp-4        53.00 ± 0%   53.00 ± 0%        ~ (p=1.000 n=10) ¹
go h1 GET mimicry-4       174.00 ± 0%   56.00 ± 0%  -67.82% (p=0.000 n=10)
go h1 POST nethttp-4       64.00 ± 0%   64.00 ± 0%        ~ (p=1.000 n=10) ¹
go h1 POST mimicry-4      201.00 ± 0%   72.00 ± 0%  -64.18% (p=0.000 n=10)
go h2 GET nethttp-4        39.00 ± 0%   39.00 ± 0%        ~ (p=1.000 n=10) ¹
go h2 GET mimicry-4       154.00 ± 0%   73.00 ± 0%  -52.60% (p=0.000 n=10)
go h2 POST nethttp-4       51.00 ± 0%   51.00 ± 0%        ~ (p=1.000 n=10) ¹
go h2 POST mimicry-4      182.00 ± 0%   92.00 ± 0%  -49.45% (p=0.000 n=10)
go h3 GET nethttp-4        104.0 ± 0%   104.0 ± 0%        ~ (p=1.000 n=10) ¹
go h3 GET mimicry-4       176.00 ± 0%   95.00 ± 0%  -46.02% (p=0.000 n=10)
go h3 POST nethttp-4       116.0 ± 0%   116.0 ± 0%        ~ (p=1.000 n=10) ¹
go h3 POST mimicry-4       201.0 ± 0%   113.0 ± 0%  -43.78% (p=0.000 n=10)
geomean                                                       115.8        79.02       -31.75%
¹ all samples are equal
```

## Layer 3: throughput and latency, mimicry before → after

Peak heap in MB; GCs and total GC pause over the 5 s window; B and allocs per request. A point either run discarded shows why.

| server | proto | body | conc | req/s | p99 ms | peak heap MB | GCs | GC pause ms | B/req | allocs/req | CPU µs/req |
|---|---|---|---:|---|---|---|---|---|---|---|---|
| nginx | h1 | 1KB | 1 | 15,884 → 14,938 (-6%) | 0.09 → 0.10 | 9.3 → 9.3 | 172 → 82 | 28 → 19 | 14.2K → 7.5K | 180 → 62 | 39 → 38 |
| nginx | h1 | 1KB | 16 ‡ | 57,598 → 71,357 (+24%) | 2.90 → 1.93 | 22.2 → 24.2 | 352 → 237 | 814 → 573 | 14.2K → 7.5K | 179 → 61 | 30 → 20 |
| nginx | h1 | 1KB | 64 ‡ | 60,648 → 64,800 (+7%) | 5.08 → 4.11 | 26.3 → 22.2 | 343 → 225 | 829 → 492 | 14.2K → 7.5K | 179 → 61 | 29 → 22 |
| nginx | h1 | 1KB | 256 ‡ | 63,767 → 70,101 (+10%) | 10.55 → 9.63 | 29.3 → 26.7 | 277 → 172 | 616 → 326 | 14.2K → 7.5K | 179 → 61 | 27 → 20 |
| nginx | h1 | 100KB | 1 | 9,661 → 10,386 (+7%) | 0.14 → 0.13 | 9.6 → 9.3 | 101 → 56 | 29 → 7 | 14.3K → 7.5K | 180 → 62 | 70 → 61 |
| nginx | h1 | 100KB | 16 ‡ | 28,008 → 34,541 (+23%) | 2.90 → 2.02 | 22.5 → 15.6 | 191 → 154 | 352 → 246 | 14.2K → 7.5K | 179 → 61 | 53 → 46 |
| nginx | h1 | 100KB | 64 ‡ | 23,932 → 26,044 (+9%) | 5.87 → 5.34 | 19.6 → 17.7 | 173 → 104 | 304 → 188 | 14.2K → 7.5K | 179 → 61 | 53 → 47 |
| nginx | h1 | 100KB | 256 ‡ | 31,044 → 34,754 (+12%) | 14.61 → 12.52 | 29.7 → 26.6 | 137 → 83 | 223 → 153 | 14.2K → 7.5K | 179 → 61 | 56 → 45 |
| nginx | h1 | 10MB | 1 | 198 → 201 (+1%) | 5.47 → 5.21 | 8.9 → 8.0 | 2 → 1 | 2 → 0 | 14.3K → 7.5K | 180 → 62 | 3,786 → 3,750 |
| nginx | h1 | 10MB | 16 ‡ | 202 → 622 (+207%) | 80.22 → 46.40 | 10.2 → 9.7 | 2 → 3 | 0 → 5 | 14.3K → 7.5K | 179 → 61 | 3,764 → 2,972 |
| nginx | h1 | 10MB | 64 ‡ | 638 → 640 (+0%) | 121.11 → 115.87 | 13.3 → 12.5 | 6 → 3 | 5 → 6 | 14.2K → 7.5K | 179 → 61 | 2,922 → 2,990 |
| nginx | h1 | 10MB | 256 ‡ | 645 → 630 (-2%) | 467.66 → 429.92 | 23.0 → 19.7 | 4 → 3 | 4 → 0 | 14.2K → 7.5K | 179 → 61 | 2,943 → 3,087 |
| nginx | h2 | 1KB | 1 | 12,670 → 13,131 (+4%) | 0.11 → 0.17 | 9.7 → 9.6 | 132 → 79 | 34 → 25 | 14.3K → 8.5K | 172 → 91 | 52 → 47 |
| nginx | h2 | 1KB | 16 | 44,431 → 54,499 (+23%) | 2.80 → 2.54 | 16.4 → 17.1 | 353 → 271 | 702 → 695 | 14.3K → 8.5K | 172 → 91 | 30 → 22 |
| nginx | h2 | 1KB | 64 | 50,612 → 59,325 (+17%) | 5.28 → 4.75 | 21.5 → 19.8 | 337 → 259 | 697 → 655 | 14.4K → 8.5K | 172 → 91 | 27 → 20 |
| nginx | h2 | 1KB | 256 | 54,249 → 66,134 (+22%) | 13.57 → 11.21 | 30.0 → 31.2 | 265 → 189 | 495 → 351 | 14.5K → 8.6K | 172 → 91 | 27 → 20 |
| nginx | h2 | 100KB | 1 | 5,143 → 8,453 (+64%) | 2.67 → 0.24 | 30.1 → 10.0 | 587 → 54 | 1,031 → 90 | 258.2K → 9.4K | 188 → 106 | 149 → 76 |
| nginx | h2 | 100KB | 16 | 6,174 → 16,775 (+172%) | 9.11 → 3.36 | 28.0 → 15.9 | 542 → 83 | 1,231 → 206 | 249.2K → 9.3K | 188 → 105 | 138 → 56 |
| nginx | h2 | 100KB | 64 | 7,134 → 17,219 (+141%) | 19.01 → 8.45 | 43.8 → 27.8 | 518 → 55 | 1,341 → 144 | 250.7K → 9.5K | 188 → 105 | 127 → 57 |
| nginx | h2 | 100KB | 256 | 9,233 → 16,011 (+73%) | 40.63 → 23.20 | 80.0 → 44.4 | 388 → 35 | 917 → 87 | 236.8K → 9.9K | 188 → 106 | 118 → 61 |
| nginx | h2 | 10MB | 1 | 172 → 178 (+3%) | 14.61 → 6.78 | 62.1 → 19.6 | 103 → 7 | 223 → 10 | 2048.8K → 75.4K | 1,464 → 1,379 | 4,951 → 4,746 |
| nginx | h2 | 10MB | 16 | 186 → 200 (+8%) | 122.16 → 95.94 | 100.2 → 33.2 | 115 → 4 | 335 → 7 | 4391.2K → 71.9K | 1,465 → 1,378 | 4,818 → 4,370 |
| nginx | h2 | 10MB | 64 | 189 → 204 (+8%) | 371.20 → 320.86 | 115.3 → 30.8 | 99 → 5 | 282 → 13 | 3972.1K → 79.8K | 1,466 → 1,379 | 4,802 → 4,342 |
| nginx | h2 | 10MB | 256 | 191 → 202 (+6%) | 1,384.12 → 1,283.46 | 89.6 → 39.5 | 75 → 4 | 176 → 8 | 3250.2K → 73.9K | 1,463 → 1,381 | 4,796 → 4,357 |
| nginx | h3 | 1KB | 1 | 12,765 → 12,264 (-4%) | 0.12 → 0.11 | 1,215.3 → 11.3 | 4 → 157 | 7 → 317 | 23.6K → 18.7K | 193 → 112 | 54 → 48 |
| nginx | h3 | 1KB | 16 | 35,845 → 31,902 (-11%) | 0.79 → 3.59 | 3,536.4 → 23.9 | 3 → 255 | 6 → 765 | 23.7K → 18.8K | 193 → 112 | 34 → 30 |
| nginx | h3 | 1KB | 64 | 35,978 → 33,877 (-6%) | 3.13 → 6.00 | 3,427.8 → 27.7 | 3 → 238 | 13 → 724 | 23.7K → 18.8K | 193 → 112 | 35 → 29 |
| nginx | h3 | 1KB | 256 | 32,594 → 32,773 (+1%) | 14.48 → 16.71 | 3,622.1 → 32.8 | 3 → 178 | 10 → 460 | 23.7K → 18.9K | 193 → 113 | 39 → 31 |
| nginx | h3 | 100KB | 1 | 4,011 → 3,834 (-4%) | 0.33 → 0.35 | 453.3 → 11.5 | 5 → 71 | 15 → 147 | 32.5K → 27.7K | 335 → 254 | 195 → 197 |
| nginx | h3 | 100KB | 16 | 6,125 → 5,721 (-7%) | 4.18 → 7.63 | 714.2 → 13.6 | 4 → 94 | 12 → 328 | 32.5K → 27.7K | 334 → 253 | 172 → 173 |
| nginx | h3 | 100KB | 64 | 5,802 → 5,875 (+1%) | 22.41 → 19.79 | 697.1 → 16.8 | 4 → 78 | 17 → 285 | 32.5K → 27.7K | 334 → 253 | 177 → 170 |
| nginx | h3 | 100KB | 256 | 5,896 → 5,957 (+1%) | 71.83 → 57.93 | 685.4 → 32.5 | 4 → 46 | 18 → 131 | 32.5K → 27.6K | 334 → 253 | 173 → 166 |
| nginx | h3 | 10MB | 1 | 66 → 67 (+1%) | 23.46 → 22.41 | 25.9 → 17.1 | 29 → 41 | 110 → 125 | 1004.5K → 1015.2K | 15,237 → 15,136 | 15,135 → 15,005 |
| nginx | h3 | 10MB | 16 | 67 → 67 (-0%) | 261.10 → 240.12 | 22.2 → 12.6 | 27 → 37 | 98 → 106 | 940.9K → 943.2K | 14,805 → 14,814 | 14,939 → 15,013 |
| nginx | h3 | 10MB | 64 | 64 → 64 (+0%) | 952.11 → 968.88 | 24.4 → 17.4 | 23 → 29 | 106 → 83 | 992.4K → 994.3K | 15,635 → 15,647 | 15,677 → 15,742 |
| nginx | h3 | 10MB | 256 | 48 → 48 (+0%) | 4,009.75 → 3,976.20 | 38.3 → 35.9 | 14 → 15 | 61 → 48 | 1167.0K → 1214.4K | 18,289 → 18,931 | 19,120 → 19,249 |
| go | h1 | 1KB | 1 | 13,360 → 15,638 (+17%) | 0.10 → 0.09 | 8.8 → 9.3 | 132 → 79 | 35 → 14 | 13.4K → 6.9K | 174 → 56 | 46 → 33 |
| go | h1 | 1KB | 64 ‡ | 54,472 → 64,433 (+18%) | 4.95 → 4.15 | 27.9 → 24.0 | 296 → 203 | 669 → 530 | 13.4K → 6.9K | 173 → 55 | 29 → 19 |
| go | h1 | 100KB | 1 | 9,797 → 10,307 (+5%) | 0.13 → 0.13 | 9.2 → 8.8 | 97 → 52 | 32 → 13 | 13.5K → 6.9K | 174 → 56 | 70 → 61 |
| go | h1 | 100KB | 64 ‡ | 35,815 → 39,849 (+11%) | 5.28 → 4.69 | 19.8 → 16.9 | 220 → 141 | 339 → 181 | 13.4K → 6.9K | 173 → 55 | 50 → 45 |
| go | h2 | 1KB | 1 | 10,901 → 11,777 (+8%) | 0.11 → 0.10 | 9.5 → 9.3 | 106 → 68 | 52 → 17 | 13.7K → 8.1K | 154 → 73 | 51 → 43 |
| go | h2 | 1KB | 64 | 44,665 → 52,160 (+17%) | 6.91 → 5.60 | 22.1 → 17.4 | 279 → 213 | 516 → 461 | 13.8K → 8.2K | 154 → 73 | 26 → 18 |
| go | h2 | 100KB | 1 | 5,060 → 7,371 (+46%) | 2.47 → 0.19 | 31.0 → 9.9 | 496 → 43 | 907 → 61 | 208.7K → 8.7K | 164 → 82 | 145 → 82 |
| go | h2 | 100KB | 64 | 8,848 → 13,443 (+52%) | 15.53 → 11.21 | 44.2 → 23.3 | 485 → 48 | 1,124 → 136 | 202.6K → 9.0K | 164 → 81 | 110 → 70 |
| go | h3 | 1KB | 1 | 9,266 → 8,774 (-5%) | 0.14 → 0.16 | 943.2 → 9.9 | 3 → 112 | 3 → 203 | 23.2K → 18.5K | 178 → 97 | 58 → 57 |
| go | h3 | 1KB | 64 | 38,860 → 38,566 (-1%) | 5.21 → 6.46 | 4,187.5 → 21.8 | 3 → 317 | 11 → 696 | 23.1K → 18.4K | 176 → 95 | 32 → 26 |
| go | h3 | 100KB | 1 | 3,290 → 3,123 (-5%) | 0.38 → 0.45 | 343.8 → 10.4 | 4 → 58 | 16 → 135 | 32.5K → 27.9K | 326 → 245 | 208 → 216 |
| go | h3 | 100KB | 64 | 5,949 → 6,011 (+1%) | 20.84 → 20.05 | 651.6 → 17.9 | 4 → 80 | 12 → 191 | 32.0K → 27.5K | 320 → 239 | 177 → 170 |

### mimicry before → after, geometric mean over every load point both runs completed

| metric | HTTP/1.1 | HTTP/2 | HTTP/3 |
|---|---|---|---|
| requests/s | 1.16× (16) | 1.35× (16) | 0.98× (16) |
| p50 | 0.89× (16) | 0.80× (16) | 0.96× (16) |
| p99 | 0.86× (16) | 0.56× (16) | 1.20× (16) |
| CPU per request | 0.84× (16) | 0.69× (16) | 0.94× (16) |
| bytes per request | 0.52× (16) | 0.10× (16) | 0.87× (16) |
| allocs per request | 0.34× (16) | 0.61× (16) | 0.73× (16) |
| peak heap | 0.91× (16) | 0.56× (16) | 0.04× (16) |
| GCs per window | 0.64× (16) | 0.18× (16) | 14.43× (16) |

## Layer 3: mimicry (after) against the others

As in the first report: geometric mean of mimicry's value over the other client's, over the load points both completed; below 1.00 mimicry is lower (better for latency, CPU, allocations and heap; worse for requests/s). net/http is this run's; tls-client, httpcloak and azuretls are the first report's. HTTP/1.1 points above 6 concurrent requests are left out (‡, mimicry queues there by design); first-report ratios in brackets.

**HTTP/1.1**

| metric | vs net/http | vs tls-client | vs httpcloak | vs azuretls |
|---|---|---|---|---|
| requests/s | 0.96× (0.91×), n=5 | 1.06× (1.01×), n=5 | 1.31× (1.25×), n=5 | 1.44× (1.38×), n=5 |
| p50 | 1.03× (1.08×), n=5 | 0.96× (1.00×), n=5 | 0.86× (0.89×), n=5 | 0.81× (0.84×), n=5 |
| p99 | 1.02× (1.18×), n=5 | 0.97× (1.01×), n=5 | 0.34× (0.35×), n=5 | 0.29× (0.30×), n=5 |
| CPU per request | 1.04× (1.20×), n=5 | 0.91× (1.03×), n=5 | 0.61× (0.69×), n=5 | 0.50× (0.57×), n=5 |
| bytes per request | 1.83× (3.51×), n=5 | 0.68× (1.31×), n=5 | 0.06× (0.11×), n=5 | 0.04× (0.07×), n=5 |
| allocs per request | 1.05× (3.14×), n=5 | 0.55× (1.64×), n=5 | 0.23× (0.68×), n=5 | 0.18× (0.54×), n=5 |
| peak heap | 1.01× (1.01×), n=5 | 0.95× (0.98×), n=5 | 0.59× (0.61×), n=5 | 0.53× (0.55×), n=5 |

**HTTP/2**

| metric | vs net/http | vs tls-client | vs httpcloak | vs azuretls |
|---|---|---|---|---|
| requests/s | 1.05× (0.77×), n=16 | 1.18× (0.88×), n=16 | 1.08× (0.80×), n=16 | 2.38× (1.63×), n=12 |
| p50 | 0.95× (1.22×), n=16 | 0.89× (1.11×), n=16 | 0.94× (1.17×), n=16 | 0.46× (0.61×), n=12 |
| p99 | 0.93× (1.70×), n=16 | 0.85× (1.51×), n=16 | 1.00× (1.78×), n=16 | 0.34× (0.65×), n=12 |
| CPU per request | 0.88× (1.30×), n=16 | 0.82× (1.19×), n=16 | 0.61× (0.89×), n=16 | 0.38× (0.60×), n=12 |
| bytes per request | 1.44× (15.83×), n=16 | 0.62× (6.52×), n=16 | 0.09× (0.95×), n=16 | 0.12× (0.76×), n=12 |
| allocs per request | 1.49× (2.46×), n=16 | 0.61× (1.00×), n=16 | 0.80× (1.32×), n=16 | 0.21× (0.40×), n=12 |
| peak heap | 0.83× (1.47×), n=16 | 0.81× (1.44×), n=16 | 0.04× (0.07×), n=16 | 0.71× (1.06×), n=12 |

**HTTP/3**

| metric | vs net/http | vs tls-client | vs httpcloak | vs azuretls |
|---|---|---|---|---|
| requests/s | 0.65× (0.65×), n=16 | 0.81× (0.83×), n=16 | 1.06× (1.09×), n=16 | 0.98× (1.01×), n=15 |
| p50 | 1.54× (1.60×), n=16 | 1.23× (1.28×), n=16 | 0.93× (0.97×), n=16 | 1.05× (1.09×), n=15 |
| p99 | 1.43× (1.05×), n=16 | 1.06× (0.88×), n=16 | 0.72× (0.60×), n=16 | 0.74× (0.61×), n=15 |
| CPU per request | 1.52× (1.65×), n=16 | 1.12× (1.19×), n=16 | 0.81× (0.86×), n=16 | 0.85× (0.90×), n=15 |
| bytes per request | 1.82× (2.12×), n=16 | 1.10× (1.27×), n=16 | 0.28× (0.32×), n=16 | 0.21× (0.24×), n=15 |
| allocs per request | 0.76× (1.06×), n=16 | 0.55× (0.75×), n=16 | 0.45× (0.62×), n=16 | 0.45× (0.63×), n=15 |
| peak heap | 1.10× (27.66×), n=16 | 0.96× (24.67×), n=16 | 0.35× (8.99×), n=16 | 0.33× (10.07×), n=15 |

## Layer 4: memory per idle connection, mimicry before → after

| proto | heap KB/client | goroutines/client | heap KB/idle conn | goroutines/idle conn | after close: heap KB | net/http now: KB/idle conn |
|---|---|---|---|---|---|---|
| h1 | 153.3 → 153.3 | 0.0 → 0.0 | 41.3 → 40.1 | 0.0 → 0.0 | 50 → 55 | 25.3 |
| h2 | 153.3 → 153.3 | 0.0 → 0.0 | 35.7 → 35.7 | 2.0 → 2.0 | 134 → 140 | 31.2 |
| h3 ‡ | 153.7 → 153.6 | 0.0 → 0.0 | 146.4 → 135.4 | 11.0 → 11.0 | 437 → 426 | 63.1 |

‡ HTTP/3: the idle state is the QUIC connection plus the HTTP/2 connection Alt-Svc was learnt on, as in Chrome.

## Discarded in this run

None.

## What still stands out

Profiles from a separate run (`bench run -profile`, mimicry only, nginx, concurrency 64, after the fixes; not part of the numbers above).

- **HTTP/1.1 and HTTP/2 are now level with net/http and ahead of the impersonating clients.** HTTP/2 went from 0.77× net/http's requests/s to 1.05×, and the 100 KB response's 250 KB/request of buffer growth is gone (9 KB). On HTTP/1.1 above 6 concurrent requests (‡), mimicry still runs at about a third of net/http's requests/s: that is Chrome's 6-connection limit, unchanged by design.
- **HTTP/3 throughput did not move: 0.65× net/http, 0.81× tls-client.** The leak is fixed (in-use heap after the window: 7 MB, against 830 MB before; peak heap 1.2–4.2 GB → 11–33 MB). With a normal heap the GC now runs 14× as often, so HTTP/3 p99 is up 1.2× and GC pause per 5 s window is 130–770 ms. HTTP/3 is now mimicry's slowest protocol. CPU per request is 1.52× net/http's (quic-go).
- **HTTP/3 still allocates the QUIC pipe buffers anew for every stream.** In the CPU profile of HTTP/3 1 KB at concurrency 64, `quic.(*Conn).loop` takes 23% of CPU, UDP sends (one `write` syscall per datagram) 9%, QPACK decode 9% and the GC mark workers 12%. In the allocation profile, `pipebufPool`'s `New` (`internal/quic/pipe.go`, 4 KB buffers) is 43% of all bytes allocated, about 8 KB per request. Buffers go back to the pool only in `pipe.go:166`, when a pipe's data is consumed. [INFERENCE] Streams ended by `finish` (CloseRead), and the send side, drop theirs without `recycle`, so the pool never refills. The next largest allocators are `net/textproto.MIMEHeader.Add` (response headers, 6%), `mimicry.fill` (6%), `qpack.(*Encoder).indexAll` (5%), `qpack.(*Decoder).Decode` (6%) and `quic.newStream` (6%).
- **A new mimicry Transport still holds about 153 KB before any request**, against 2–16 KB for the others (layer 4, unchanged by the fixes). It matters only for code that makes many Transports, such as one per site (decision 0019).
- **Per-request allocations on HTTP/1.1 match net/http's count (1.05×) but not its bytes (1.83×).** HTTP/2 allocations are 1.49× net/http's.

Not re-measured: layer 2 (new connections) and layer 5 (page loads). The fixes touch neither handshakes nor pooling, so the first report's numbers stand for them, except where HTTP/2 response buffering affected page loads.

