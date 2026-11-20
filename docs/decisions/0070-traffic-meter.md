# 0070: Traffic meter

- Status: Proposed
- Date: 2026-11-20
- Commits: none yet ([#162](https://github.com/roachadam/mimicry/issues/162))

**Summary.** `Config.Traffic`, a `*TrafficMeter`, counts the bytes mimicry's sockets send and receive, by protocol: every connection, the traffic mimicry makes beyond the caller's requests included. Each socket keeps its own counters, which a snapshot sums, so counting costs one uncontended atomic add per socket read or write, and nothing when no meter is set. A socket's protocol and host are tagged once, so the breakdowns cost nothing per byte. Per-host totals are opt-in; there are no per-request counts.

## Problem

Proxy providers bill per byte. A scraper wants to know what a run cost, per identity and per site. Other clients offer this:

- **tls-client:** `GetBandwidthTracker()` with read, write and total bytes. It wraps each TCP `net.Conn`, so HTTP/3 is not counted.
- **curl_cffi:** built on libcurl, which reports per-transfer sizes (`CURLINFO_SIZE_DOWNLOAD_T`, `CURLINFO_SIZE_UPLOAD_T`).

mimicry sends traffic the caller never asked for directly: CORS preflights, redirect hops, Alt-Svc QUIC attempts that lose to TCP, PINGs, ECH's HTTPS lookups ([what-mimicry-does.md](../concepts/what-mimicry-does.md)). A caller cannot count those from outside.

Requirement from the maintainer: close to zero cost, and zero when off.

## Options

- **Wrap every `net.Conn` and `net.PacketConn` in `internal/dial`** (tls-client's way, plus UDP).
  - Wrapping the UDP socket breaks QUIC's fast path. `quic.wrapPacketConn` (`client_path.go`) takes the batched `recvmmsg` path (`udp_batch_linux.go`) only for a `*net.UDPConn`. Anything else falls back to `netPacketConn`, one datagram per read.
  - TCP alone is fine to wrap. Nothing outside tests asserts `*net.TCPConn`.
- **One shared atomic counter per meter.**
  - 9 ns per count on 4 busy cores, 34 ns on 12 (measured on an M3 Pro, every goroutine counting at once). The cache line bounces between cores.
- **Per-host map looked up on each read or write.** 86–89 ns per count on 4–12 cores. Rejected.
- **OS counters** (`TCP_INFO` byte counts). Linux only, no UDP, no proxies. Rejected.
- **Per-request counts in the HTTP/1.1, HTTP/2 and HTTP/3 stream code.** Cheap at run time, but it touches the three protocol layers, and its numbers leave out TLS and QUIC overhead, which is what a proxy bills. Left out for now.
- **Chosen: a counter per socket, tagged with its protocol and host, summed on read.** 0.4–0.5 ns per count on 4–12 cores, no contention. Counted where the bytes meet the OS, so nothing is counted twice.

## Decision

### Public API

```go
type Config struct {
    // ...
    // Traffic, if set, counts the bytes this Transport's sockets send and
    // receive. Several Transports may share one meter.
    Traffic *TrafficMeter
}

type TrafficOptions struct {
    // ByHost keeps a total per host and proxy reached: one entry each.
    ByHost bool
}

func NewTrafficMeter(opts TrafficOptions) *TrafficMeter

// Snapshot returns the counts so far: closed sockets' totals plus the live
// ones' current counts.
func (m *TrafficMeter) Snapshot() Traffic

// Reset returns the counts so far and starts again from zero.
func (m *TrafficMeter) Reset() Traffic

type Traffic struct {
    HTTP1     Bytes // TCP connections that negotiated HTTP/1.1 (or plain http://)
    HTTP2     Bytes // TCP connections that negotiated h2
    HTTP3     Bytes // QUIC connections, raced attempts that lost included
    WebSocket Bytes // a WebSocket's own HTTP/1.1 connection, handshake included
    DNS       Bytes // lookups mimicry sends itself (ECH's HTTPS records)
    Other     Bytes // sockets that ended before a protocol was known

    ByHost map[Peer]Bytes // nil unless TrafficOptions.ByHost
}

func (t Traffic) Total() Bytes

type Bytes struct {
    Sent, Received int64
}

// Peer is where a socket went: the host it was dialed for, and the proxy it
// went through ("" for none).
type Peer struct {
    Host, Proxy string
}
```

A fixed struct, not a map: a snapshot without `ByHost` allocates nothing.

### What is counted

- **Real sockets only.** Bytes are counted where they meet the OS, so a tunnel is counted once, on its outer socket.

| Socket | Opened in | Counted in |
|---|---|---|
| TCP: origins, proxies, WebSockets | `dial.Dialer.dial` | a `net.Conn` wrapper there [1] |
| UDP: direct QUIC | `dial.Dialer.ListenUDP` | `internal/quic`'s `netUDPConn.Read` and `Write` [2] |
| UDP: SOCKS5 association | `dial.Dialer.dialRelay` | `proxy.Association`, SOCKS header included |
| UDP: CONNECT-UDP | rides the proxy's QUIC connection | that connection's own socket; the inner QUIC connection is not counted [3] |
| DNS: ECH's HTTPS lookups | `httpsrr.Resolver.dial` | a `net.Conn` wrapper there |

1. TLS, HTTP/1.1, HTTP/2, the CONNECT exchange and an HTTPS proxy's own TLS all ride this socket. `Config.DialContext`'s conns are wrapped too.
2. Counted after the batch read and the write, by datagram size. The `*net.UDPConn` fast path is kept.
3. Its packets are the outer connection's DATAGRAM frames.

- **Payload bytes, not packet headers.** Counts are what goes through the socket API: TCP and UDP payloads. IP, TCP and UDP headers (about 40–60 bytes a packet) and TCP retransmissions are the kernel's and not counted. TLS records, QUIC packets, QUIC retransmissions, ACKs and padding are counted.
- **Not counted:** A and AAAA lookups through the system resolver (or `Config.Resolver`'s own `Dial`). They do not go through mimicry's sockets.
  - A caller who needs them can count them in the `Dial` of the `net.Resolver` set as `Config.Resolver`.

### Protocol

- A socket's protocol is set once, by the engine, when it is known: after ALPN for TLS, at dial for QUIC, at the upgrade for a WebSocket's own connection. One atomic store per connection.
- The whole socket's bytes go to that protocol when summed, its handshake included. A socket that closes without one (a TCP race lost before TLS finished) counts as `Other`.
- **Proxies:** a socket to an HTTP or SOCKS proxy carries one tunnel; it counts as the tunnel's protocol (h1 or h2 to the site). A socket to an HTTPS proxy speaking HTTP/2 carries many tunnels of mixed protocols; it counts as `HTTP2`. CONNECT-UDP's outer QUIC connection counts as `HTTP3`.
- **WebSockets over HTTP/2** (RFC 8441, a stream on a shared connection) count as `HTTP2`: separating a stream's bytes needs counting in the stream code, which this design leaves out.

### Host

- With `ByHost`, a socket's `Peer` is set once when it is dialed: the host it was dialed for, and its proxy.
- **Coalesced HTTP/2 connections count under the host they were dialed for.**
  - mimicry, as Chrome does, reuses one HTTP/2 connection for another host when the certificate covers it and the host resolves to the same address ([what-mimicry-does.md](../concepts/what-mimicry-does.md#http2-coalescing)).
  - Example: a page on `www.shop.example` loads from `static.shop.example` and `api.shop.example`. All three go on the connection dialed for `www`, so `ByHost` shows every byte under `www` and nothing under the other two.
  - Exact per-host numbers need either `Config.DisableCoalescing` (one connection per host, unlike the browser) or counting in the stream code.
- An HTTPS proxy's HTTP/2 connection carrying tunnels to many hosts counts under `Peer{Proxy: p}`, `Host` empty.

### How

- **Per socket:** a `socketCount` struct with two `atomic.Int64`, a protocol and a `Peer`, padded to its own cache line. One add per read or write call. A connection's reads and its writes are each already serialized, and no other socket touches the line, so the atomic is uncontended. It is atomic only so a snapshot can read it from another goroutine.
- **Registry:** the meter keeps the live sockets in a set under a mutex, touched only when a socket opens or closes. On close, its counts fold into its protocol's total (and its `Peer`'s, with `ByHost`).
- **Snapshot:** locks the registry, sums the totals and the live sockets. Cost grows with the live sockets, never with traffic.
- **Off:** with `Config.Traffic` nil, `internal/dial` returns unwrapped conns, `internal/quic` skips a nil pointer, and the protocol setter is a no-op.

### Language SDKs

- `mimicry-core` holds meters as it holds jars: `new_traffic_meter(by_host=)` returns an object, and `snapshot()` and `reset()` are calls.

## Evidence

- Counter costs, measured 2026-11-20 on an M3 Pro, Go 1.27, `RunParallel` on 1, 4 and 12 cores, every goroutine counting continuously (wall time per count):

| Design | 1 core | 4 cores | 12 cores |
|---|---|---|---|
| Shared atomic | 1.9 ns | 9 ns | 34 ns |
| Per-socket atomic, padded | 1.9 ns | 0.5 ns | 0.4 ns |
| Per-host map behind a mutex | 6.6 ns | 86 ns | 89 ns |
| No meter (nil check) | ~0 | ~0 | ~0 |

- A warm GET costs mimicry 35–50 µs ([performance](../performance.md)) and a few socket calls. Per-socket counting is well under 0.1% of that.
- QUIC's batched read path needs a `*net.UDPConn`: `wrapPacketConn` in `internal/quic/client_path.go`.

## Consequences

- One counting point per socket kind, all in `internal/dial`, `internal/quic` and `internal/proxy`, and one protocol setter where the engine learns ALPN. A new socket kind must count too; the e2e test below fails if it doesn't.
- Totals include TLS and QUIC overhead, handshakes and raced QUIC attempts. That is close to what a proxy bills, less the packet headers.
- `ByHost` grows one entry per host. A crawler can leave it off, or `Reset()` between batches.

**Tests:**

- **e2e:** the capture server and the test proxies count what they receive and send. For a page over HTTP/1.1, HTTP/2 and HTTP/3, direct and through each proxy kind, mimicry's totals and its protocol split equal theirs. The bench's layer 6 already checks a client's sent bytes against what its proxy relayed.
- **Cost:** `TestWarmRequestAllocations` unchanged with the meter on and off; the warm-request benchmarks within noise.

**Docs:** a concept doc (`docs/concepts/traffic.md`), a row in [coming-from-tls-client.md](../concepts/coming-from-tls-client.md) mapping `GetBandwidthTracker`, the SDK READMEs.

## Revisit when

- A caller needs per-request counts, exact per-host counts on coalesced connections, or WebSockets over HTTP/2 counted on their own. All three need counting in the stream code.
- Counting the system resolver's lookups is asked for.
- The registry's mutex shows up in a profile of a client opening many connections a second.
