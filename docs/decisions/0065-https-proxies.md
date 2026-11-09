# 0065: https:// proxies as each browser uses them

- Status: Accepted
- Date: 2026-11-09
- Commits: `0adcd6c`

**Summary.** `Config.Proxy` takes `https://` proxies: TLS to the proxy with
the profile's ClientHello, then CONNECT over HTTP/2 when the proxy picks
`h2` (every tunnel a stream of one connection, as Chrome, Edge and Firefox
do) or over HTTP/1.1 (each tunnel a TLS connection of its own). A
generated profile section, `proxy.https`, holds what the captures show;
profiles without it (Safari, the iPhone profiles, Chrome 155, Firefox 158)
refuse an `https://` proxy with `ErrNotMeasured`. Amends
[0014](0014-proxies.md).

## Problem

`https://` proxies were refused (0014, amendment 2026-10-30). Some
residential and corporate proxies are reachable only over TLS, and
tls-client takes them ([#105](https://github.com/roachadam/mimicry/issues/105)).
A proxy reached over TLS sees the client's ClientHello, its ALPN offer,
and, over HTTP/2, its SETTINGS, priorities and HPACK: all fingerprintable.

## Options

- **Go's `crypto/tls` to the proxy and HTTP/1.1 CONNECT** (what most Go
  clients do): a Go ClientHello and no HTTP/2, unlike every browser.
- **The profile's hello, HTTP/1.1 CONNECT only, offering `http/1.1`
  alone:** the hello would differ from the browser's (its ALPN offers
  `h2`), and a proxy picking `h2` is what browsers get from most HTTPS
  proxies.
- **The profile's hello and protocols, the browser's CONNECT on each,
  from captures** (chosen).

## Decision

- Scheme `https` (default port 443). TLS to the proxy goes through
  `internal/handshake` with the profile's ClientHello (`plan.Hello`, or
  the resumed one), `server_name` the proxy's host (the trailing-dot rule
  as for a site), verified against `Config.RootCAs` (system roots when
  nil). `Config.VerifyConnection` and `Config.Pins` apply, called with the
  proxy's host: a caller can pin a proxy, and a callback written for sites
  sees the proxy's host too. `Config.KeyLogWriter` receives its secrets.
  `Config.GetClientCertificate` is not asked: a proxy asking for a
  certificate gets none, as from a browser with none (no browser was
  captured answering a proxy's request). A failed TLS handshake with the
  proxy, a pin or `VerifyConnection` included, is a `*ProxyError`.
- The proxy picks the protocol. Over HTTP/2 (`internal/h2`, the profile's
  `http2` section: preface, SETTINGS, WINDOW_UPDATE, HPACK, flow control,
  idle PING, Firefox's GOAWAY on expiry) every tunnel is a CONNECT stream
  (`h2.Conn.Tunnel`) of one connection per proxy URL (the engine's
  `proxyConns` pool, which waits for a first dial's protocol as for a
  site), its HEADERS frame at `proxy.https.weight` with the profile's
  dependency rules, the tunnel a `net.Conn` over DATA frames with
  deadlines, closed with RST_STREAM `proxy.https.reset`. Over HTTP/1.1 the
  CONNECT is `proxy.connect`'s, on a TLS connection per tunnel.
- `proxy.https` (hellogen, from the four `proxy-https*` scenarios):
  `connect` and `connectAuth` (the fields after `:method` and
  `:authority`), `weight`, `reset`, `resume`. hellogen also refuses the
  captures unless every tunnel connection's hello is the profile's, the
  HTTP/2 connections open with `http2.settings` and `windowUpdate`, the
  CONNECT's pseudo-header order follows `http2.pseudoHeaderOrder`, Firefox's
  stream top-up follows each HEADERS, the HTTP/1.1 CONNECTs are
  `proxy.connect`'s, and `proxy.connectNoResume` holds through the https
  proxy's tunnels too (Firefox offers no session through them, Chrome
  does).
- Credentials as for an http proxy (0039): up front by default; with
  `BrowserProxyAuth` the challenge first, answered on a new stream of the
  same HTTP/2 connection or on the same (or a new) HTTP/1.1 connection.
  The flow is one implementation (`dial.Dialer.tunnel`) for both.
- TLS sessions with the proxy are resumed where `proxy.https.resume`
  (Chrome, Edge), kept under `proxy via <hash of the URL>` in the ticket
  store: per proxy URL, credentials included, saved with neither its
  address nor its credentials (`toProxy` in the saved file), dropped when
  the proxy is swept (0040). [INFERENCE] Not partitioned: only the proxy
  sees these connections.
- Plain `http://` requests and `ws://` sockets through an https proxy are
  `ErrNotMeasured` (no capture sends one). `wss://` sockets go through a
  tunnel as any request ([INFERENCE] from 0014's rule for `wss://`).
  HTTP/3 never goes through an https proxy, as browsers send none through
  a CONNECT proxy (`HTTP3OverSOCKS5` is for SOCKS5 only).

## Evidence

Captures `proxy-https`, `proxy-https-h1`, `proxy-https-auth`,
`proxy-https-h1-auth` ([capturing](../capturing.md#https-proxy-scenarios)),
three samples each, for chrome-156-macos/windows, chrome-157-linux,
edge-157-macos/windows and firefox-159-macos/windows/linux (2026-11-09):

- The hello to the proxy is the direct one (resumed: as the direct
  resumed); ALPN `h2`, `http/1.1`; server_name the proxy's name.
- Over HTTP/2: Chromium's CONNECT fields `:method`, `:authority`,
  `user-agent` (`proxy-authorization` last), its HEADERS exclusive at
  weight 146 (its lowest priority), chained as its requests are; Firefox's
  the same fields, weight 21, non-exclusive on stream 0, stream 3 first,
  its 12451840 top-up after each; both their usual SETTINGS and
  WINDOW_UPDATE; every tunnel the browser ended was reset with CANCEL.
- Over HTTP/1.1: the CONNECT lines of `proxy.connect`.
- Credentials: Chrome 3–4, Edge 2, Firefox 1 CONNECT answered 407
  (parallel tunnels before the challenge), then credentials on every
  CONNECT; over HTTP/2 all on one connection.
- Resumption to the proxy over HTTP/1.1: Chromium resumed nearly every
  tunnel connection after the first (12 of 14 on macOS); Firefox resumed
  1–2 of 12 per sample, mostly the page's last.
- Checked by `TestHTTPSProxyMatchesBrowser` (every captured profile, the
  four scenarios), `TestUnmeasuredProxyIsAnError`, `TestProxyErrors`
  (TLS failure, VerifyConnection, refusals and 407s over both protocols),
  `TestTunnelDeadlines` and hellogen's `TestRegenerateReproducesProfiles`.

## Consequences

- A rotating https proxy costs a TLS handshake per new proxy URL, and per
  tunnel over HTTP/1.1 (resumed for Chrome profiles).
- Firefox profiles never resume TLS to the proxy; Firefox did on a few
  connections (EVIDENCE.md, Not covered).
- Firefox's own background connections to the proxy send another hello
  (no session ID, fewer extensions); mimicry makes none.
- Safari cannot be captured: macOS offers no proxy reached over TLS.

## Revisit when

macOS or iOS offers an https proxy; a browser is captured sending plain
HTTP or `ws://` through one, answering a proxy's CertificateRequest, or
partitioning its proxy connections; Firefox's proxy resumption rule is
found.
