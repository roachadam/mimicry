# Traffic

`Config.Traffic` counts the bytes mimicry sends and receives: what a run cost, by protocol and, if you ask, by host and proxy. Why it is built this way: [decision 0070](../decisions/0070-traffic-meter.md).

```go
meter := mimicry.NewTrafficMeter(mimicry.TrafficOptions{ByHost: true})

client, err := mimicry.NewClient(mimicry.Config{
	Profile: profiles.ChromeMacOS(),
	Traffic: meter,
})
// ... requests ...

t := meter.Snapshot()
fmt.Println(t.Total().Sent, t.Total().Received)
fmt.Println(t.HTTP3.Received)
fmt.Println(t.ByHost[mimicry.Peer{Host: "www.shop.example", Proxy: "http://proxy.example:8080"}])
```

- **Snapshot** returns the counts so far: closed connections', and the open ones' bytes until now.
- **Reset** returns the same, then starts again from zero.
- **Sharing:** several Transports given one meter count into it, as one identity's tabs would.
- **Off by default.** With no meter, nothing is counted and nothing costs.

## What is counted

Every socket mimicry opens, counted where its bytes meet the OS, so a tunnel counts once:

- TCP connections to sites and proxies: TLS, HTTP/1.1, HTTP/2, WebSockets, CONNECT exchanges, an https proxy's own TLS.
- QUIC connections, directly, through a SOCKS5 UDP association (its header included) and to a CONNECT-UDP proxy.
- The DNS lookups mimicry sends itself: each host's HTTPS record, for ECH.

That includes traffic you never asked for directly: CORS preflights, redirect hops, QUIC attempts that lost the race to TCP, PINGs ([what mimicry does](what-mimicry-does.md)).

**Not counted:**

- **Packet headers.** Counts are payload bytes as the sockets read and write them. IP, TCP and UDP headers (about 40–60 bytes a packet) and the kernel's TCP retransmissions are not counted, so a proxy provider measuring on the wire sees a little more.
- **A and AAAA lookups.** They go through the system's resolver, or `Config.Resolver`. To count them, count in the `Dial` of the `net.Resolver` you set as `Config.Resolver`.
- **Bytes after close.** What the server sends after mimicry closed a connection (its TLS close_notify, say) never reaches mimicry.

## By protocol

| Field | What it holds |
|---|---|
| `HTTP1` | TCP connections that carried HTTP/1.1, plain `http://` included |
| `HTTP2` | TCP connections that negotiated HTTP/2 [1] |
| `HTTP3` | QUIC connections [2] |
| `WebSocket` | WebSockets' own HTTP/1.1 connections, handshake included [3] |
| `DNS` | ECH's HTTPS record lookups |
| `Other` | connections that ended before their protocol was known [4] |

1. An https proxy's HTTP/2 connection counts here whatever its tunnels carry.
2. Attempts that lost the race to TCP, SOCKS5 UDP associations and CONNECT-UDP proxy connections included.
3. A WebSocket over HTTP/2 is a stream of a shared connection: it counts in `HTTP2`.
4. A TCP connection that lost a race before its TLS handshake ended, say.

A connection's handshake counts under its protocol. A connection through an HTTP or SOCKS proxy carries one tunnel and counts as what the tunnel carries.

## By host

With `TrafficOptions{ByHost: true}`, `Traffic.ByHost` holds the counts per `Peer`:

- **Host** is the host the connection was dialed for. A DNS lookup's is the nameserver it was sent to.
- **Proxy** is the proxy it went through, scheme, host and port, without credentials; `""` for none.
- **Coalesced connections:** a browser, and mimicry, reuses an HTTP/2 connection for another host when its certificate covers it and the host resolves to the same address. Those requests count under the host the connection was dialed for. `Config.DisableCoalescing` gives each host its own connection, for exact per-host counts, at the cost of looking less like the browser.
- **An https proxy's HTTP/2 connection** carries tunnels to many hosts; it counts under its proxy with no host.
- **Memory:** one entry per host and proxy reached, until `Reset`.

## Cost

Each socket keeps its own counter, so counting is one uncontended atomic add per socket read or write: well under 1% of a request. A snapshot's cost grows with the open connections, not with the traffic. The warm-request allocation bounds hold with a meter on.
