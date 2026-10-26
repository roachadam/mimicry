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
| With credentials | every socket tunnel (and the ws:// one) sends them unasked, after the page's own CONNECT was challenged (three 407-only preconnects first, as for pages) | same, no preconnects | not captured (Safari 27: see the addendum) |

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

## Addendum 2026-10-26: Safari 27 with credentials

`proxy-websocket-auth` on Safari 27 (27.0.1), three samples, taken later
the same day with the `com.apple.proxy.http.global` device profile
(manual proxy at the capture proxy, so no SOCKS5); macOS answered every
407 with the credentials it remembered. Every step opened, echoed and
closed cleanly in every sample. Safari 26 was not captured (no one at
the macOS 26 machine to answer its login dialog).

| Socket | What reached the proxy (all three samples) |
|---|---|
| `px-h2` (wss, extended CONNECT allowed) | a stream on the page's HTTP/2 connection, in the page's tunnel: its CONNECT without credentials, 407, then again on the same connection with `Host`, `Connection`, `Proxy-Authorization`, `Proxy-Connection` |
| `px-h1`, `px-h1-again` (wss, own tunnel) | each its own tunnel, each CONNECT challenged first and sent again on its connection, as the page's |
| `px-plain` (ws://, sent whole) | `GET ws://…` on a proxy connection of its own, `Proxy-Authorization` unasked, right after `Accept-Encoding`; never challenged |

These are Safari 27's page rules from the
[proxies report](proxies-2026-10-24.md): `connect_credentials:
on_challenge` (every tunnel, sockets included), `connect_auth`'s order,
and forwarded requests carrying credentials unasked after
`Accept-Encoding` (`forward_credentials: always`, `forward_auth`). The
four CONNECTs to the plain port before the `http://` page (Safari's
HTTPS upgrade attempts, each challenged) are the page's, as in
`proxy-plain-auth` (four with credentials, one without).

Two things differ between samples, neither in what the proxy sees for a
socket:

- **`Proxy-Authorization` to the server.** In samples 1 and 2 every
  request inside a tunnel carries `Proxy-Authorization` (after
  `Accept-Encoding`; HTTP/1.1 and HTTP/2 alike, the sockets' handshakes
  and extended CONNECT included), where the server reads it. In sample 0
  none does until its `http://` page was challenged (below); the two
  requests after that (`/px/end` and `/done`) carry it. Chrome and
  Firefox never send it inside a tunnel, nor did Safari 27 in
  `proxy-auth` (2026-10-24), which forwards nothing. [INFERENCE] Safari
  (CFNetwork) keeps, per process, that the proxy took its credentials on
  a forwarded request, and from then on adds them to every request
  through it; the samples share one Safari, so samples 1 and 2 start in
  that state.
- **The first forwarded request.** Sample 0's `GET http://…/px/plain`
  went without credentials, was answered 407 and sent again on its
  connection; samples 1 and 2 sent it with them, as every
  `proxy-plain-auth` sample did. The sockets came after it in every
  sample, so the forwarded handshake's "unasked" holds under either
  reading.

### Modelled

- `proxy.websocket.credentials: true` for Safari 27, and for Safari 26
  copied from it (**C** in EVIDENCE.md): its `proxy-websocket` and
  `proxy-websocket-socks5` captures matched Safari 27's on every claim,
  as its other proxy captures did, and its other credential rules are
  already copies. `safari-26-macos.proxy-websocket-auth` stays in
  `missingCaptures`.
- A `ws://` handshake sent whole carries the proxy's credentials as the
  browser's other forwarded requests do (`internal/engine/native`,
  `openWebSocketH1`), placed by `forward_auth`; `credentials` with
  `plain: forward` requires `forward_credentials: always` (no browser
  was measured answering a challenge to a forwarded handshake).
- Not modelled: `Proxy-Authorization` inside tunnels. mimicry is
  Safari's first state (sample 0 before its challenge);
  `TestWebSocketsMatchBrowser` accepts any sample's handshake and logs
  the variance for `px-h2`, `px-h1` and `px-h1-again`. Pinning the
  trigger needs captures from a restarted Safari; sending the caller's
  proxy credentials to every server is a decision of its own (backlog,
  Proxies).

### Checks

`TestWebSocketsMatchBrowser` replays `safari-27-macos/proxy-websocket-auth`
(every socket against every sample: transport, proxy route, handshake,
frames, close). Mutations, each failing it: Safari 27's
`connect_credentials` to `after_challenge` (`px-h1`, `px-h1-again`:
proxy route); `forward_auth` after `accept-language` (`px-plain`:
handshake); dropping the forwarded handshake's credentials in
`openWebSocketH1` (`px-plain`: the proxy's 407, a `*ProxyError`);
`credentials` unset (every socket: `ErrNotMeasured`).
`TestUnmeasuredProxyIsAnError` keeps the unset case with a stripped
Chrome profile, since no built-in profile has it now.
