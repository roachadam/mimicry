# 0066: HTTP/3 through HTTP proxies by CONNECT-UDP over HTTP/3, on request

- Status: Accepted
- Date: 2026-11-09
- Commits: (this change)

**Summary.** `Config.ConnectUDPProxy` (nil by default) lets requests
through an `http://` or `https://` proxy use HTTP/3: each QUIC connection
to a site becomes a CONNECT-UDP stream (RFC 9298, MASQUE) of an HTTP/3
connection to the CONNECT-UDP proxy the function returns for that proxy,
its packets HTTP/3 datagrams in QUIC DATAGRAM frames. The site sees the
profile's QUIC unchanged, from the proxy's address. Every MASQUE service
documented (Bright Data, Oxylabs, Decodo) runs it over HTTP/3 only, on an
endpoint of its own beside its TCP proxy, so that is what is built; no
browser does it. Amends [0038](0038-http3-over-socks5.md).

## Problem

HTTP/3 went only through SOCKS5 proxies (0038). Through an `http://` or
`https://` proxy every request went over TCP, so a site serving HTTP/3 saw
a client that never used it from that address
([#104](https://github.com/roachadam/mimicry/issues/104)). httpcloak
carries QUIC through HTTP proxies with CONNECT-UDP. What needed settling:
which providers offer it and over what, how mimicry is told where it is,
and what the proxy and the site then see.

## Options

- **What providers offer** (read 2026-11-09; anything not stated in a
  source is marked):
  - Bright Data: "supports the UDP MASQUE protocol for QUIC/HTTP/3 on
    port 10001" ([FAQ](https://docs.brightdata.com/products/data-center/faqs)).
    Its sample client [h3-cli](https://github.com/luminati-io/h3-cli)
    (`src/h3/__init__.py`) speaks HTTP/3 to `brd.superproxy.io:10001`:
    the default template `/.well-known/masque/udp/{host}/{port}/`,
    `:protocol connect-udp`, `capsule-protocol: ?1`,
    `proxy-authorization: Basic`, QUIC DATAGRAM frames with context ID 0,
    and wants a `200` with `capsule-protocol: ?1` back. Its regular proxy
    ports (33335, 44445) are other ports; nothing says 10001 also takes
    TCP.
  - Oxylabs: "The MASQUE service runs over `QUIC` on a dedicated `UDP`
    port: masque.oxylabs.io:50000", HTTP/3, ALPN `h3`, datagrams required,
    the RFC 9298 template ("Recommended") or a legacy
    `/masque?h=…&p=…`, `Proxy-Authorization: Basic`, a public CA,
    datagrams over 1500 bytes dropped, and "Outer QUIC (Client-to-Proxy):
    … `1392` bytes. Inner QUIC (Client-to-Target): … `1352` bytes"; for
    TCP, "use our main backconnect endpoint (`pr.oxylabs.io:7777`)"
    ([protocols](https://developers.oxylabs.io/products/proxies/residential-proxies/protocols.md)).
    Residential only; it refuses some UDP targets (Google).
  - Decodo (Smartproxy): "`MASQUE` has its own dedicated gateway … the
    `masque-gate.decodo.com` endpoint on port `42000`. The connection runs
    over QUIC", `Proxy-Authorization: Basic`, datagrams over 1500 bytes
    dropped; its Go example uses quic-go's `http3` with the legacy
    `/masque?h=…&p=…` path; TCP goes to `gate.decodo.com:7000`
    ([protocols](https://help.decodo.com/docs/residential-proxy-protocols.md)).
    [INFERENCE] Whether it also serves the default template is not
    stated.
  - SOAX: "UDP works over **SOCKS5** only"
    ([FAQ](https://developers.soax.com/troubleshooting/faq)). Webshare:
    no UDP at all. IPRoyal, NetNut: nothing found.
  - Not proxies a caller configures: Cloudflare WARP (MASQUE, HTTP/3 to
    Cloudflare), iCloud Private Relay (QUIC; Cloudflare's blog says
    MASQUE), Chrome's IP Protection (CONNECT and CONNECT-UDP to Google's
    proxies, its own token auth).
  - None of the three documents capsules on the request stream, HTTP/2
    extended CONNECT, or HTTP/1.1 Upgrade.
- **What other clients target.** httpcloak (`proxy/masque.go`) speaks
  HTTP/3 only (quic-go's `http3`, `EnableDatagrams`), the default
  template, context ID 0 in DATAGRAM frames, no capsules; it fails when
  SETTINGS lack extended CONNECT or datagrams, and forces its outer
  packets to 1350 bytes. Its provider host list
  (`masque_providers.go`) does not choose the transport: `IsMASQUEProxy`
  takes `masque://` or an `https://` URL whose path says `masque`, and
  TCP goes to a separate `WithSessionTCPProxy` ("HTTP CONNECT for TCP,
  MASQUE for UDP", its docs). quic-go's [masque-go](https://github.com/quic-go/masque-go)
  (MIT) is HTTP/3 only and discards capsules. curl 8.21.0 added
  CONNECT-UDP over HTTP/3, HTTP/2 and HTTP/1.1 proxies (experimental);
  Envoy terminates it from HTTP/3 clients only (alpha).
- **The transport.** (a) Capsules (RFC 9297, section 3.5) on an HTTP/2
  extended CONNECT stream or an HTTP/1.1 Upgrade, through the `http://`
  or `https://` proxy itself: what the issue first sketched, reusing the
  CONNECT paths; but no provider offers it, it puts QUIC behind TCP's
  head-of-line blocking, and every server that offers it (h2o) also
  offers HTTP/3. Rejected. (b) HTTP/3 to the proxy with DATAGRAM frames
  (chosen): what every provider and httpcloak use. It needs DATAGRAM
  frames in `internal/quic` (none there: upstream drops them) and
  extended CONNECT with datagrams in `internal/h3`. The proxy's
  DATAGRAM capsules on the stream are read too (RFC 9297 allows them);
  mimicry never sends any.
- **Telling mimicry where the proxy is.** The providers' CONNECT-UDP
  endpoints are not their TCP proxies (Oxylabs, Decodo say so; Bright
  Data's ports differ), so one URL cannot serve both. (a)
  `Config.HTTP3OverHTTPProxy bool`, HTTP/3 to the proxy's own host and
  port: right for a self-hosted proxy, wrong for every provider. (b) A
  separate `Config.HTTP3Proxy func(*http.Request)`: a second per-request
  choice the pools would have to key by. (c) `Config.ConnectUDPProxy
  func(proxy *url.URL) (*url.URL, error)` (chosen): asked with each
  request's `http://` or `https://` proxy, it returns that proxy's
  CONNECT-UDP proxy, the proxy itself where it serves both, or nil for
  TCP only. Connections, tickets and origin state stay keyed by the TCP
  proxy as before. One option for SOCKS5 and HTTP proxies was not taken:
  `HTTP3OverSOCKS5` is a switch, this needs an address.
- **The outer packets' size.** An inner datagram (1250 bytes for Chrome)
  plus the DATAGRAM frame, quarter stream ID, context ID and the outer
  packet's header and AEAD tag does not fit the profile's own datagram
  size. Shrinking the inner packets would change what the site sees (its
  first flight's sizes). The outer connection's datagrams are the
  profile's plus 64 bytes (Chrome, Edge 1314, Firefox 1316), within
  Oxylabs' 1392 and every provider's 1500; only the proxy sees them.
- **A profile whose QUIC and HTTP/3 announce no datagrams** (Safari, and
  Chrome and Firefox on iOS: no max_datagram_frame_size, no
  SETTINGS_H3_DATAGRAM). Announcing them would invent what the browser
  never sends, even if only to the proxy. Such a profile's requests stay
  on TCP.

## Decision

- `Config.ConnectUDPProxy` (nil by default). For a request to an
  `https://` URL through an `http://` or `https://` proxy, it is asked for
  that proxy's CONNECT-UDP proxy: an `https://` URL (port 443 by
  default), credentials in it. Nil keeps the proxy's requests on TCP;
  another scheme, or no host, fails the request. With one, requests use
  HTTP/3 as directly (Alt-Svc, each profile's race, 0-RTT, fast fallback
  0036, broken origins), keyed per TCP proxy URL as 0038's.
- Each QUIC connection to a site (and each new path Chrome moves to) gets
  its own HTTP/3 connection to the CONNECT-UDP proxy, closed with it, as
  0038 gives each one its own association: a UDP socket as for a site
  (`Config.LocalAddrs`, the receive buffer), the profile's QUIC hello with
  the proxy's host as `server_name`, verified as an `https://` proxy is
  (`RootCAs`, `Pins`, `VerifyConnection`, `KeyLogWriter`), no session
  resumed, the profile's QUIC and HTTP/3 with DATAGRAM frames on and
  datagrams 64 bytes larger. Once its SETTINGS allow extended CONNECT and
  datagrams (and its transport parameters DATAGRAM frames), one
  CONNECT-UDP: `:method CONNECT`, `:protocol connect-udp` after the
  profile's pseudo-header fields, `:scheme https`, `:authority` the
  proxy's host and port, `:path` the default template with the host
  (named, for the proxy to resolve; an IP literal as one,
  percent-encoded), then `capsule-protocol: ?1`, the profile's
  `user-agent` and, with credentials, `proxy-authorization: Basic`. Not
  measured: no browser sends one. Any 2xx opens the tunnel.
- Credentials as for an `http://` proxy (0039): up front by default; with
  `BrowserProxyAuth` the 407 first, answered on a new stream of the same
  connection, unasked from then on where the profile does so.
- Refusals remembered per CONNECT-UDP proxy URL (the dialer's
  `udpRefused`, as 0038's): a proxy whose connection cannot carry it
  (SETTINGS or transport parameters without datagrams or extended
  CONNECT, or a max_datagram_frame_size too small for the site
  connection's datagrams with their two-byte prefix, which would make
  every write fail), or answering 400, 404, 405, 501, or 407 to
  credentials. Its requests go on TCP from then on, without asking again.
  Other answers (403, 5xx) may be about the target (Oxylabs refuses some)
  and fail that attempt only; the origin is then marked broken as any
  failed QUIC attempt. A proxy that never answers is not remembered
  either: each attempt waits up to the race delay. Forgetting the TCP
  proxy (0040) forgets its CONNECT-UDP proxy's state once no other
  remembered proxy maps to it.
- When the proxy ends the stream or the connection, the socket's reads
  fail and the QUIC connection ends at once (`internal/quic` change 14).
- Never a datagram around the proxy: a request through an HTTP proxy
  sends UDP only to the CONNECT-UDP proxy.
- `internal/quic` change 28 (DATAGRAM frames, RFC 9221); `internal/h3`
  `ConnectUDP` and `UDPConn` (a `net.PacketConn`); `internal/dial`
  `ConnectUDP` (the credential flow, shared with CONNECT through
  `tunnel`, and the refusals); the engine's `listenConnectUDP`.

## Evidence

- `TestQUICInterop`'s rows, every profile, against quic-go's server and a
  CONNECT-UDP proxy on quic-go's `http3.Server` (`connectudp_proxy_test.go`)
  beside the harness's CONNECT proxy for TCP:
  - `connect-udp`: credentials up front, then with `BrowserProxyAuth`
    (407, then credentials); each request names the default template
    with `capsule-protocol: ?1`; the server receives the same first flight
    as directly (a tap in front of it: [1250 1250] Chrome, [1252 1252]
    Firefox) and only from the proxy's relay sockets; a 256 KB download
    and 0-RTT through it. Profiles without datagrams (Safari, iOS) stay on
    TCP and never reach the proxy.
  - `connect-udp-refused`: a proxy answering 501, and one whose
    connection has no HTTP/3 datagrams: each request goes on TCP within
    2 s, and the proxy sees one connection (and the 501 one request)
    across two origins; a proxy that never answers: TCP within the race
    delay (5 s in these rows) plus 2 s.
  - `connect-udp-teardown`: the proxy ends the stream under a live
    connection; the next request goes on TCP within 2 s.
- Mutation checks, each failing its row: datagrams written empty
  (`connect-udp`: never on HTTP/3), no room added to the outer packets
  (the same: the inner datagrams do not fit), no `proxy-authorization` on
  the retry (the same), the refusal not remembered (`connect-udp-refused`:
  5 connections, want 1), the stream's end not ending the socket
  (`connect-udp-teardown`: 20 s, the case's limit).
- Unit tests for what no interop row reaches (quic-go's server announces
  a fixed max_datagram_frame_size of 16383; the harness never sweeps
  proxies): `internal/h3`'s `TestConnectUDPNeedsRoomForTheDatagrams` (a
  raw peer announcing 1250 is refused before any request, 1255 is not;
  checking only for a nonzero limit fails it) and the engine's
  `TestSharedConnectUDPProxyIsForgottenLast` (two proxies mapped to one
  CONNECT-UDP proxy: its refusal outlives the first swept; forgetting it
  with the first fails it).
- A smoke run through the harness proxy, with taps on both legs
  (Chrome 156, Firefox 159, Edge 157 on macOS, loopback): the site's first
  flights through the proxy equal the direct ones, resumed included
  (Chrome [1250 1250 81 402]); the proxy's are [1314 1314] (Chrome, Edge)
  and [1316 1316] (Firefox); a new connection and its request took
  1.7–2.2 ms directly and 3.8–4.1 ms through the proxy (the proxy's
  handshake and the CONNECT-UDP: two round trips to the proxy).

## Consequences

- What the site sees: the profile's QUIC and HTTP/3 as directly, from the
  proxy's address, two round trips to the proxy later on each new
  connection (its handshake is never resumed, so 0-RTT to the site still
  waits for them). Its packets to the client must fit, with about 50
  bytes of framing, in the proxy's packets to the client (Chrome's
  max_udp_payload_size 1472, the providers' 1500): a site packet over
  about 1420 bytes, such as a path MTU probe, is dropped by the proxy, so
  the site finds a smaller path MTU than directly.
- What the proxy sees: the target's name and port; the profile's QUIC
  hello and transport parameters to its own host (Chrome and Firefox
  announce max_datagram_frame_size anyway); datagrams 64 bytes larger
  than the browser's; the profile's HTTP/3 SETTINGS (H3_DATAGRAM too, as
  the browser sends); a CONNECT-UDP no browser sends; and, unlike any
  browser behind a proxy, HTTP/3 at all. Documented as a deviation the
  caller chooses (README, proxies.md, EVIDENCE.md).
- The client-to-proxy datagrams are 1342–1344 bytes with IPv4 and UDP
  headers (1362–1364 with IPv6): a path with a smaller MTU drops or
  fragments them, the attempt times out, and requests go on TCP after the
  race delay each time until the origin is marked broken.
- Safari profiles and the iPhone profiles never use HTTP/3 through an
  HTTP proxy.
- Only the default URI template is sent: Decodo documents only its
  legacy path; [INFERENCE] if it serves no other, it answers 404 and is
  remembered as refusing.
- A CONNECT-UDP proxy per connection costs a QUIC handshake each; Chrome's
  IP Protection multiplexes. Not modelled: no browser behind a configured
  proxy does either.

## Revisit when

A provider is found offering CONNECT-UDP over HTTP/2 or HTTP/1.1 only, or
needing a template other than the default; a browser is seen using
HTTP/3 through a configured proxy; a provider's endpoint is tested live
(capsules, response headers, the 1500-byte limit).
