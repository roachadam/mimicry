# 0038: HTTP/3 through SOCKS5 proxies on request, never a datagram around the proxy

- Status: Accepted
- Date: 2026-10-28
- Commits: `c1cd089`

**Summary.** `Config.HTTP3OverSOCKS5` (off by default) lets requests
through a SOCKS5 proxy use HTTP/3 via UDP ASSOCIATE, with the profile's
QUIC unchanged; through any proxy, no datagram ever goes around it. It
gives parity with other impersonating clients while the default stays the
browsers' TCP-only behaviour behind a proxy. A library user who enables
it reaches HTTP/3 sites from the proxy's address, which no browser behind
a proxy does.

## Problem

Requests through a proxy always went on TCP: browsers do not use HTTP/3
through an HTTP or SOCKS5 proxy, and the engine refused QUIC whenever a
proxy was configured.
Other impersonating clients (azuretls-client, httpcloak, tls-client v1.17)
carry HTTP/3 through SOCKS5 proxies with UDP ASSOCIATE (RFC 1928, section
7), and callers asked for parity: a site that serves HTTP/3 is reached over
it from the proxy's address. (An earlier note here said tls-client sends its
QUIC straight to the site; that was read from tls-client v1.13.1, an older
version than the one benchmarked. The multi-client benchmark showed v1.17.1
sending it through the proxy's UDP relay.)

## Options

- **Keep TCP only through proxies** (the browsers' behaviour). Faithful,
  and still the default; but no way to reach HTTP/3 from a proxy's address.
- **UDP ASSOCIATE, opt-in** (chosen). The SOCKS5 header travels only
  between client and proxy; the proxy sends the QUIC datagrams on
  unchanged, so the site sees the profile's QUIC exactly as without the
  proxy, from the proxy's address.
- **MASQUE (CONNECT-UDP, RFC 9298) through an HTTP proxy.** The standard
  way, and what an HTTP proxy would need; few proxy providers offer it.
  Deferred ([Proxies, Limits](../concepts/proxies.md#limits)).
- **How QUIC runs over the association.** (a) A local socket pair, as
  udpbara (httpcloak) does: QUIC keeps a real `*net.UDPConn`, a goroutine
  copies between it and the relay socket. udpbara needs it because
  quic-go demands a `*net.UDPConn`. (b) A `net.PacketConn` wrapping the
  relay socket, given to mimicry's QUIC copy (chosen): `internal/quic` already
  takes any `net.PacketConn` (upstream's `netPacketConn`, used for anything
  but a `*net.UDPConn`), so it needs no change for it. What (b) loses is
  what a relay makes meaningless: ECN marks and the local address (the
  proxy's own socket sets the IP header the site sees), and Linux's batched
  receive (change 13), which (a)'s copying goroutine would lose as well
  unless it batched itself. (a) costs two more syscalls and a goroutine
  hand-off per datagram: measured on an M3 Pro, sending and receiving one
  1252-byte datagram over loopback took 12.0–12.4 µs through the wrapper
  and 22.0–24.6 µs through a socket pair (3 runs of 200,000 each).
- **Which address the datagrams name.** `socks5://` with an IP address
  (local DNS, curl's meaning) and `socks5h://` with the host name was the
  first plan. mimicry's TCP path sends the host name for both schemes, as
  browsers do ([0014](0014-proxies.md)); an IP for `socks5://` would add a
  local DNS lookup the TCP path through the same proxy never makes,
  telling the local resolver the host. Both send the host name (an IP
  literal host goes as an address), as on TCP. Residential proxies that
  authenticate or route on the requested name need it too (udpbara's
  README reports it; not tested here).

## Decision

- `Config.HTTP3OverSOCKS5` (default false). With it, a request through a
  `socks5` or `socks5h` proxy uses HTTP/3 as it would directly: Alt-Svc,
  each profile's discovery and race, 0-RTT, fast fallback (0036) and
  broken-origin marking. Connections, tickets and origin state stay per
  proxy (the pool key, 0014); the Alt-Svc cache is keyed by origin and
  partition without the proxy, which also keeps a proxy URL and its
  credentials out of a saved cache.
- One association per QUIC connection, and per socket: Chrome's move to a
  new port on a degrading path opens a new one. The association
  authenticates as the TCP path does (the profile's methods, username and
  password when the URL has them), asks with DST 0.0.0.0:0, and takes an
  unspecified BND.ADDR as the proxy's address. Its socket is connected to
  the relay; fragments (FRAG other than 0) and malformed datagrams are
  dropped.
- The association lives exactly as long as its control connection. When
  the proxy closes it, the socket's reads fail and the QUIC connection
  ends at once (`internal/quic` change 14, a sole endpoint's socket
  failing), so requests go on TCP through the proxy instead of waiting for
  the connection to idle out.
- A refusal of UDP ASSOCIATE (any reply other than success) is remembered
  per proxy URL: no later connection asks again, and requests through that
  proxy stay on TCP.
- **Never a datagram around the proxy.** A request through any proxy
  sends no UDP to the site: through an HTTP proxy, or with the option off,
  no UDP at all; with it on, only to the SOCKS5 relay. The dial path
  (`dialQUIC`) opens a direct socket only for a request with no proxy.
- `internal/proxy` holds the protocol (`UDPAssociate`, `Association`);
  `internal/dial` the browser-facing setup (`ListenUDPVia`, `UDPRefused`).

## Evidence

- `TestQUICInterop`'s rows, every profile, against quic-go's server:
  `socks5-no-leak` (a SOCKS5 proxy that records its relay's datagrams and
  forwards none; the server's relay must see no client: with the option
  off and through an HTTP proxy no UDP at all, with it on the datagrams
  reach the proxy naming the host), `socks5` (socks5 and socks5h with
  credentials: requests, a 256 KB download and 0-RTT through the proxy;
  the server receives the same first flight as directly, [1250 1250]
  Chrome, [1252 1252] Firefox, [1200 1200] Safari), `socks5-teardown`
  and `socks5-refused`.
- Mutation checks, each failing its row: the dial bypassing the
  association (`socks5-no-leak`: the server got datagrams directly, for
  every profile; with the HTTP-proxy refusal also removed, through the
  HTTP proxy too), the option's gate removed (`socks5-no-leak`: the
  proxy got UDP ASSOCIATE with the option off), change 14 removed
  (`socks5-teardown`: the request waited 12 s (Safari) or until the 20 s
  deadline), the refusal not remembered (`socks5-refused`: 2–4
  associations asked for, want 1), a byte added to each datagram
  (`socks5`).
- Sizes: a profile's datagrams are at most its `datagramSize` (1200–1252
  bytes). The SOCKS5 header adds 10 bytes for an IPv4 address, 22 for
  IPv6, 7 plus the name's length for a host name: a 12-character host's
  1252-byte datagram is 1271 bytes to the proxy, 1299 with IPv4 and UDP
  headers. Only a host name over about 210 characters (190 to an IPv6
  relay) takes the client-to-proxy datagram past a 1500-byte MTU, where
  the OS fragments it; the site's leg is unchanged.

## Consequences

- With the option on, a site sees HTTP/3 from a proxy's address, which no
  browser behind a proxy sends: the deviation is the caller's choice and
  documented as one (README, what-mimicry-does.md).
- A proxy that accepts UDP ASSOCIATE but never relays (common among
  residential providers) costs each origin its QUIC attempts until it is
  marked broken (`Fallback.BrokenAfter`), each request waiting at most the
  race delay with fast fallback; it is not marked per proxy.
- `Config.DialContext` reaches the proxy for the control connection; the
  relay socket is a plain UDP socket, as a direct QUIC socket is.

## Revisit when

Proxy providers offer MASQUE widely, a browser is seen using HTTP/3
through a proxy, or callers hit proxies that accept associations and drop
them (a per-proxy mark after failed handshakes).
