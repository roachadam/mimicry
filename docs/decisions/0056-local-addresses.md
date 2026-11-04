# 0056: One local address for TCP and HTTP/3

- Status: Accepted
- Date: 2026-11-04
- Commits: the one that closes [#81](https://github.com/roachadam/mimicry/issues/81)

**Summary.** `Config.LocalAddrs`, at most one IPv4 and one IPv6 address,
binds every socket mimicry opens itself: TCP connects (unless
`Config.DialContext` opens them), HTTP/3's UDP sockets and SOCKS5 UDP
relays. A caller sending each identity from its own address now sends its
HTTP/3 from that address too, as a browser sends both from one interface.

## Problem

HTTP/3 never went through `Config.DialContext`: QUIC's socket was opened
on the unspecified address. A caller binding TCP to a chosen address
(multi-address scrapers) sent HTTP/2 from it and HTTP/3 from the host's
default, so a server saw one session from two addresses once its site
advertised HTTP/3. The only workaround was `DisableHTTP3`, itself a tell on
HTTP/3 sites.

## Options

- **`Config.ListenPacket`, a hook mirroring `DialContext` for UDP.** Full
  control, but every caller writes the same bind, and the address family
  has to match the server's, which the hook cannot see before mimicry
  resolves it.
- **One `Config.LocalAddr`.** A dual-stack host could bind one family only.
- **`Config.LocalAddrs`, one per family** (chosen). mimicry picks the
  server address of a family it has an address for and binds that
  family's. A server reachable only over a family with none is an error,
  never a silent send from the default address.

How to bind TCP with both families:

- **`net.Dialer.LocalAddr`, one family at a time** (IPv6, then IPv4). The
  dialer binds one local address for both of Happy Eyeballs' families, so
  the two cannot race: a host whose IPv6 is blackholed waits out that
  connect before IPv4 is tried, where a browser moves on after its
  fallback delay.
- **Bind each socket in the dialer's `Control` hook, before `connect()`**
  (chosen; nukilabs/tlsclient's way, `proxy/bind_unix.go`). Each family's
  socket gets its own address and Go's Happy Eyeballs races them as
  without `LocalAddrs` (300 ms fallback, Chrome's too): the dial matches
  the unbound one in every way but the source address. It costs a bind
  per OS (`bind_unix.go`, `bind_windows.go`).

Other clients: bogdanfinn/tls-client binds TCP only (`WithLocalAddr`); its
`http3.Transport` gets no dialer, so QUIC leaves from the default address,
the bug this fixes. nukilabs/tlsclient binds TCP and UDP per family as
chosen here.

## Decision

`Config.LocalAddrs []netip.Addr`, checked by `NewTransport` (host
addresses, at most one per family), passed to `internal/dial`, which binds
the TCP connect (in the dialer's `Control` hook, per family), `ListenUDP`
and the SOCKS5 relay socket to it. `DialContext`, when set, still opens
TCP: the caller binds it there.

## Consequences

- TCP and HTTP/3 leave from the same address with one setting.
- `TestLocalAddrs` (`internal/dial`) checks the TCP peer address and the
  UDP socket's bound address for IPv4, IPv6, both (by address and by a
  name with both, raced), and the refusal of an uncovered family; its IPv4
  rows need 127.0.0.2 on loopback (Linux CI).

## Revisit when

A caller needs a socket option beyond the address (then a `ListenPacket`
hook).
