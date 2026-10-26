# WebSockets through a proxy: Chrome 155, Firefox 158, Safari 27/26, 2026-10-26

Scenarios `proxy-websocket` (HTTP proxy), `proxy-websocket-auth` (HTTP
proxy demanding Basic credentials) and `proxy-websocket-socks5`, three
samples per browser and scenario, taken in one batch: Chrome and Firefox
on this Mac (throwaway profiles, `--proxy-server` and proxy prefs, the 407
answered over DevTools and WebDriver BiDi, as in the
[proxies report](proxies-2026-10-24.md)), Safari 27 here and Safari 26
(macOS 26.2) on the second Mac over ssh, both through the capture tool's
PAC script (set with `networksetup` without `sudo`, turned off after; the
first run on each Mac timed out fetching the script, the second ran).
Safari's `proxy-websocket-auth` was not taken: Safari answers a 407 only
with credentials macOS holds, which needs a device profile and a person
typing into the system's dialog (the proxies report).

## The page

`server.ProxyWebSocketSteps`: the page on `www.ws-a.test`, whose HTTP/2
connections advertise `SETTINGS_ENABLE_CONNECT_PROTOCOL`, fetches from
`h1.ws-a.test`, whose do not (the scenario's `connectHosts`), then opens
`px-h2` (wss, `www`), `px-h1` and `px-h1-again` (wss, `h1`), each sending
5 and 200 bytes and closing; then, but for SOCKS5, it goes to its
`http://` page on the plain port and opens `px-plain` (ws://). The capture
proxy hands what goes to the plain port (tunnels and absolute-form
requests) to the server's plain side, and the server records which proxy
connection carried each socket (`capture.WebSocket.ProxyConn`). SOCKS5
has no ws:// step: Safari's proxy script sends one port (15443) to SOCKS5.

Every step opened, echoed and closed cleanly in every sample of every
browser (the page's own reports, `ws_results`).

## What reaches the proxy

| | Chrome 155 | Firefox 158 | Safari 27, 26 |
|---|---|---|---|
| wss, extended CONNECT allowed (`px-h2`) | a stream on the page's HTTP/2 connection, in the page's tunnel; nothing new to the proxy | same | same |
| wss, not allowed (`px-h1`, `px-h1-again`) | a CONNECT (or SOCKS5) tunnel each, its usual CONNECT lines (`Host`, `Proxy-Connection`, `User-Agent`) | a tunnel each, its usual lines | a tunnel each, its usual lines (no `User-Agent`) |
| ws:// through the HTTP proxy (`px-plain`) | `CONNECT www.ws-a.test:<plain port>`, its usual lines; the handshake inside as sent directly | same | **no CONNECT**: `GET ws://www.ws-a.test:<plain port>/ws/px-plain HTTP/1.1` sent to the proxy on a connection of its own, its header lines exactly those of its direct ws:// handshake (no `Proxy-Connection`) |
| SOCKS5 | `05 01 00`, host name, as for its page | same | same |
| With credentials | every socket tunnel (and the ws:// one) sends them unasked, after the page's own CONNECT was challenged (three 407-only preconnects first, as for pages) | same, no preconnects | not captured |

The handshakes inside the tunnels are the browsers' direct ones (header
lines, HTTP/2 fields, `websocket_h1` hellos); only the request target of
Safari's forwarded ws:// handshake changes.

## Session resumption

| Socket connection | Chrome | Firefox | Safari |
|---|---|---|---|
| Through the HTTP proxy | resumed (both) | **full handshake** (both, every sample) | full (never resumes) |
| Through SOCKS5 | resumed | resumed | full |

Firefox's other connections through an HTTP proxy resume nothing either:
none of the 12 connections of each `proxy-auth` sample (2026-10-24) did,
where Chrome resumed 8. Firefox resumes through SOCKS5 and directly.
`px-h1-again` tells "never through a CONNECT tunnel" from "a cache of the
socket's own": it did not resume `px-h1`'s session either. [INFERENCE] its
TLS session cache key for CONNECT tunnels never matches; not traced in
source.

## Modelled

- `proxy.websocket.plain`: `tunnel` (Chrome, Firefox), `forward` (Safari);
  `proxy.websocket.credentials`: Chrome, Firefox.
- `proxy.connect_no_resume`: Firefox, for every connection through an HTTP
  proxy's CONNECT tunnel, requests included.
- `ErrNotMeasured`: a Safari profile with proxy credentials; `ws://`
  through SOCKS5 (no browser captured); a profile without
  `proxy.websocket`.
- `DialWebSocket` dials through `internal/dial` (`DialVia`, `DialProxy`),
  so a proxy failure is a `*ProxyError` (a 407 to Safari's forwarded
  handshake too), and `Config.Pins` and `VerifyConnection` check the
  server's TLS inside the tunnel.

## Checks

`TestWebSocketsMatchBrowser` replays the three pages through the capture
proxy for every profile (Safari's auth captures listed missing) and
compares each socket with every sample: transport, what reached the proxy
(`checkProxyRoute`), hello, handshake, frames, close. Setting Firefox's
`connect_no_resume` to false fails `px-h1` and `px-h1-again` (their
hellos offer `pre_shared_key` and drop `session_ticket`); setting
Safari 27's `plain` to `tunnel` fails `px-plain` (handshake line and
proxy route). `TestUnmeasuredProxyIsAnError`, `TestProxyErrors` and
`TestPins` hold the error paths.
