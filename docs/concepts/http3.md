# HTTP/3

mimicry speaks HTTP/3 over QUIC where the profile's browser would. It moves
a site to HTTP/3 when and how that browser does. Its QUIC packets and
HTTP/3 settings and headers match the browser's.

HTTP/3 is on by default for every built-in profile. Set
`Config.DisableHTTP3` to keep every request on TCP.

## When a request goes over HTTP/3

A request can use HTTP/3 only when all of these hold:

- it is an `https://` request;
- it is sent directly, or through a SOCKS5 proxy with
  `Config.HTTP3OverSOCKS5` (see [Proxies](proxies.md#http3-through-socks5));
- the site earlier answered with an `Alt-Svc: h3=…` header that has not
  expired. This header tells the browser the site also speaks HTTP/3.

Requests the profile has no HTTP/3 data for stay on TCP.

Each browser then switches in its own way:

- **Chrome** sends the next request on HTTP/2 and opens a QUIC connection
  beside it. Later requests use HTTP/3 once it is up.
- **Firefox** opens a QUIC connection as soon as it sees the header.
  Requests stay on TCP until it is up, then move to HTTP/3. Firefox then
  closes its HTTP/2 connections to the site.
- **Safari** uses HTTP/3 only when a request needs a new connection. It
  keeps using a live HTTP/2 connection.

Once a site has used HTTP/3, a request that finds no QUIC connection opens
a new one. One exception: after the server closes a connection, Firefox
sends the next request on a new TCP connection and opens QUIC after it.

QUIC looks up the host through `Config.Resolver`. `Config.DialContext` is
not used for QUIC.

## Sharing what a Transport learned

By default, each Transport keeps the `Alt-Svc` entries it learns to itself.
To share them, give several Transports the same `AltSvcCache` through
`Config.AltSvc`. They then share entries the way a browser's tabs do.
`WriteJSON` and `ReadJSON` save and load the cache, as browsers keep it
across restarts.

A Transport that finds a site in the cache acts as if it had learned the
entry itself:

- **Chrome and Safari** open a QUIC connection for the first request. If it
  is not up quickly, they also try TCP. On a working network the first page
  loads over HTTP/3.
- **Firefox** sends the first request on TCP and checks QUIC in the
  background. Later requests move to HTTP/3 once it is up.

Only the `Alt-Svc` entries are shared. Each Transport keeps its own
connections, session tickets and list of sites where QUIC failed. So a
Transport's first QUIC connection is a full handshake, as after a browser
restart.

Entries are keyed by site, not by the page that made the request. Browsers
also key them by that page. So one cache shared between Transports used
for unrelated sites lets each see the others' entries.

## If QUIC is blocked

Some networks drop UDP, so QUIC never connects. Each browser then falls
back to TCP after a wait. mimicry has two modes for this.

**Default (fast fallback).** mimicry keeps each browser's first wait before
trying TCP. It does not wait longer than that, even where the browser
would. A resumed connection that gets no answer stops holding requests at
that point. A failed attempt sends the request to TCP at once. On a
working network both modes look the same.

**`Config.BrowserQUICFallback`.** mimicry waits exactly as long as the
browser does, including its longer holds. Use it when the exact timing of
the fallback matters more than speed.

| Browser | First try | Falls back to TCP | Longer hold (`BrowserQUICFallback` only) |
| --- | --- | --- | --- |
| Chrome | QUIC | after about 300 ms, while QUIC keeps trying | a resumed connection holds requests until it gives up, after 4 s |
| Firefox | TCP, QUIC checked in the background | never waits for QUIC | requests on a resumed connection after a server close wait up to 5 s |
| Safari | QUIC | after about 100 ms, dropping the QUIC attempt | none |

After one failed attempt (two for Safari), mimicry keeps that site on TCP
for the rest of the Transport's life, as the browsers do. Firefox tries
QUIC again a few seconds later.

If a live connection stops answering, each browser reacts differently:

- Chrome moves the connection to a new UDP port and carries on.
- Safari closes it after 12 s and sends the waiting requests again on a
  new QUIC connection.
- Firefox keeps trying until its 30 s idle timeout, then sends the
  requests on TCP.

mimicry keeps these waits in both modes. The requests were already sent,
so resending them early would show the server duplicates. While a request
waits on a quiet connection, mimicry sends PINGs as the browser does. An
idle connection with no request waiting still closes silently.

## Settings

- `Config.DisableHTTP3` keeps every request on TCP.
- `Config.HTTP3OverSOCKS5` sends HTTP/3 through a SOCKS5 proxy. See
  [Proxies](proxies.md#http3-through-socks5).
- `Config.UDPReadBufferSize` sets how much each QUIC socket asks the OS to
  buffer. The default is 1 MB, as Chrome uses. A negative value keeps the
  OS default. A bigger buffer drops fewer packets under load. It changes
  nothing on the wire.
- `Config.HTTP3ACKEvery` acknowledges packets less often than the browser
  does. Use it only for a server that a fast client overloads. It makes
  the traffic differ from the browser's.

Idle QUIC connections close as described in
[What mimicry does](what-mimicry-does.md).

**0-RTT.** When reconnecting to a site, Chrome and Firefox profiles send
their first GET, HEAD and OPTIONS requests in 0-RTT, before the handshake
finishes. Other requests, such as a POST, wait for the handshake, and a
request the server answers `425 Too Early` is sent again on the same
connection.

## Not supported

- Safari profiles never resume a QUIC connection: every one is a full
  handshake.
- Finding HTTP/3 through DNS HTTPS records. Only the `Alt-Svc` header is
  used.
- Connection migration for Firefox and Safari, path MTU probing, ECN and
  DATAGRAM frames.
- Retrying a site after it is marked as failing. It stays on TCP for the
  Transport's life.
- Keying shared `Alt-Svc` entries by the page that made the request.

The [backlog](../backlog.md#http3-and-quic) lists each gap and what closing
it takes.

Background: [0026](../decisions/0026-http3-capture.md) (how the browsers
were captured), [0027](../decisions/0027-quic-base.md) (the QUIC stack),
[0028](../decisions/0028-quic-failure-model.md) (failures and connection
behaviour), [0036](../decisions/0036-fast-quic-fallback.md) (fast
fallback), [0011](../decisions/0011-connection-partitioning.md)
(partitioning), [0043](../decisions/0043-http3-ack-rate-opt-in.md) (ACK
rate), [0045](../decisions/0045-udp-receive-buffer-default.md) (receive
buffer).
